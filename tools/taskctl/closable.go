package main

import (
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Вопрос «кого из Check вправе закрыть автоматика» задаёт тик сторожка
// (tools/devkitctl/watch.py): сессия, слившая и прогнавшая задачу, кончается
// раньше закрытия, и агентская строка стоит в Check до следующей живой сессии,
// хотя человеку в ней делать нечего. Ответ считается тут, а не в самом тике:
// вид приёмки, форма файла задачи и ворота закрытия живут у taskctl, и вторая
// копия этих правил в питоне разошлась бы с первой на первой же правке, а
// разошедшаяся копия закрывает чужую строку и пушит это в origin.

// closableRow это строка Check, готовая к закрытию автоматикой, вместе с
// причиной отказа для остальных: список печатается машине, причины человеку,
// который спросил «а почему моя строка стоит».
type closableRow struct {
	ID     string
	Reason string // пусто значит «закрывать можно»
}

// closable отбирает строки Check по четырём условиям: секция, вид приёмки
// agent, действующая на последний выкат отметка smoke и непустой раздел
// «Проверка» в файле задачи. Три последних условия это уже стоящие рубежи
// (LLD DK-292, решение 4 и LLD DK-400, решение 7), собранные в один вердикт.
//
// Файла задачи нет значит закрывать нельзя, и тут вердикт строже ворот close:
// те пропускают безфайловую задачу молча (сценарий живёт в другом документе),
// а автоматике нечего читать, и молчаливое закрытие вслепую хуже строки,
// дождавшейся живой сессии.
func closable(root string, b *Board) []closableRow {
	var out []closableRow
	for _, r := range b.Sects[SectCheck].Rows {
		if kind := acceptOf(r.Title); kind != acceptAgent {
			out = append(out, closableRow{r.ID, "вид приёмки " + kind + ": приёмка за человеком"})
			continue
		}
		if _, _, _, failSuf, _ := splitTitle(r.Title); failSuf != "" {
			out = append(out, closableRow{r.ID, "непогашенный провал проверки: сначала чинится прод"})
			continue
		}
		data, err := os.ReadFile(taskFilePath(root, r.ID))
		if err != nil {
			out = append(out, closableRow{r.ID, "файла задачи нет: закрывать вслепую нечем"})
			continue
		}
		if !taskform.SmokeCovers(string(data)) {
			out = append(out, closableRow{r.ID, "отметки smoke на последний выкат нет: сценарий после выката не прогнан"})
			continue
		}
		if text, found, _ := verificationSection(root, r.ID); !found || strings.TrimSpace(text) == "" {
			out = append(out, closableRow{r.ID, "раздел «Проверка» пуст: вывода прогона в файле задачи нет"})
			continue
		}
		if err := closeVerifyGate(root, r.ID); err != nil {
			out = append(out, closableRow{r.ID, "сценарий прогнал исполнитель разработки: нужен прогон другой моделью"})
			continue
		}
		out = append(out, closableRow{ID: r.ID})
	}
	return out
}

// cmdClosable печатает готовых к закрытию по одному ID в строке, а отказы
// уводит под строку «отказано:». Формат тут контракт с тиком сторожка: тот
// берёт строки до первого отказа и зовёт по каждой `taskctl close`, поэтому
// голый ID в начале вывода значит «закрывать можно», и никакой прозы над ним
// не встаёт.
func cmdClosable(root string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	rows := closable(root, b)
	var ready, refused []string
	for _, r := range rows {
		if r.Reason == "" {
			ready = append(ready, r.ID)
		} else {
			refused = append(refused, "  "+r.ID+": "+r.Reason)
		}
	}
	var out []string
	out = append(out, ready...)
	if len(ready) == 0 {
		out = append(out, "закрывать автоматике нечего")
	}
	if len(refused) > 0 {
		out = append(out, "отказано:")
		out = append(out, refused...)
	}
	return strings.Join(out, "\n"), nil
}

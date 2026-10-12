package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// Метка технического долга (DK-624, развилка «метка»). Долг это строка, после
// которой пользователь снаружи ничего не замечает, а код, тесты или дока
// становятся честнее: стенд без драйвера, вывод подпроцесса на экране,
// невычитанный README. Такие строки по рангу всегда проигрывают живым задачам
// и лежат месяцами, поэтому берёт их не очередь, а скилл board-debt при сдаче
// соседней задачи, чей дифф трогает те же файлы. Метка нужна ему затем, чтобы
// запас был виден одной командой, а не вычитывался из ста пятидесяти строк.
//
// Живёт метка суффиксом заголовка «[долг]» и стоит до «[приёмка: ...]», рядом
// с «[взвод]»: постоянное свойство строки раньше временных пометок (LLD
// DK-292, решение 3). Место выбрано не только порядком. Регулярки пакета
// accept привязаны к концу строки и снимают хвосты, которые стоят после
// приёмки; суффикс до неё им не виден, как не виден и взвод, и потому вид
// приёмки метку переживает без правки разбора.

// debtSufRe разбирает суффикс «[долг]». Содержимого у него нет: строка либо
// долг, либо нет, и градаций у этого признака не бывает.
var debtSufRe = regexp.MustCompile(`\s*\[долг\]\s*$`)

// debtSuffix это сам суффикс с ведущим пробелом, как он ложится в заголовок.
const debtSuffix = " [долг]"

// isDebt говорит, помечена ли строка долгом.
func isDebt(title string) bool {
	_, _, debtSuf, _, _, _, _ := splitTitle(title)
	return debtSuf != ""
}

// DebtParams это аргументы команды `taskctl debt`.
type DebtParams struct {
	Off    bool
	Commit CommitOpts
}

// cmdDebt ставит и снимает метку долга у стоящей строки. Отдельная команда, а
// не ключ `set`, по образцу взвода: признак двоичный, и снимать его надо той же
// командой, какой ставили.
func cmdDebt(root, id string, p DebtParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "debt"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("задачи %s на доске нет", id)
	}
	base, deps, debtSuf, armSuf, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	if p.Off && debtSuf == "" {
		return fmt.Sprintf("%s и так без метки долга", id), nil
	}
	if !p.Off && debtSuf != "" {
		return fmt.Sprintf("%s уже помечена долгом", id), nil
	}
	want := debtSuffix
	if p.Off {
		want = ""
	}
	row.Title = joinTitle(base, deps, want, armSuf, acceptSuf, failSuf, blockSuf)
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		return "", err
	}
	verb := "помечена долгом"
	if p.Off {
		verb = "без метки долга"
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s: %s%s", id, verb, row.Title, tail), nil
}

// openDebtMark пишет открытую отметку долга в раздел «Ход работы» файла
// задачи. Зовёт её перевод в Check: сдача приехала, дифф круга готов, и хвост
// разбора долга виден снаружи, какой бы маршрут строку ни перевёл. Уже
// открытая отметка переживает возврат на доработку молча: последний перевод
// снова ждёт разбора, и второй строки то же состояние не описывает точнее.
// Возврат это относительный путь файла, если отметка легла. Файла нет значит и
// читателя нет, а ронять перевод из-за отметки нельзя, поэтому ошибка пишется
// в хвост сообщения самим вызывающим.
func openDebtMark(root, id string, now time.Time) (string, error) {
	path := taskFilePath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if taskform.DebtHeld(string(data)) {
		return "", nil
	}
	line := fmt.Sprintf("- %s у диффа, %s.", taskform.DebtOpenNote, now.Format("2006-01-02 15:04"))
	if err := os.WriteFile(path, []byte(taskform.InsertIntoSection(string(data), stageSection, line)), 0o644); err != nil {
		return "", err
	}
	return filepath.Join("docs", "tasks", id+".md"), nil
}

// DebtDoneParams это аргументы команды `taskctl debt-done`.
type DebtDoneParams struct {
	Note   string // итог разбора: «пересечений нет» либо «взята <ID долга>»
	Now    time.Time
	Commit CommitOpts
}

// cmdDebtDone закрывает открытую отметку долга итогом разбора. Доску команда
// не трогает, поэтому работает и из дерева задачи: закрывает хвост тот, кто
// разбирал, исполнитель у ручной сдачи и проверяющий у конвейерной. Открытой
// отметки нет значит закрывать нечего, и это не отказ: заказ проверяющего
// называет шаг безусловно, и уже закрытый хвост не должен ронять прогон.
func cmdDebtDone(root, id string, p DebtDoneParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	note := strings.TrimSpace(p.Note)
	if note == "" {
		return "", fmt.Errorf("итог разбора обязателен: \"пересечений нет\" либо \"взята <ID долга>\"")
	}
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("%q не похож на ID задачи", id)
	}
	path := taskFilePath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !taskform.DebtHeld(string(data)) {
		return fmt.Sprintf("%s: открытой отметки долга нет, закрывать нечего", id), nil
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	line := fmt.Sprintf("- %s, %s, %s.", taskform.DebtDoneNote, note, now.Format("2006-01-02 15:04"))
	if err := os.WriteFile(path, []byte(taskform.InsertIntoSection(string(data), stageSection, line)), 0o644); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "tasks", id+".md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: хвост долга закрыт, %s%s", id, note, tail), nil
}

// lintTails ловит сломанный порядок хвостов заголовка. Порядок фиксирован
// (LLD DK-292, решение 3), и разбор снимает хвосты с конца строки: хвост,
// оказавшийся не на своём месте, остаётся в основе заголовка, и тогда взвод,
// вид приёмки или метка долга читаются как отсутствующие. Правится порядок
// руками или повторным вызовом той команды, что ставит хвост.
func lintTails(b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Rows {
		base, _, _, _, _, _, _ := splitTitle(r.Title)
		for _, t := range []string{"[долг]", "[взвод]", "[приёмка: ", "[провал: ", "[блок: ", "[после "} {
			if strings.Contains(base, t) {
				finds = append(finds, fmt.Sprintf("%s:%d: %s: хвост «%s» стоит не на своём месте, порядок такой: [после ...] [долг] [взвод] [приёмка: ...] [провал: ...] [блок: ...]",
					bp, r.LineIdx+1, r.ID, strings.TrimSuffix(t, " ")))
				break
			}
		}
	}
	return finds
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type verdict struct {
	Model  string
	Reason string
	Groom  bool // вердикт «исполнять рано»: сначала грумминг или разбивка
}

// pickModel выводит модель из метаданных строки доски. Порядок правил
// значим: грумминг и разбивка перебивают дешевизну, LLD сильнее всего.
func pickModel(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD") && (r.Cost == "L" || r.Cost == "XL"):
		return verdict{Model: "fable", Reason: "LLD ценой L/XL: сложное проектирование, fable делает его лучше opus"}
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{Model: "opus", Reason: "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{Model: "opus", Reason: fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc), Groom: true}
	case r.Cost == "XL":
		return verdict{Model: "opus", Reason: "цена XL: сначала разбить на серию, целиком не отдавать", Groom: true}
	case r.Cost == "S" && unc == 0:
		return verdict{Model: "haiku", Reason: "совсем атомарная правка с очевидным подходом, дешёвой модели хватает"}
	case (r.Cost == "S" || r.Cost == "M") && unc >= 0 && unc <= 1:
		return verdict{Model: "sonnet", Reason: "подход уже выбран, размышлять не над чем"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{Model: "opus", Reason: "цена не оценена, до оценки модель по умолчанию, не забыть оценить"}
	default:
		return verdict{Model: "opus", Reason: "обычная задача, модель по умолчанию"}
	}
}

// validModels перечисляет допустимые значения override-строки «Модель: ...»
// в файле задачи. Опечатка в имени не должна молча провалиться в обычный
// маппинг, поэтому неизвестное имя это ошибка pick, а не игнорируемая строка.
var validModels = map[string]bool{"haiku": true, "sonnet": true, "opus": true, "fable": true}

// readOverride ищет в файле задачи строку override модели (форматы
// «Модель: opus» и «- Модель: opus», поясняющий хвост в скобках допустим и
// отбрасывается). Нет файла или строки, пустой результат без ошибки: работает
// обычный маппинг pickModel.
func readOverride(root, id string) (string, error) {
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		t = strings.TrimPrefix(t, "- ")
		if !strings.HasPrefix(t, "Модель:") {
			continue
		}
		model := strings.TrimSpace(strings.TrimPrefix(t, "Модель:"))
		if i := strings.Index(model, "("); i >= 0 {
			model = strings.TrimSpace(model[:i])
		}
		if !validModels[model] {
			return "", fmt.Errorf("файл задачи %s: override-строка задаёт неизвестную модель %q, допустимы haiku, sonnet, opus, fable", id, model)
		}
		return model, nil
	}
	return "", nil
}

func cmdPick(root, id string, record bool) (string, error) {
	rows, err := loadRows(root)
	if err != nil {
		return "", err
	}
	r := rowOf(rows, id)
	if r == nil {
		return "", fmt.Errorf("задачи %s нет на доске", id)
	}
	override, err := readOverride(root, id)
	if err != nil {
		return "", err
	}
	v := pickModel(*r)
	if override != "" {
		v = verdict{Model: override, Reason: "модель задана override-строкой файла задачи"}
	}
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	if record {
		if err := recordExecution(root, id, v); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("model: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, r.ID, r.Type, r.Cost, unc, v.Reason), nil
}

// recordExecution дописывает строку исполнения в конец раздела «Ход работы»
// файла задачи (перед хвостовыми пустыми строками, чтобы не оторваться от
// остальных записей); без раздела он добавляется в конец файла. Грумминговый
// вердикт пишется словом «Грумминг»: исполнение по нему не начинается, и
// строка не должна обещать то, чего не было.
func recordExecution(root, id string, v verdict) error {
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("файла задачи нет, завести: taskctl file %s", id)
		}
		return err
	}
	content := strings.TrimRight(string(data), "\n") + "\n"
	label := "Исполнение"
	if v.Groom {
		label = "Грумминг"
	}
	line := fmt.Sprintf("- %s: субагент %s по вердикту pick, %s.",
		label, v.Model, time.Now().Format("2006-01-02"))

	lines := strings.Split(content, "\n")
	head := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## Ход работы") {
			head = i
			break
		}
	}
	if head < 0 {
		content += "\n## Ход работы\n\n" + line + "\n"
		return os.WriteFile(path, []byte(content), 0o644)
	}
	end := len(lines)
	for i := head + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	for end > head+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	ins := []string{line}
	if end == head+1 { // раздел был пуст, отбить запись от заголовка
		ins = []string{"", line}
	}
	lines = append(lines[:end], append(ins, lines[end:]...)...)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

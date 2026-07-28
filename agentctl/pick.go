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
}

// pickModel выводит модель из метаданных строки доски. Порядок правил
// значим: грумминг и разбивка перебивают дешевизну, LLD сильнее всего.
func pickModel(r row) verdict {
	unc := uncertainty(r.Rank)
	switch {
	case strings.EqualFold(r.Type, "LLD"):
		return verdict{"opus", "LLD: дизайн отдаётся сильной модели"}
	case unc >= 4:
		return verdict{"opus", fmt.Sprintf("неопределённость %d: сначала грумминг, исполнять рано", unc)}
	case r.Cost == "XL":
		return verdict{"opus", "цена XL: сначала разбить на серию, целиком не отдавать"}
	case r.Cost == "S" && unc >= 0 && unc <= 1:
		return verdict{"haiku", "мелочь с ясным подходом, дешёвой модели хватает"}
	case r.Cost == "" || r.Cost == "-":
		return verdict{"sonnet", "цена не оценена, до оценки модель по умолчанию"}
	default:
		return verdict{"sonnet", "обычная задача, модель по умолчанию"}
	}
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
	v := pickModel(*r)
	unc := "?"
	if n := uncertainty(r.Rank); n >= 0 {
		unc = fmt.Sprint(n)
	}
	if record {
		if err := recordExecution(root, id, v.Model); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("model: %s\n%s (%s, цена %s, неопределённость %s): %s",
		v.Model, r.ID, r.Type, r.Cost, unc, v.Reason), nil
}

// recordExecution дописывает строку исполнения в конец раздела «Ход работы»
// файла задачи (перед хвостовыми пустыми строками, чтобы не оторваться от
// остальных записей); без раздела он добавляется в конец файла.
func recordExecution(root, id, model string) error {
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("файла задачи нет, завести: taskctl file %s", id)
		}
		return err
	}
	content := strings.TrimRight(string(data), "\n") + "\n"
	line := fmt.Sprintf("- Исполнение: субагент %s по вердикту pick, %s.",
		model, time.Now().Format("2006-01-02"))

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

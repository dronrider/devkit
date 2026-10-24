package taskform

import (
	"os"
	"path/filepath"
	"strings"
)

// GoalKey приводит запись связи с целью к одному виду: цель пишут в разных
// формах (голый ID, путь с docs/tasks/, имя файла с .md), и без приведения
// одна цель, записанная двумя формами, не узнаётся. Ключом служит имя файла
// ссылки без каталога и суффикса. Разбор один на taskctl и дашборд: первый
// считает им потолок висящих вопросов (LLD DK-400, решение 5), второй ставит
// чип цели в строке записи (LLD DK-1422, решение 6), и правка формата меняет
// обоих одной правкой, а не молча разводит их.
func GoalKey(link string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(link), "Цель:"))
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.Index(s[i:], "]"); j > 0 {
			s = s[i+1 : i+j]
		}
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".md")
}

// TaskGoalKey это ID цели, в состав которой входит запись, по строке
// «Цель: ...» шапки файла задачи. Шапкой считается всё до первого «## ».
// Пусто, когда строки нет, ссылки нет, шапка кончилась либо файла цели нет
// на месте: чип цели без самой цели врал бы навигацией в пустоту.
func TaskGoalKey(root, id string) string {
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "## ") {
			return ""
		}
		if !strings.HasPrefix(s, "Цель:") {
			continue
		}
		key := GoalKey(s)
		if key == "" {
			return ""
		}
		if _, err := os.Stat(filepath.Join(root, "docs", "tasks", key+".md")); err != nil {
			return ""
		}
		return key
	}
	return ""
}

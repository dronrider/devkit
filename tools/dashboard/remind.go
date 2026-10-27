package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Напоминание по сроку (DK-1466): раз в день, в час из машинного конфига, демон
// обходит доски проектов и кладёт в журнал уведомителя фоновую строку про
// каждую строку доски со сроком на ближайшие дни либо уже прошедшим. Журнал
// один на машину и читается лентой дашборда, а браузерное уведомление фронтенд
// поднимает по самому событию повода. Отправку держит hooks/notify.py: своего
// канала наружу дашборд не заводит (LLD DK-112), а фоновый уровень не звонит
// человеку системным баннером: напоминание ждёт открытого дашборда, а не
// перебивает работу.

const (
	// remindReason это повод строки журнала: по нему лента узнаёт событие о
	// строке доски, а фронтенд решает, чему поднимать браузерное уведомление.
	remindReason = "task_due"
	// remindStep это шаг круга хранителя. Час напоминания проверяется часто, а
	// сам круг дешевле чтения досок и на пустом месте не ходит.
	remindStep = time.Minute
	// remindMarkRel это файл памяти хранителя от дома: день последнего круга.
	// Перезапуск демона в тот же день второго круга не поднимает, а пропущенный
	// из-за простоя день нагоняется первым кругом после старта.
	remindMarkRel = ".devkit/remind.mark"
)

// remindRound это один круг напоминаний: строки со сроком от всех досок
// машины. Отдельная запись на строку, а не одна на доску: ключ задачи ведёт
// ленту от события к строке доски (DK-323), и сводная запись растеряла бы его.
type remindRow struct {
	Project string
	ID      string
	Title   string
	Due     string
}

// dueRemindKeeper это фоновый круг напоминаний по сроку. Живёт от старта демона
// до его остановки и ходит шагом remindStep, а не подпиской на часы: минута
// опоздания напоминанию безразлична, а свой таймер на каждый час держал бы
// вторую правду о времени рядом с s.now.
func (s *server) dueRemindKeeper(stop <-chan struct{}) {
	for {
		s.dueRemindRound()
		select {
		case <-stop:
			return
		case <-time.After(remindStep):
		}
	}
}

// dueRemindRound делает круг, когда час настал и круг этого дня ещё не ходил.
// Пустой итог тоже закрывает день: доска без сроков в напоминаниях не нуждается,
// и повторять её круг до полуночи незачем.
func (s *server) dueRemindRound() {
	now := s.now()
	if now.Hour() < s.cfg.RemindHour {
		return
	}
	day := now.Format(time.DateOnly)
	mark := filepath.Join(s.cfg.Home, remindMarkRel)
	if data, err := os.ReadFile(mark); err == nil && strings.TrimSpace(string(data)) == day {
		return
	}
	rows := s.dueRemindRows(now)
	for _, r := range rows {
		s.dueRemindSay(r, now)
	}
	if len(rows) > 0 {
		s.logf("напоминание по сроку: %d строк за день %s", len(rows), day)
	}
	if err := os.MkdirAll(filepath.Dir(mark), 0o755); err == nil {
		os.WriteFile(mark, []byte(day), 0o644)
	}
}

// remindBoard это доска в разрезе круга: секции и их строки, из ответа
// taskctl берутся только ID, заголовок и срок.
type remindBoard struct {
	Sections []struct {
		Rows []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Due   string `json:"due"`
		} `json:"rows"`
	} `json:"sections"`
}

// dueRemindRows собирает строки со сроком на окне из конфига: срок наступает
// через remind_days дней или раньше, включая уже прошедший. Отказы чтения
// досок круг молча пропускает: экран назовёт их словами витрины, а напоминание
// не обязано ровняться на самую битую доску машины.
func (s *server) dueRemindRows(now time.Time) []remindRow {
	projects, _ := s.projects()
	horizon := now.AddDate(0, 0, s.cfg.RemindDays)
	var out []remindRow
	for _, p := range projects {
		raw, err := s.projectBoard(p.Path)
		if err != nil {
			continue
		}
		var b remindBoard
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}
		for _, sec := range b.Sections {
			for _, r := range sec.Rows {
				if r.Due == "" || r.Due > horizon.Format(time.DateOnly) {
					continue
				}
				out = append(out, remindRow{Project: p.Name, ID: r.ID, Title: r.Title, Due: r.Due})
			}
		}
	}
	return out
}

// dueRemindSay кладёт одну строку в журнал уведомителя. Провал вызова круг не
// роняет и день не открывает заново: строка окна вернётся завтра, если срок
// ещё стоит, а повторный зов сегодня был бы вторым напоминанием одного дня.
func (s *server) dueRemindSay(r remindRow, now time.Time) {
	np := notifierPath(s.cfg.Roots)
	if np == "" {
		s.logf("напоминание по сроку: %s", notifierMissing)
		return
	}
	title := fmt.Sprintf("%s: %s срок %s", r.Project, r.ID, r.Due)
	cmd := exec.Command("python3", np, "--quiet", "--reason", remindReason,
		"--task", r.ID, "--project", r.Project, title, dueWords(r.Due, now))
	// Зов идёт из корня проекта, как у стопа из дашборда: уведомитель собирает
	// цель перехода по рабочей директории.
	cmd.Dir = s.projectRoot(r.Project)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.logf("напоминание по сроку %s не ушло: %v (%s)", r.ID, err, strings.TrimSpace(string(out)))
	}
}

// dueWords это тело напоминания: сколько осталось до срока либо сколько он
// висит просроченным. Считается по дням календаря, а не по секундам: срок это
// дата суффикса заголовка, часа у него нет.
func dueWords(due string, now time.Time) string {
	d, err := time.ParseInLocation(time.DateOnly, due, now.Location())
	if err != nil {
		return "срок строки в заголовке"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	left := int(d.Sub(today).Hours() / 24)
	switch {
	case left > 1:
		return fmt.Sprintf("до срока %d дней, правка срока на экране задачи", left)
	case left == 1:
		return "срок завтра, правка срока на экране задачи"
	case left == 0:
		return "срок сегодня, правка срока на экране задачи"
	}
	return fmt.Sprintf("срок прошёл %d дней назад, правка срока на экране задачи", -left)
}

// projectRoot это путь дерева проекта по имени. Пустой путь значит «проекта
// нет в корнях», и зов уведомителя тогда идёт из дома демона.
func (s *server) projectRoot(name string) string {
	if p := s.projectNamed(name); p != nil {
		return p.Path
	}
	return s.cfg.Home
}

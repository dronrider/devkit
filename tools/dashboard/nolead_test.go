package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Задача без ведущей сессии (DK-660). Конвейер задачи поднимался и умирал
// молча: сессия исполнителя погибла на середине хода, строка осталась стоять в
// разработке, и снаружи это ничем не отличалось от штатной очереди. Живой
// случай стоил полутора часов простоя, а заметил его человек сам, написав
// задаче в чат.
//
// Правка, от которой стенд краснеет: сессия конвейера встаёт под сторожа
// подъёма, а её смерть при стоящей в разработке строке зовёт человека
// уведомителем.

// noLeadEnv поднимает конвейер задачи XR-004 и отдаёт журнал зовов
// уведомителя. Сессий на машине нет ни до подъёма, ни после: сторож видит имя
// пропавшим и считает сессию умершей.
func noLeadEnv(t *testing.T, id string) (*testEnv, string) {
	t.Helper()
	e, c, _ := runsEnv(t, "")
	calls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), calls)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "`+id+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск %s: %d %s", id, resp.StatusCode, body(t, resp))
	}
	return e, calls
}

// Смерть сессии конвейера при стоящей в разработке строке доходит до человека
// сама: уведомитель зовётся поводом task_nolead, задачей и проектом.
func TestTaskNoLeadCallsNotifier(t *testing.T) {
	e, calls := noLeadEnv(t, "XR-004")
	said := ""
	e.s.logf = func(format string, args ...any) { said += fmt.Sprintf(format, args...) + "\n" }

	e.s.chatWatchTick()

	got := readFile(t, calls)
	if got == "" {
		t.Fatalf("осиротевшая задача прошла молча, журнал дашборда:\n%s", said)
	}
	for _, want := range []string{"--reason\ttask_nolead\t", "--task\tXR-004\t", "--project\tdemo\t"} {
		if !strings.Contains(got, want) {
			t.Errorf("уведомитель позван без %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "XR-004") || !strings.Contains(got, "ведущей сессии") {
		t.Errorf("в уведомлении не сказано, что случилось: %q", got)
	}
	if !strings.Contains(said, "XR-004") {
		t.Errorf("журнал дашборда молчит про осиротевшую задачу: %s", said)
	}
}

// Строка, ушедшая из разработки, человека не будит: сессия конвейера кончается
// и штатно, доработав задачу до проверки, и уведомление тут было бы шумом.
func TestTaskNoLeadSilentWhenRowMoved(t *testing.T) {
	e, calls := noLeadEnv(t, "XR-003")

	e.s.chatWatchTick()

	if got := readFile(t, calls); strings.TrimSpace(got) != "" {
		t.Fatalf("человека позвали к строке, которая ушла из разработки: %q", got)
	}
}

// Лента разводит события по поводам, и осиротевшая задача едет тем же типом,
// что стоп и провал: событие про строку доски. Без этого экран показал бы её
// среди прочих, куда сложены поводы хуков сессии, и фильтр задач её бы не
// нашёл.
func TestNoLeadLineReadsAsTaskEvent(t *testing.T) {
	line := "2026-08-31T16:52:08 сессия - повод " + noLeadReason +
		" уровень громкий бэкенд terminal-notifier цель - задача XR-004 проект demo " +
		"код возврата: 0 текст «demo: XR-004 осталась без ведущей сессии» " +
		"«сессия task-XR-004 прожила 2 мин и кончилась. Строка стоит в разработке.»"
	n, ok := parseNotifyLine(line)
	if !ok {
		t.Fatal("строка про осиротевшую задачу не разобралась")
	}
	if n.Kind != "task" || n.Reason != noLeadReason {
		t.Errorf("тип события: %q (повод %q), ожидал task", n.Kind, n.Reason)
	}
	if n.ID != "XR-004" || n.Project != "demo" {
		t.Errorf("лента не ведёт к строке доски: задача %q, проект %q", n.ID, n.Project)
	}
}

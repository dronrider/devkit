package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
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
	// Повод назван словом, а не константой кода: стенд меряет строку журнала,
	// какой её пишет уведомитель, и с ней же сверяется лента.
	line := "2026-08-31T16:52:08 сессия - повод task_nolead" +
		" уровень громкий бэкенд terminal-notifier цель - задача XR-004 проект demo " +
		"код возврата: 0 текст «demo: XR-004 осталась без ведущей сессии» " +
		"«сессия task-XR-004 прожила 2 мин и кончилась. Строка стоит в разработке.»"
	n, ok := parseNotifyLine(line)
	if !ok {
		t.Fatal("строка про осиротевшую задачу не разобралась")
	}
	if n.Kind != "task" || n.Reason != "task_nolead" {
		t.Errorf("тип события: %q (повод %q), ожидал task", n.Kind, n.Reason)
	}
	if n.ID != "XR-004" || n.Project != "demo" {
		t.Errorf("лента не ведёт к строке доски: задача %q, проект %q", n.ID, n.Project)
	}
}

// Стоп рукой человека тревоги не поднимает. Сессию снял сам человек, о своём
// стопе он знает по строке `run_stop` рядом, и второе уведомление про ту же
// работу было бы шумом. Держится это на порядке: `chatWatchOff` снимает
// сессию с присмотра до `kill-session`.
func TestTaskNoLeadSilentAfterHandStop(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	calls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), calls)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-004"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск XR-004: %d %s", resp.StatusCode, body(t, resp))
	}
	// Стоп ищет работу среди живых, и до нажатия сессия конвейера жива.
	writeTmuxFake(t, e.bin, tmuxLog, `task-XR-004\n`)
	stop := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	if stop.StatusCode != http.StatusOK {
		t.Fatalf("стоп XR-004: %d %s", stop.StatusCode, body(t, stop))
	}
	// Снятая сессия ушла из списка живых, и обход сторожа видит имя пропавшим.
	writeTmuxFake(t, e.bin, tmuxLog, "")

	e.s.chatWatchTick()

	got := readFile(t, calls)
	if strings.Contains(got, "task_nolead") {
		t.Fatalf("человека позвали к работе, которую он снял сам: %q", got)
	}
	// Стоп о себе сказал, и это предусловие стенда: без строки run_stop он
	// проверял бы не ту ветку.
	if !strings.Contains(got, "run_stop") {
		t.Fatalf("стоп прошёл без уведомления, стенд стоит не на той ветке: %q", got)
	}
}

// Живая работа за строкой тревоги не поднимает. Окно конвейера пропало, а
// задачу ведёт сессия человека, поднятая мимо дашборда: работа по строке идёт,
// и звать к ней некого.
func TestTaskNoLeadSilentWhileWorkLives(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	calls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), calls)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-004"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск XR-004: %d %s", resp.StatusCode, body(t, resp))
	}
	writeSession(t, e.home, e.proj, "", "worker", sessionLine("правлю XR-004", "main"), time.Now())
	writeBinds(t, e.home, bindRecord("2026-09-03T01:00:00", "worker", "XR-004", sessions.BySrc))
	// Предусловие стенда: строка помечена живой работой, и помечена не чатом.
	works := e.s.liveWorks(e.proj, "XR", boardRaw(t, e))
	if runMarks(works)["XR-004"] == "" {
		t.Fatalf("живой работы за строкой нет, стенд проверяет не ту ветку: %+v", works)
	}

	e.s.chatWatchTick()

	if got := readFile(t, calls); strings.Contains(got, "task_nolead") {
		t.Fatalf("человека позвали к строке, по которой идёт работа: %q", got)
	}
}

// boardRaw отдаёт доску проекта тем же ответом taskctl, каким её читает сервер.
func boardRaw(t *testing.T, e *testEnv) json.RawMessage {
	t.Helper()
	raw, err := e.s.projectBoard(e.proj)
	if err != nil {
		t.Fatalf("доска фикстуры не прочиталась: %v", err)
	}
	return raw
}

package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Ручная глубина размышления (DK-1570): уровень из ползунка меню едет в
// окружение поднимаемой сессии, и там его читает agentctl pick поверх строки
// «Эффорт:» файла задачи и вердикта. Сторожится тут слово-список, пара
// окружения, кольцо вердикта из ручки меню и проезд выбора в команды запуска
// задачи, цели и разбора.

func TestEffortBadNamesBadWord(t *testing.T) {
	for _, said := range effortLevels {
		if msg := effortBad(said); msg != "" {
			t.Errorf("уровень %q назван кривым: %s", said, msg)
		}
	}
	if msg := effortBad(""); msg != "" {
		t.Errorf("пустой выбор не пустое поле: %s", msg)
	}
	msg := effortBad("highest")
	if msg == "" || !strings.Contains(msg, "highest") || !strings.Contains(msg, "xhigh") {
		t.Fatalf("кривое слово не названо со списком уровней: %q", msg)
	}
}

func TestEffortPairWrapsLevel(t *testing.T) {
	if got := effortPair(""); got != "" {
		t.Errorf("без выбора пара не пустая: %q", got)
	}
	if got := effortPair("xhigh"); got != "DEVKIT_EFFORT='xhigh' " {
		t.Errorf("уровень едет не парой окружения: %q", got)
	}
}

// Ручка меню отдаёт кольцо вердикта: панель рисует уровень pick на шкале, не
// зная самой утилиты. Отказ вердикта не роняет ручку, а называется полем note.
func TestTaskPickMenuVerdict(t *testing.T) {
	t.Run("вердикт назвал уровень", func(t *testing.T) {
		e, c, _ := runsEnv(t, "")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "base")
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-002/pick", "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ручка меню: %d %s", resp.StatusCode, text)
		}
		for _, want := range []string{`"tier":"base"`, `"effort":"high"`} {
			if !strings.Contains(text, want) {
				t.Errorf("ответ без %q: %s", want, text)
			}
		}
		if strings.Contains(text, `"note"`) {
			t.Errorf("живой вердикт принёс лишнюю оговорку: %s", text)
		}
	})

	t.Run("молчащий вердикт остаётся рабочим меню", func(t *testing.T) {
		e, c, _ := runsEnv(t, "")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "")
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-002/pick", "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ручка меню при молчащем вердикте: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, "яруса не назвал") || !strings.Contains(text, `"effort":""`) {
			t.Errorf("молчащий вердикт не назван оговоркой: %s", text)
		}
	})
}

// Выбранный рукой уровень доезжает до команды запуска, кривое слово отбивается
// словами, а без выбора команда остаётся прежней.
func TestRunStartCarriesEffort(t *testing.T) {
	t.Run("задача", func(t *testing.T) {
		e, c, tmuxLog := runsEnv(t, "")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
		writeTaskRunFake(t, filepath.Dir(e.proj))
		useHeadHome(t, e)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
			`{"id": "XR-002", "effort": "xhigh"}`)
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("запуск с ручной глубиной: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, "effort xhigh выбран рукой") {
			t.Errorf("ответ не назвал ручную глубину: %s", text)
		}
		if got := readFile(t, tmuxLog); !strings.Contains(got, "DEVKIT_EFFORT='xhigh'") {
			t.Errorf("глубина не доехала до команды сессии: %s", got)
		}
	})

	t.Run("цель на headless-ступени", func(t *testing.T) {
		e, c, _ := runsEnv(t, "")
		calls := filepath.Join(e.home, "goal-run.calls")
		writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(calls))
		dropGoalClient(t, e)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
			`{"id": "XR-100", "effort": "max"}`)
		if text := body(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("запуск цели с глубиной: %d %s", resp.StatusCode, text)
		}
		if got := readFile(t, calls); !strings.Contains(got, "--effort max") {
			t.Errorf("оболочка цели не получила флаг глубины: %q", got)
		}
	})

	t.Run("кривое слово отбивается", func(t *testing.T) {
		e, c, _ := runsEnv(t, "")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
			`{"id": "XR-002", "effort": "highest"}`)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("кривая глубина прошла: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, "неизвестный effort") {
			t.Errorf("отказ не назвал слово и список уровней: %s", text)
		}
	})

	t.Run("без выбора команда прежняя", func(t *testing.T) {
		e, c, tmuxLog := runsEnv(t, "")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
		writeTaskRunFake(t, filepath.Dir(e.proj))
		useHeadHome(t, e)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
		if text := body(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("запуск без глубины: %d %s", resp.StatusCode, text)
		}
		// Снятие переменной из окружения (ключ -u) в счёт не идёт: гигиена
		// окружения обязана убирать чужую пару, если та как-то стояла.
		if got := readFile(t, tmuxLog); strings.Contains(got, "DEVKIT_EFFORT='") {
			t.Errorf("без выбора в команде появилась пара глубины: %s", got)
		}
	})
}

// Глубина разбора черновика едет той же парой окружения: разбор это та же
// работа агента, и меню у него то же (DK-1570).
func TestGroomCarriesEffort(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	id := makeDraft(t, c, e, "мысль на разбор с глубиной")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"effort": "high"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("разбор с ручной глубиной: %d %s", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "DEVKIT_EFFORT='high'") {
		t.Errorf("глубина не доехала до команды разбора: %s", got)
	}

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"effort": "максимум"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("кривая глубина разбора прошла: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "неизвестный effort") {
		t.Errorf("отказ разбора не назвал слово: %s", text)
	}
}

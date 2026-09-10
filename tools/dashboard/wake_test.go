package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

// Подъём сессии после ответа человека (DK-922). Стенд тот же, что у подъёма
// прогона сценария и второго круга ревью: синтетическая доска, журналируемый
// tmux, фикстуры клиента, прав и оболочки конвейера.

// wakeOne гоняет подъём по одной строке на готовом стенде.
func wakeOne(t *testing.T, e *testEnv, id string) checkRunReport {
	t.Helper()
	raw, err := e.s.projectBoard(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e.s.taskWake(&Project{Name: "demo", Path: e.proj}, id, rows)
}

// Строка припаркована вопросом, ответ лежит во входе: подъём снимает парковку
// и поднимает сессию задачи. До этой правки ответ человека возвращал строку в
// работу и на этом всё кончалось: оболочка конвейера уже вышла по стопу
// wait_human, окна не было, и задача стояла с непрочитанной репликой во входе,
// пока человек сам не нажмёт «Запуск».
func TestTaskWakeRaisesParkedRow(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)

	rep := wakeOne(t, e, "XR-7")
	if !rep.Raised || rep.Failed {
		t.Fatalf("сессия не поднята: %+v", rep)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, "new-session -d -s task-XR-7") {
		t.Fatalf("tmux позван не так: %q", got)
	}
	if !strings.Contains(got, "--order 'Продолжай выполнение XR-7'") {
		t.Errorf("заказ поднятой сессии не про продолжение: %s", got)
	}
	// Признак hidden тут не ставится: подъём начал человек своим ответом, и
	// разговор обязан остаться в списке панели, а не пропасть из неё.
	if strings.Contains(got, hiddenEnv) {
		t.Errorf("сессия ответа поднята скрытой от списка панели: %s", got)
	}
}

// Парковка снимается тем же вызовом taskctl, каким её снимает тик сторожка:
// оболочка конвейера спрашивает доску предполётом, и поверх строки в Blocked
// она вышла бы тем же стопом wait_human, с которого всё началось.
func TestTaskWakeUnparksBeforeRaise(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	moved := filepath.Join(e.home, "moved.log")
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move XR-7 in-progress"*) echo "$@" >> `+moved+`; echo '{"ok":true}';;
*) echo '`+chatParkedBoard+`';;
esac`)
	if err := chat.WriteAsk(e.proj, "task-XR-7", chat.Ask{Task: "XR-7",
		Questions: []chat.Question{{Text: "какую схему брать"}}}); err != nil {
		t.Fatal(err)
	}

	rep := wakeOne(t, e, "XR-7")
	if !rep.Raised || rep.Failed {
		t.Fatalf("сессия не поднята: %+v", rep)
	}
	args := readFile(t, moved)
	if !strings.Contains(args, "move XR-7 in-progress") || !strings.Contains(args, "--push") {
		t.Fatalf("строка не сведена в In progress коммитом и пушем: %q", args)
	}
	if !strings.Contains(args, "docs(tasks): XR-7 разбуждена ответом") {
		t.Errorf("возврат строки уехал без своего текста коммита: %q", args)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-7")); ok {
		t.Error("признак ожидания пережил подъём: панель рисовала бы вопрос уже не ждущей строке")
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "new-session -d -s task-XR-7") {
		t.Errorf("сессия не поднята после снятия парковки: %s", got)
	}
}

// Признак ожидания лежит в дереве задачи: спрашивают чаще всего оттуда, а
// отвечают в панели основного чекаута. Переживи он подъём, панель показывала
// бы вопрос уже разбуженной строке до второго вопроса или руки человека.
func TestTaskWakeDropsAskInTaskTree(t *testing.T) {
	e, _ := parkedRaiseEnv(t)
	side := sideTree(t, e.proj, "xr-7")
	if err := chat.WriteAsk(side, "task-XR-7", chat.Ask{Task: "XR-7",
		Questions: []chat.Question{{Text: "какую схему брать"}}}); err != nil {
		t.Fatal(err)
	}

	if rep := wakeOne(t, e, "XR-7"); !rep.Raised {
		t.Fatalf("сессия не поднята: %+v", rep)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(side, "task-XR-7")); ok {
		t.Error("признак в дереве задачи пережил подъём")
	}
}

// Работа по строке уже идёт: поднимать поверх живой сессии нечего, лежащую
// реплику заберёт её же подхват первым ходом.
func TestTaskWakeSkipsLiveSession(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	writeTmuxFake(t, e.bin, tmuxLog, "task-XR-7\\n")

	rep := wakeOne(t, e, "XR-7")
	if rep.Raised || rep.Failed {
		t.Fatalf("подъём поверх живой сессии: %+v", rep)
	}
	if !strings.Contains(rep.Line, "работа уже идёт") {
		t.Errorf("отчёт не назвал причину: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("поверх живой сессии поднята вторая: %s", got)
	}
}

// Цель на вопросе не умирает: её цикл сторожит признак сам, и сессия задачи ей
// не нужна вовсе.
func TestTaskWakeSkipsGoal(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)

	rep := wakeOne(t, e, "XR-100")
	if rep.Raised || rep.Failed {
		t.Fatalf("цели подняли сессию задачи: %+v", rep)
	}
	if !strings.Contains(rep.Line, "это цель") {
		t.Errorf("отчёт не назвал причину: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("цели поднята сессия задачи: %s", got)
	}
}

// Строки нет на доске: поднимать нечего, и это поломка, а не молчаливый
// пропуск.
func TestTaskWakeRefusesUnknownRow(t *testing.T) {
	e, _ := parkedRaiseEnv(t)

	rep := wakeOne(t, e, "XR-777")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём по несуществующей строке: %+v", rep)
	}
	if !strings.Contains(rep.Line, "строки нет на доске") {
		t.Errorf("отчёт не назвал причину: %q", rep.Line)
	}
}

// Прав машинного контура на машине нет: сессия без них встанет на первом же
// запросе разрешения, одобрить который в окне без человека некому.
func TestTaskWakeRefusesWithoutPerms(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	writePermsFake(t, filepath.Dir(e.proj), true)

	rep := wakeOne(t, e, "XR-7")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём без прав не отказал: %+v", rep)
	}
	if !strings.Contains(rep.Line, "прав машинного контура") {
		t.Errorf("в отказе нет причины: %s", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия всё-таки поднята: %s", got)
	}
}

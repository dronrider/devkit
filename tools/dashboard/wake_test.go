package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskhead"
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

// parkedMoveEnv это тот же стенд с журналом ходов доски: поддельный taskctl
// пишет всякий move строки в файл, а признак ожидания лежит там, где его
// оставил бы вопрос. По этим двум следам и видно, где строка осталась стоять.
func parkedMoveEnv(t *testing.T) (e *testEnv, tmuxLog, moved string) {
	t.Helper()
	e, tmuxLog = parkedRaiseEnv(t)
	moved = filepath.Join(e.home, "moved.log")
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move XR-7"*) echo "$@" >> `+moved+`; echo '{"ok":true}';;
*) echo '`+chatParkedBoard+`';;
esac`)
	if err := chat.WriteAsk(e.proj, "task-XR-7", chat.Ask{Task: "XR-7",
		Questions: []chat.Question{{Text: "какую схему брать"}}}); err != nil {
		t.Fatal(err)
	}
	return e, tmuxLog, moved
}

// Подъём отказал: строка обязана остаться припаркованной вопросом. Парковка это
// единственное, чем строка видна следующему заходу: тик берёт будимых из
// Blocked, а признак ожидания рисует вопрос в панели. Снятая до отказа, она
// оставляла бы строку в работе без сессии и без признака, и поднять её было бы
// некому, кроме руки человека.
func TestTaskWakeKeepsRowParkedOnRefusal(t *testing.T) {
	e, tmuxLog, moved := parkedMoveEnv(t)
	writePermsFake(t, filepath.Dir(e.proj), true)

	rep := wakeOne(t, e, "XR-7")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём без прав не отказал: %+v", rep)
	}
	if args := readFile(t, moved); args != "" {
		t.Errorf("строку увели из Blocked отказавшим подъёмом: %q", args)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-7")); !ok {
		t.Error("признак ожидания снят отказавшим подъёмом: панель потеряет вопрос, а тик строку")
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия всё-таки поднята: %s", got)
	}
}

// Оболочки конвейера на машине нет: подъём отказывает до всякой правки доски.
// Причина эта живёт долго, и снятая под неё парковка стоила бы двух коммитов
// доски на каждом тике.
func TestTaskWakeKeepsRowParkedWithoutTaskRun(t *testing.T) {
	e, _, moved := parkedMoveEnv(t)
	if err := os.Remove(filepath.Join(filepath.Dir(e.proj), "devkit", "kit", "skills",
		"board-task", "task-run.py")); err != nil {
		t.Fatal(err)
	}

	rep := wakeOne(t, e, "XR-7")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём без оболочки конвейера не отказал: %+v", rep)
	}
	if !strings.Contains(rep.Line, "task-run.py") {
		t.Errorf("в отказе нет причины: %s", rep.Line)
	}
	if args := readFile(t, moved); args != "" {
		t.Errorf("строку увели из Blocked без оболочки конвейера: %q", args)
	}
}

// Бинарь tmux на месте, а сервер его не отвечает. Отказ этот затяжной: пока
// сокет сорван, он повторится на каждом заходе тика. Спрошен он обязан быть до
// снятия парковки, иначе строка каждые пять минут уходила бы в работу и
// возвращалась назад, а доска получала бы по два коммита с пушем на заход.
func TestTaskWakeKeepsRowParkedWhenServerIsDown(t *testing.T) {
	e, tmuxLog, moved := parkedMoveEnv(t)
	writeScript(t, e.bin, "tmux", `echo "$@" >> `+tmuxLog+`
case "$1" in
ls) printf 'чужая-сессия\n';;
start-server) echo "tmux: сервер не отвечает" >&2; exit 1;;
esac
exit 0`)

	rep := wakeOne(t, e, "XR-7")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём при мёртвом сервере tmux не отказал: %+v", rep)
	}
	if !strings.Contains(rep.Line, "сервер tmux не отвечает") {
		t.Errorf("в отказе нет причины: %s", rep.Line)
	}
	if args := readFile(t, moved); args != "" {
		t.Errorf("доску подвигали при мёртвом сервере tmux: %q", args)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-7")); !ok {
		t.Error("признак ожидания снят отказавшим подъёмом: панель потеряет вопрос, а тик строку")
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("окно подняли поверх мёртвого сервера: %s", got)
	}
}

// Сессия не поднялась там, где парковка уже снята: строка возвращается в
// Blocked тем же вопросом. Иначе она стояла бы в работе без сессии и без
// признака ожидания, а тик берёт будимых только из Blocked, и повторить подъём
// было бы некому.
func TestTaskWakeReparksAfterFailedStart(t *testing.T) {
	e, tmuxLog, moved := parkedMoveEnv(t)
	// Сервер tmux отвечает, а окна не поднимаются, и оболочка, запущенная
	// ступенью headless, замка не принимает: так отказывает уже сам подъём, за
	// снятой парковкой, и лестнице поднять голову нечем (DK-935).
	writeScript(t, e.bin, "tmux", `echo "$@" >> `+tmuxLog+`
case "$1" in
ls) printf 'чужая-сессия\n';;
new-session) echo "tmux: сервер не отвечает" >&2; exit 1;;
esac
exit 0`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.proj), "devkit", "kit", "skills",
		"board-task", "task-run.py"), []byte("import sys\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(taskhead.AdoptEnv, "1")

	rep := wakeOne(t, e, "XR-7")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём упавшего tmux не отказал: %+v", rep)
	}
	args := readFile(t, moved)
	if !strings.Contains(args, "move XR-7 in-progress") {
		t.Fatalf("парковка не снималась вовсе, случай не тот: %q", args)
	}
	if !strings.Contains(args, "move XR-7 blocked") {
		t.Fatalf("строка осталась в работе без сессии: %q", args)
	}
	if !strings.Contains(args, "вопрос: какую схему брать") {
		t.Errorf("строка вернулась в Blocked не тем вопросом: %q", args)
	}
	if !strings.Contains(args, "--push") {
		t.Errorf("возврат в Blocked оставил правку доски грязной: %q", args)
	}
	if !strings.Contains(rep.Line, "возвращена в Blocked") {
		t.Errorf("отчёт молчит о судьбе строки: %q", rep.Line)
	}
}

// Замок держит голова, поднятая мимо дашборда: вторая рядом не встаёт, а
// строка в Blocked не возвращается, потому что ответ дойдёт до живой головы,
// как до живой tmux-сессии (DK-935).
func TestTaskWakeBusyLockLeavesHeadAlone(t *testing.T) {
	e, tmuxLog, moved := parkedMoveEnv(t)
	holdHeadLock(t, e.home, "XR-7")

	rep := wakeOne(t, e, "XR-7")
	if rep.Failed || rep.Raised {
		t.Fatalf("занятый замок отчитан не как живая работа: %+v", rep)
	}
	if !strings.Contains(rep.Line, "подъём не нужен, работа уже идёт (голова уже поднята: замок ") {
		t.Errorf("отчёт не назвал замок: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("поверх занятого замка поднято окно: %s", got)
	}
	if args := readFile(t, moved); strings.Contains(args, "move XR-7 blocked") {
		t.Errorf("строку вернули в Blocked при живой голове: %q", args)
	}
}

// Цель припаркована вопросом: сессию задачи ей поднимать нечего, а парковку
// снять надо. Ответ лежит во входе, цикл цели прочитает его сам, но строка,
// оставленная в Blocked, стояла бы там с вопросом, на который уже ответили.
func TestTaskWakeUnparksGoal(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	moved := filepath.Join(e.home, "moved.log")
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move XR-100"*) echo "$@" >> `+moved+`; echo '{"ok":true}';;
*) echo '`+parkedGoalBoard+`';;
esac`)

	rep := wakeOne(t, e, "XR-100")
	if rep.Raised || rep.Failed {
		t.Fatalf("цели подняли сессию задачи: %+v", rep)
	}
	if args := readFile(t, moved); !strings.Contains(args, "move XR-100 in-progress") {
		t.Errorf("припаркованная цель осталась в Blocked с отвеченным вопросом: %q", args)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("цели поднята сессия задачи: %s", got)
	}
}

// parkedGoalBoard это доска с целью, припаркованной вопросом: своей строки
// такого вида у общего стенда нет, а разбирается она отдельной дорогой.
const parkedGoalBoard = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"XR-100","title":"Цель: пробный цикл","block":"вопрос: резать ли цель",` +
	`"type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"}]}]}`

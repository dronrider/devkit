package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const record = "%s сессия %s задача %s проект devkit дерево /Users/r/projects/devkit-dk-432 " +
	"транскрипт /Users/r/.claude/projects/devkit/%s.jsonl источник заказ повод startup tmux task-%s\n"

func line(when, sid, task string) string {
	return fmt.Sprintf(record, when, sid, task, sid, task)
}

// Строка реестра разбирается по ключевым словам, а не по позициям: значение
// поля собирается до следующего слова, поэтому путь с пробелом строку не
// рассыпает.
func TestParseLineKeepsSpacedPaths(t *testing.T) {
	sid, b, ok := ParseLine("2026-08-19T12:00:00 сессия aaa-1 задача dk-432 проект devkit " +
		"дерево /Users/r/my projects/devkit транскрипт /tmp/t.jsonl источник рука повод hand tmux -")
	if !ok {
		t.Fatal("строка не разобралась")
	}
	if sid != "aaa-1" || b.Task != "DK-432" || b.Tree != "/Users/r/my projects/devkit" {
		t.Fatalf("разбор: %q %+v", sid, b)
	}
	if b.Tmux != "" {
		t.Fatalf("пустое поле дефисом читается пустым, а вышло %q", b.Tmux)
	}
}

// Родитель это разговор, раздавший работу: по нему список чатов отличает
// розданную работу от разговора человека, и старая строка без поля читается
// сессией без родителя, а не ломает разбор соседнего поля.
func TestParseLineReadsParent(t *testing.T) {
	_, b, ok := ParseLine("2026-08-29T12:00:00 сессия bbb-2 задача DK-581 проект devkit " +
		"дерево /Users/r/projects/devkit транскрипт /tmp/t.jsonl источник заказ повод startup " +
		"tmux - родитель aaa-1")
	if !ok {
		t.Fatal("строка не разобралась")
	}
	if b.Parent != "aaa-1" {
		t.Fatalf("родитель %q, жду aaa-1", b.Parent)
	}
	if b.Tmux != "" {
		t.Fatalf("поле родителя утекло в соседнее: tmux %q", b.Tmux)
	}
	_, old, _ := ParseLine(line("2026-08-29T12:00:00", "ccc-3", "DK-581"))
	if old.Parent != "" {
		t.Fatalf("у строки без поля родителя нет, а вышло %q", old.Parent)
	}
	// Свёртка записей сессии родителя не теряет: сессия пишет строку и на
	// старте, и на каждом compact, а розданная работа остаётся розданной.
	if got := Last([]Bind{b, old}).Parent; got != "aaa-1" {
		t.Fatalf("свёртка потеряла родителя: %q", got)
	}
}

// Чужая строка в общем журнале не обрушает разбор: она просто пропускается.
func TestParseSkipsForeignLines(t *testing.T) {
	binds := Parse([]byte("мусор без штампа\n" + line("2026-08-19T12:00:00", "aaa-1", "DK-1")))
	if len(binds) != 1 || binds["aaa-1"].Task != "DK-1" {
		t.Fatalf("свёртка: %+v", binds)
	}
}

// Задачу ведёт последняя записанная сессия: заходов у задачи бывает несколько
// подряд, и вопрос ожидания идёт последнему.
func TestLeadsTakesTheFreshestRecord(t *testing.T) {
	binds := Parse([]byte(
		line("2026-08-19T10:00:00", "aaa-1", "DK-432") +
			line("2026-08-19T12:00:00", "bbb-2", "DK-432") +
			line("2026-08-19T13:00:00", "ccc-3", "DK-9")))
	sid, rec := binds.Leads("dk-432")
	if sid != "bbb-2" || rec.Task != "DK-432" {
		t.Fatalf("ведущая сессия: %q %+v", sid, rec)
	}
	if sid, _ := binds.Leads("DK-777"); sid != "" {
		t.Fatalf("реестр назвал сессию задаче, которой в нём нет: %q", sid)
	}
}

// Отвязка это обычная запись с пустой задачей, и выигрывает она как последняя:
// иначе кнопка отвязки ничего не меняла бы.
func TestLeadsForgetsTheUnbound(t *testing.T) {
	binds := Parse([]byte(
		line("2026-08-19T10:00:00", "aaa-1", "DK-432") +
			"2026-08-19T11:00:00 сессия aaa-1 задача - проект devkit дерево - транскрипт - источник снята повод рука tmux -\n"))
	if sid, _ := binds.Leads("DK-432"); sid != "" {
		t.Fatalf("снятая привязка вернулась сессией %q", sid)
	}
}

// Реестра нет вовсе: на машине без хука старта это пустая привязка, а не отказ.
func TestLoadWithoutRegistry(t *testing.T) {
	if binds := Load(t.TempDir()); len(binds) != 0 {
		t.Fatalf("пустой дом: %+v", binds)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), []byte(line("2026-08-19T12:00:00", "aaa-1", "DK-1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if binds := Load(home); binds["aaa-1"].Task != "DK-1" {
		t.Fatalf("реестр не прочитан: %+v", binds)
	}
}

// Запись по факту работы кладут утилиты доски (taskctl move, shipctl merge), и
// про tmux им не известно ничего: имя сессии в такой строке стоит прочерком.
// Взяв её целиком, реестр забывал имя живой сессии, и дашборд переставал
// видеть, чем её снимать: живая работа показывалась «поднятой вне дашборда»
// (живой случай сессии chat-DK-397-1). Свёртка добирает поля не задачные из
// прежних записей, как это делает Last.
func TestParseKeepsTmuxAcrossWorkRecords(t *testing.T) {
	sid := "8257b5e0-a1a7-424c-a334-d9e7d23bccf5"
	log := "2026-08-25T12:00:00 сессия " + sid + " задача - проект devkit дерево /p " +
		"транскрипт /p/t.jsonl источник - повод startup tmux chat-DK-397-1\n" +
		"2026-08-25T15:24:48 сессия " + sid + " задача XR-004 проект - дерево /tmp/wt " +
		"транскрипт - источник работа повод taskctl move XR-004 tmux -\n"
	got := Parse([]byte(log))
	b, ok := got[sid]
	if !ok {
		t.Fatal("сессии в реестре нет вовсе")
	}
	if b.Tmux != "chat-DK-397-1" {
		t.Errorf("имя tmux-сессии затёрто записью работы: %q", b.Tmux)
	}
	if b.Transcript != "/p/t.jsonl" {
		t.Errorf("путь транскрипта затёрт записью работы: %q", b.Transcript)
	}
	// Задача и повод берутся у свежей записи: их запись работы и несёт.
	if b.Task != "XR-004" || b.Source != "работа" {
		t.Errorf("свежая запись не выиграла задачей: %+v", b)
	}
}

// Критерий рабочей сессии (DK-716). Работой сессию делает слово источника, а
// не имя её окна: до этой правки дашборд решал по префиксу tmux, и доводящий
// чат chat-<ID>-<n> вёл задачу целый вечер при пустом признаке на строке.
func TestWorksStandsOnSourceNotWindow(t *testing.T) {
	sid := "aaaa1111-1111-4111-8111-111111111111"
	log := "2026-09-03T10:00:00 сессия " + sid + " задача XR-1 проект devkit дерево /p " +
		"транскрипт /p/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n" +
		"2026-09-03T10:05:00 сессия " + sid + " задача XR-1 проект - дерево /p " +
		"транскрипт - источник работа повод «taskctl move XR-1» tmux -\n"
	recs := All([]byte(log))[sid]
	if !WorksOn(recs, "XR-1") {
		t.Errorf("сессия двигала строку, а работой её не считают: %+v", Works(recs))
	}
	// Регистр ID приходит с экрана и из команд вперемешку, и критерий обязан
	// узнавать задачу в любом.
	if !WorksOn(recs, "xr-1") {
		t.Error("критерий не узнал задачу в нижнем регистре")
	}
}

// Подъём разговора работой не считается: кнопка чата дашборда поднимает
// разговор о задаче тем же словом «заказ», каким поднимается конвейер, и по
// нему всякий открытый ради вопроса чат отбирал бы у строки кнопку запуска
// (границы DK-716, защита DK-460).
func TestWorksIgnoresOrderAndHand(t *testing.T) {
	sid := "bbbb2222-2222-4222-8222-222222222222"
	log := "2026-09-03T10:00:00 сессия " + sid + " задача XR-2 проект devkit дерево /p " +
		"транскрипт /p/t.jsonl источник заказ повод startup tmux chat-XR-2-1\n" +
		"2026-09-03T10:01:00 сессия " + sid + " задача XR-3 проект devkit дерево /p " +
		"транскрипт - источник рука повод рука tmux -\n"
	recs := All([]byte(log))[sid]
	if got := Works(recs); len(got) != 0 {
		t.Errorf("разговор о задаче объявлен работой: %+v", got)
	}
	// Касание при этом остаётся касанием: список чатов подписывает разговор
	// той задачей, о которой в нём говорят, и обе записи ему нужны.
	if got := Touched(recs); len(got) != 2 {
		t.Errorf("касания потерялись вместе с работой: %+v", got)
	}
}

// Работ у сессии столько, скольких задач она коснулась командой доски: строку
// показывает каждая, пока работа по ней не кончилась.
func TestWorksKeepsEveryTaskUntilRelease(t *testing.T) {
	sid := "cccc3333-3333-4333-8333-333333333333"
	log := "2026-09-03T10:00:00 сессия " + sid + " задача XR-1 проект devkit дерево /p " +
		"транскрипт - источник работа повод «taskctl move XR-1» tmux -\n" +
		"2026-09-03T10:10:00 сессия " + sid + " задача XR-2 проект devkit дерево /p " +
		"транскрипт - источник работа повод «taskctl move XR-2» tmux -\n"
	recs := All([]byte(log))[sid]
	want := []string{"XR-2", "XR-1"}
	if got := Works(recs); !reflect.DeepEqual(got, want) {
		t.Fatalf("рабочие задачи %+v, ожидал свежими сверху %+v", got, want)
	}
	// Именная отвязка снимает одну задачу: работа по строке кончается своим
	// порядком, у закрытия и у перевода из in-progress, а соседняя задача той
	// же сессии в этот момент идёт дальше.
	off := All([]byte(log + "2026-09-03T10:20:00 сессия " + sid + " задача XR-1 проект devkit " +
		"дерево /p транскрипт - источник снята повод «taskctl close XR-1» tmux -\n"))[sid]
	if got := Works(off); !reflect.DeepEqual(got, []string{"XR-2"}) {
		t.Errorf("именная отвязка сняла не одну задачу: %+v", got)
	}
	if !Off(off, "XR-1") || Off(off, "XR-2") {
		t.Errorf("Off ответил не по названной задаче: XR-1=%v XR-2=%v", Off(off, "XR-1"), Off(off, "XR-2"))
	}
	// Отвязка рукой с экрана задачи не называет, и она снимает сессию целиком:
	// человек сказал, что работой это не считается.
	all := All([]byte(log + "2026-09-03T10:20:00 сессия " + sid + " задача - проект devkit " +
		"дерево /p транскрипт - источник снята повод рука tmux -\n"))[sid]
	if got := Works(all); len(got) != 0 {
		t.Errorf("отвязка рукой оставила работы: %+v", got)
	}
	if !Off(all, "XR-2") {
		t.Error("отвязка рукой не сняла задачу бокового дерева")
	}
}

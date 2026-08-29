package sessions

import (
	"fmt"
	"os"
	"path/filepath"
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

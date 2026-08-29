package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ход розданной работы в ленте раздавшего разговора (DK-581). Живого клиента
// тут нет, и подпроцесс изображает sh: он заводит транскрипт под домом чужой
// подписки ровно там, где его завёл бы клиент, тем именем сессии, которое ему
// дали. Проверяется от этого не меньше: наружу видны и мета-файл, и ссылка на
// транскрипт, и имя раздавшего разговора в окружении подпроцесса.

// sideProfile это харнес второй подписки: команда принимает имя сессии,
// заводит по нему транскрипт под своим каталогом конфигурации и записывает
// доставшееся имя раздавшего разговора в рабочую директорию.
const sideProfile = `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "mkdir -p $CLAUDE_CONFIG_DIR/projects/-w && printf x > $CLAUDE_CONFIG_DIR/projects/-w/$1.jsonl && printf %s $DEVKIT_PARENT_SESSION > parent.txt", "sh", "{session}"]

[hooks]

[quota]
`

const sideMachine = "enabled = [\"sidecli\"]\ndefault = \"sidecli\"\n\n[sidecli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\nhome = \"%s\"\nenv = [\"CLAUDE_CONFIG_DIR={home}\"]\n"

// parentTranscript кладёт транскрипт раздавшего разговора под своё хозяйство и
// называет его текущей сессии: имя сессии в окружении это всё, что о ней
// известно утилите.
func parentTranscript(t *testing.T, home, sid string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-Users-me-projects-devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", sid)
	return path
}

// sideFiles отдаёт содержимое каталога боковых журналов при транскрипте.
func sideFiles(t *testing.T, transcript string) (dir string, names []string) {
	t.Helper()
	dir = filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("каталога боковых журналов нет: %v", err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return dir, names
}

// TestRunCLISideLog: делегирование на вторую подписку встаёт боковым журналом
// при транскрипте раздавшего разговора. Мета-файл называет работу, журнал это
// ссылка на транскрипт подпроцесса, и по ней ход работы читается живьём.
func TestRunCLISideLog(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "sidecli", sideProfile)
	glm := filepath.Join(kit, "glm")
	writeMachine(t, kit, strings.Replace(sideMachine, "%s", glm, 1))
	root := writeBoard(t)
	transcript := parentTranscript(t, kit, "1e1e1e1e-2222-4333-8444-555555555555")
	work := realPath(t, t.TempDir())

	code, out := runOut(t, root, "T-001", roleExec, work)
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	if !strings.Contains(out, "ход работы едет в ленту разговора") {
		t.Fatalf("про боковой журнал наружу не сказано:\n%s", out)
	}

	dir, names := sideFiles(t, transcript)
	var meta, log string
	for _, n := range names {
		switch {
		case strings.HasSuffix(n, ".meta.json"):
			meta = n
		case strings.HasSuffix(n, ".jsonl"):
			log = n
		}
	}
	if meta == "" || log == "" {
		t.Fatalf("в каталоге боковых журналов не хватает файлов: %v", names)
	}
	sid := strings.TrimSuffix(strings.TrimPrefix(meta, "agent-"), ".meta.json")
	if log != "agent-"+sid+".jsonl" {
		t.Fatalf("журнал %q не пара мете %q", log, meta)
	}

	data, err := os.ReadFile(filepath.Join(dir, meta))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Agent  string `json:"agentType"`
		About  string `json:"description"`
		ToolID string `json:"toolUseId"`
		Ended  string `json:"ended"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("мета не разбирается (%v): %s", err, data)
	}
	if m.ToolID != sid {
		t.Fatalf("вызов в мете %q, а журнал зовут %q", m.ToolID, sid)
	}
	if m.Agent != "exec-low" {
		t.Fatalf("определение агента в мете %q, жду exec-low", m.Agent)
	}
	for _, want := range []string{"T-001", "исполнитель", "sidecli"} {
		if !strings.Contains(m.About, want) {
			t.Fatalf("в подписи работы %q нет %q", m.About, want)
		}
	}
	if m.Ended == "" {
		t.Fatalf("вернувшаяся работа не закрыта: %s", data)
	}

	link, err := os.Readlink(filepath.Join(dir, log))
	if err != nil {
		t.Fatalf("журнал не ссылка на транскрипт подпроцесса: %v", err)
	}
	if want := filepath.Join(glm, "projects", "-w", sid+".jsonl"); link != want {
		t.Fatalf("ссылка ведёт в %q, жду %q", link, want)
	}
	if _, err := os.ReadFile(filepath.Join(dir, log)); err != nil {
		t.Fatalf("по ссылке ничего не читается: %v", err)
	}

	parent, err := os.ReadFile(filepath.Join(work, "parent.txt"))
	if err != nil || string(parent) != "1e1e1e1e-2222-4333-8444-555555555555" {
		t.Fatalf("подпроцессу не назвали раздавший разговор: %q (%v)", parent, err)
	}
}

// TestRunCLISideLogNoSessionMark: профиль, чей клиент имя сессии не принимает,
// журналом не обрастает, и молчать про это run не вправе: снаружи пустая лента
// неотличима от работы, которая ещё не написала ни строчки.
func TestRunCLISideLogNoSessionMark(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	transcript := parentTranscript(t, kit, "2e2e2e2e-2222-4333-8444-555555555555")

	code, out := runOut(t, root, "T-001", roleExec, realPath(t, t.TempDir()))
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	if !strings.Contains(out, "ход работы в ленте не покажется") || !strings.Contains(out, "{session}") {
		t.Fatalf("про несобранный журнал не сказано:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")); err == nil {
		t.Fatalf("боковой журнал заведён там, где имени сессии взять неоткуда")
	}
}

// TestRunCLISideLogNoParent: разговор, из которого подняли run, может не
// найтись вовсе (запуск из скрипта, не из сессии). Работа тогда идёт как шла,
// а причина пустой ленты называется словами.
func TestRunCLISideLogNoParent(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "sidecli", sideProfile)
	writeMachine(t, kit, strings.Replace(sideMachine, "%s", filepath.Join(kit, "glm"), 1))
	root := writeBoard(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	code, out := runOut(t, root, "T-001", roleExec, realPath(t, t.TempDir()))
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"ход работы в ленте не покажется", "транскрипта раздавшего разговора не нашлось"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
}

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/peers"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// stageNow это «сейчас» тестов строки этапа: одно на файл, чтобы возраст
// считался в голове, а не пересчитывался в каждом месте.
var stageNow = time.Date(2026, 9, 10, 15, 30, 0, 0, time.Local)

// deadPID это pid только что кончившегося процесса: запись реестра с ним
// подделывает клиента, упавшего посреди хода.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// writePeer кладёт запись реестра клиента в дом home.
func writePeer(t *testing.T, home, sid string, pid int, touched time.Time) {
	t.Helper()
	if err := os.MkdirAll(peers.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(peers.Peer{PID: pid, SessionID: sid, Status: "busy", Updated: touched.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peers.Dir(home), sid+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// openAs открывает этап от имени сессии sid: запись берёт разговор из
// окружения, как в живом конвейере.
func openAs(t *testing.T, home, root, id, kind, sid string, at time.Time) {
	t.Helper()
	t.Setenv(sessions.SessionEnv, sid)
	if err := stage.Open(home, root, id, kind, "", at); err != nil {
		t.Fatal(err)
	}
}

// stageBoard собирает доску с четырьмя записями этапов на четыре случая:
// живая сессия со вторым кругом ревью, ожидание человека, мёртвый процесс и
// живая сессия, молчащая дольше рубежа.
func stageBoard(t *testing.T) (root, home string) {
	t.Helper()
	root = checkBoardSetup(t)
	home = t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	t.Cleanup(func() { timeNow = old })
	timeNow = func() time.Time { return stageNow }
	main := stage.MainRoot(root)

	writePeer(t, home, "s-live", os.Getpid(), stageNow.Add(-time.Minute))
	writePeer(t, home, "s-dead", deadPID(t), stageNow.Add(-time.Minute))
	writePeer(t, home, "s-quiet", os.Getpid(), stageNow.Add(-25*time.Minute))

	openAs(t, home, main, "XR-020", stage.Dev, "s-live", stageNow.Add(-5*time.Hour))
	openAs(t, home, main, "XR-020", stage.Review, "s-live", stageNow.Add(-40*time.Minute))
	openAs(t, home, main, "XR-020", stage.Review, "s-live", stageNow.Add(-12*time.Minute))
	openAs(t, home, main, "XR-010", stage.WaitHuman, "s-dead", stageNow.Add(-3*time.Hour))
	openAs(t, home, main, "XR-011", stage.Dev, "s-dead", stageNow.Add(-49*time.Hour))
	openAs(t, home, main, "XR-012", stage.Dev, "s-quiet", stageNow.Add(-50*time.Minute))
	return root, home
}

func TestListPrintsStageLineInEverySection(t *testing.T) {
	root, _ := stageBoard(t)
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| XR-020 | В работе",
		"  этап: ревью, круг 2, 12 минут, сессия жива",
		"| XR-010 | Со сценарием агента и выкатом",
		"  этап: ждёт человека, 3 часа",
		"| XR-011 | Пользовательская проверка без выката [приёмка: user]",
		"  этап: разработка, 2 дня, сессии нет, брошена",
		"| XR-012 | Без файла задачи",
		"  этап: разработка, 50 минут, сессия молчит 25 минут",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
	// Ожидание живой сессии не требует, и слов о ней под ним нет.
	if strings.Contains(out, "ждёт человека, 3 часа, сесси") {
		t.Fatalf("у ожидания появился признак сессии:\n%s", out)
	}
	// Строка этапа идёт второй под строкой доски, после пометок.
	if got := lineAfter(t, out, "  код слит, вид agent, без отметки smoke"); got != "  этап: ждёт человека, 3 часа" {
		t.Fatalf("после пометок Check стоит %q, жду строку этапа", got)
	}
}

func TestShowPrintsStageLine(t *testing.T) {
	root, _ := stageBoard(t)
	out, err := cmdShow(root, "XR-011")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n  этап: разработка, 2 дня, сессии нет, брошена\n") {
		t.Fatalf("show без строки этапа:\n%s", out)
	}
}

func TestListJSONCarriesStageFields(t *testing.T) {
	root, _ := stageBoard(t)
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sections []struct {
			Rows []map[string]any `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, sec := range doc.Sections {
		for _, row := range sec.Rows {
			rows[row["id"].(string)] = row
		}
	}
	live := rows["XR-020"]
	if live["stage"] != "ревью" || live["stage_round"] != float64(2) || live["stage_age"] != "12 минут" || live["stage_session"] != "сессия жива" {
		t.Fatalf("поля этапа живой строки: %v", live)
	}
	if since, _ := live["stage_since"].(float64); int64(since) != stageNow.Add(-12*time.Minute).Unix() {
		t.Fatalf("начало этапа %v, жду %d", live["stage_since"], stageNow.Add(-12*time.Minute).Unix())
	}
	if _, has := rows["XR-010"]["stage_session"]; has || rows["XR-010"]["stage"] != "ждёт человека" {
		t.Fatalf("ожидание человека: %v", rows["XR-010"])
	}
	// Ожидание без этапа работы перед собой (запись открыта сразу им) не
	// придумывает, где задача встала: stage_at пуст, а не название ожидания.
	if _, has := rows["XR-010"]["stage_at"]; has {
		t.Fatalf("у ожидания без этапа работы перед собой появился stage_at: %v", rows["XR-010"])
	}
	if rows["XR-011"]["stage_session"] != "сессии нет, брошена" || rows["XR-012"]["stage_session"] != "сессия молчит 25 минут" {
		t.Fatalf("признаки сессии: %v / %v", rows["XR-011"]["stage_session"], rows["XR-012"]["stage_session"])
	}
	// Строка этапа в пометки не дублируется: дашборд печатает пометки как есть,
	// а этап рисует своим чипом.
	for _, n := range toStrings(live["notes"]) {
		if strings.HasPrefix(n, "этап:") {
			t.Fatalf("строка этапа попала в notes: %v", live["notes"])
		}
	}
}

// TestListJSONStageAtMarksParkedWork: лента строки списка и шапка формы
// (DK-1119) подсвечивают деление того этапа, на котором задача встала, а не
// собственное деление ожидания, которого у ожиданий в словаре нет вовсе
// (решение исполнителя по развилке «поле на этапе ожидания», docs/tasks/DK-1119.md).
func TestListJSONStageAtMarksParkedWork(t *testing.T) {
	root := checkBoardSetup(t)
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	t.Cleanup(func() { timeNow = old })
	timeNow = func() time.Time { return stageNow }
	main := stage.MainRoot(root)
	openAs(t, home, main, "XR-020", stage.Verify, "", stageNow.Add(-time.Hour))
	openAs(t, home, main, "XR-020", stage.WaitHuman, "", stageNow.Add(-30*time.Minute))

	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sections []struct {
			Rows []map[string]any `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, sec := range doc.Sections {
		for _, r := range sec.Rows {
			if r["id"] == "XR-020" {
				row = r
			}
		}
	}
	if row == nil {
		t.Fatal("строка XR-020 не нашлась")
	}
	if row["stage"] != "ждёт человека" || row["stage_at"] != "проверка" {
		t.Fatalf("этап ожидания после проверки: %v", row)
	}
	if _, has := row["stage_session"]; has {
		t.Fatalf("у ожидания появилась сессия: %v", row)
	}
}

func toStrings(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, x := range list {
			out = append(out, x.(string))
		}
	}
	return out
}

// TestStageSessionsFromRegistry: запись этапа без сессии (открыт вне харнеса),
// а живость считается по всем рабочим сессиям строки из реестра чатов. Привязка
// рукой работой не считается, и по ней строка остаётся брошенной.
func TestStageSessionsFromRegistry(t *testing.T) {
	root := checkBoardSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return stageNow }
	main := stage.MainRoot(root)
	writePeer(t, home, "s-work", os.Getpid(), stageNow.Add(-time.Minute))
	writePeer(t, home, "s-hand", os.Getpid(), stageNow.Add(-time.Minute))
	openAs(t, home, main, "XR-020", stage.Dev, "", stageNow.Add(-90*time.Minute))
	openAs(t, home, main, "XR-010", stage.Dev, "", stageNow.Add(-90*time.Minute))
	lines := sessions.Line(stageNow.Add(-time.Hour), "s-work", sessions.Bind{Task: "XR-020", Source: sessions.BySrc}, "move") +
		sessions.Line(stageNow.Add(-time.Hour), "s-hand", sessions.Bind{Task: "XR-010", Source: sessions.ByHand}, "bind")
	if err := os.WriteFile(sessions.Path(home), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  этап: разработка, 1 час, сессия жива",
		"  этап: разработка, 1 час, сессии нет, брошена",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
}

func TestStageLineSilentWithoutRecords(t *testing.T) {
	root := checkBoardSetup(t)
	t.Setenv("HOME", t.TempDir())
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "этап:") {
		t.Fatalf("без записей строка этапа не печатается:\n%s", out)
	}
}

func TestAgeSinceWords(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:    "меньше минуты",
		time.Minute:         "1 минута",
		12 * time.Minute:    "12 минут",
		time.Hour:           "1 час",
		5 * time.Hour:       "5 часов",
		47 * time.Hour:      "47 часов",
		48 * time.Hour:      "2 дня",
		30 * 24 * time.Hour: "30 дней",
	}
	for d, want := range cases {
		if got := ageSince(stageNow.Add(-d), stageNow); got != want {
			t.Errorf("ageSince(%v) = %q, жду %q", d, got, want)
		}
	}
	if got := ageSince(stageNow.Add(time.Hour), stageNow); got != "меньше минуты" {
		t.Errorf("этап из будущего: %q", got)
	}
}

func TestStageRoundCountsSameKind(t *testing.T) {
	rec := stage.Record{Stages: []stage.Stage{{Kind: stage.Dev}, {Kind: stage.Review}, {Kind: stage.Dev}, {Kind: stage.Review}}}
	if got := stageRound(rec); got != 2 {
		t.Fatalf("круг %d, жду 2", got)
	}
	if got := stageRound(stage.Record{}); got != 0 {
		t.Fatalf("круг пустой записи %d", got)
	}
}

// TestWaitStagesHaveNoSessionTail: ожидания по словарю живой сессии не
// требуют (stage.NeedsSession), и запись от мёртвой сессии печатается без
// хвоста, а не брошенной. Слова прежнего словаря «уточнение» и «снаружи» из
// старой записи читаются ожиданиями (stage.Canon) и печатаются нынешними
// словами.
func TestWaitStagesHaveNoSessionTail(t *testing.T) {
	root := checkBoardSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return stageNow }
	main := stage.MainRoot(root)
	writePeer(t, home, "s-dead", deadPID(t), stageNow.Add(-time.Minute))
	openAs(t, home, main, "XR-020", stage.WaitEvent, "s-dead", stageNow.Add(-5*time.Hour))
	openAs(t, home, main, "XR-011", stage.WaitQueue, "s-dead", stageNow.Add(-5*time.Hour))
	legacy := "id = XR-010\nroot = " + main + "\n" +
		"этап = уточнение | " + stageNow.Add(-5*time.Hour).Format(stage.Stamp) + " | вопрос человеку | s-dead\n"
	if err := os.WriteFile(stage.Path(home, main, "XR-010"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  этап: ждёт события, 5 часов\n",
		"  этап: ждёт очереди, 5 часов\n",
		"  этап: ждёт человека, 5 часов\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "брошена") || strings.Contains(out, "уточнение") {
		t.Fatalf("ожидание названо брошенным или старым словом:\n%s", out)
	}
}

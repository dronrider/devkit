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
	writePeerIn(t, peers.Dir(home), sid, pid, touched)
}

// writePeerIn кладёт запись в указанный каталог реестра: у каталога подписки
// из CLAUDE_CONFIG_DIR путь к дому не относится.
func writePeerIn(t *testing.T, dir, sid string, pid int, touched time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(peers.Peer{PID: pid, SessionID: sid, Status: "busy", Updated: touched.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".json"), data, 0o644); err != nil {
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
	out, err := cmdList(root, "", false)
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

// TestShowReadsSessionFromConfigDir: клиент пишет реестр в каталог подписки
// из CLAUDE_CONFIG_DIR, а старый ~/.claude/sessions оставляет пустым. Строка
// такой сессии говорит «сессия жива», а не «сессии нет, брошена» (DK-1335).
func TestShowReadsSessionFromConfigDir(t *testing.T) {
	root := checkBoardSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	old := timeNow
	t.Cleanup(func() { timeNow = old })
	timeNow = func() time.Time { return stageNow }
	main := stage.MainRoot(root)

	writePeerIn(t, filepath.Join(cfg, "sessions"), "s-live", os.Getpid(), stageNow.Add(-time.Minute))
	openAs(t, home, main, "XR-020", stage.Dev, "s-live", stageNow.Add(-time.Hour))

	out, err := cmdShow(root, "XR-020")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "сессия жива") {
		t.Fatalf("запись из каталога подписки не дочитана, в show нет живой сессии:\n%s", out)
	}
	if strings.Contains(out, "сессии нет, брошена") {
		t.Fatalf("пустой старый каталог выдан за брошенную сессию:\n%s", out)
	}
}

func TestListJSONCarriesStageFields(t *testing.T) {
	root, _ := stageBoard(t)
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "", false)
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

	out, err := cmdListJSON(root, "", false)
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
	out, err := cmdList(root, "", false)
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
	out, err := cmdList(root, "", false)
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
	if got := stageRound(rec, stage.Dev); got != 2 {
		t.Fatalf("круг %d, жду 2", got)
	}
	if got := stageRound(stage.Record{}, stage.Dev); got != 0 {
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
	out, err := cmdList(root, "", false)
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

// TestClosedWaitUncoversWorkInList: предмет DK-1193. Ожидание ложится поверх
// незакрытой работы, и пока у записи ожидания не было конца, строка доски
// говорила «ждёт события» шесть часов идущего ревью (DK-920). Конец ожидания
// называет оболочка конвейера (`agentctl stage <ID> --done`), и живым этапом
// строки снова становится ревью под ним. У настоящего ожидания, которое ещё
// идёт, слово ожидания остаётся.
func TestClosedWaitUncoversWorkInList(t *testing.T) {
	root, home := stageBoard(t)
	main := stage.MainRoot(root)
	openAs(t, home, main, "XR-020", stage.WaitEvent, "s-live", stageNow.Add(-6*time.Minute))

	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  этап: ждёт события, 6 минут") {
		t.Fatalf("идущее ожидание не названо:\n%s", out)
	}

	closed, err := stage.Close(home, main, "XR-020", stage.WaitEvent, stageNow.Add(-time.Minute), "ожидание кончилось: событие")
	if err != nil || !closed {
		t.Fatalf("ожидание не закрылось: %v, %v", closed, err)
	}
	out, err = cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ждёт события") {
		t.Fatalf("закрытое ожидание осталось словом строки:\n%s", out)
	}
	if !strings.Contains(out, "  этап: ревью, круг 2, 12 минут, сессия жива") {
		t.Fatalf("под закрытым ожиданием не открылось ревью:\n%s", out)
	}
	// У соседки ожидание идёт, и её слово не трогается.
	if !strings.Contains(out, "  этап: ждёт человека, 3 часа") {
		t.Fatalf("настоящее ожидание соседки пропало:\n%s", out)
	}
}

// closeAs закрывает этап kind задачи id на момент at, как это делает хук по
// концу субагента или shipctl по концу слияния.
func closeAs(t *testing.T, home, root, id, kind string, at time.Time) {
	t.Helper()
	closed, err := stage.Close(home, root, id, kind, at, "")
	if err != nil || !closed {
		t.Fatalf("этап %s у %s не закрылся: %v, %v", kind, id, closed, err)
	}
}

// TestListPrintsLastClosedStage: запись, в которой закрыты все этапы, это
// задача, которой агент касался и которую бросил конвейер (DK-1205). Под
// строкой стоит последний закрытый этап со словом «закрыт», возрастом от
// конца и головой задачи: у XR-011 сессия мертва, головы нет, у XR-020 сессия
// s-live жива, и голова жива.
func TestListPrintsLastClosedStage(t *testing.T) {
	root, home := stageBoard(t)
	main := stage.MainRoot(root)
	closeAs(t, home, main, "XR-011", stage.Dev, stageNow.Add(-3*time.Hour))
	closeAs(t, home, main, "XR-020", stage.Review, stageNow.Add(-5*time.Minute))
	closeAs(t, home, main, "XR-020", stage.Review, stageNow.Add(-4*time.Minute))
	closeAs(t, home, main, "XR-020", stage.Dev, stageNow.Add(-2*time.Minute))

	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  этап: разработка, закрыт 3 часа назад, головы нет",
		// Последний это кончившийся позже прочих, а не записанный последним:
		// разработка XR-020 закрылась после обоих ревью.
		"  этап: разработка, закрыт 2 минуты назад, голова жива",
		// Соседки с открытым этапом печатаются как прежде.
		"  этап: ждёт человека, 3 часа",
		"  этап: разработка, 50 минут, сессия молчит 25 минут",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "сессии нет, брошена") {
		t.Fatalf("закрытый этап назван брошенным:\n%s", out)
	}

	show, err := cmdShow(root, "XR-011")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "\n  этап: разработка, закрыт 3 часа назад, головы нет\n") {
		t.Fatalf("show без закрытого этапа:\n%s", show)
	}
}

// TestListJSONCarriesClosedStageState: состояние этапа едет отдельным полем,
// а дашборд рисует закрытый этап по нему, без своего расчёта. У закрытого есть
// конец и голова, а хвоста о сессии нет; у открытого состояние «открыт».
func TestListJSONCarriesClosedStageState(t *testing.T) {
	root, home := stageBoard(t)
	main := stage.MainRoot(root)
	end := stageNow.Add(-3 * time.Hour)
	closeAs(t, home, main, "XR-011", stage.Dev, end)
	rows := listJSONRows(t, root)
	closed := rows["XR-011"]
	if closed["stage"] != "разработка" || closed["stage_state"] != "закрыт" || closed["stage_age"] != "3 часа" || closed["stage_head"] != "головы нет" {
		t.Fatalf("поля закрытого этапа: %v", closed)
	}
	if got, _ := closed["stage_end"].(float64); int64(got) != end.Unix() {
		t.Fatalf("конец этапа %v, жду %d", closed["stage_end"], end.Unix())
	}
	if got, _ := closed["stage_since"].(float64); int64(got) != stageNow.Add(-49*time.Hour).Unix() {
		t.Fatalf("начало закрытого этапа %v", closed["stage_since"])
	}
	if _, has := closed["stage_session"]; has {
		t.Fatalf("у закрытого этапа хвост о сессии: %v", closed)
	}
	if live := rows["XR-020"]; live["stage_state"] != "открыт" || live["stage_end"] != nil || live["stage_head"] != nil {
		t.Fatalf("поля открытого этапа: %v", live)
	}
}

// listJSONRows отдаёт строки list --json картой по ID.
func listJSONRows(t *testing.T, root string) map[string]map[string]any {
	t.Helper()
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "", false)
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
	return rows
}

// TestListReadsLastStageFromProgressSection: без записи в runs последний этап
// берётся из строк «Хода работы» файла задачи в main (решение исполнителя по
// развилке «источник», docs/tasks/DK-1205.md). Последним считается
// кончившийся позже прочих, а этап нулевой длины кончается там же, где
// начался. Задача с файлом без строк этапов и задача без файла остаются без
// строки этапа: их агент не касался. Постановка черновика это тоже касание
// (развилка «постановка»).
func TestListReadsLastStageFromProgressSection(t *testing.T) {
	root, _ := stageBoard(t)
	day := stageNow.Format("2006-01-02")
	progress := "# XR-010\n\n## Выкат\n\n- 2026-01-01 слито: 1234567\n\n## Сценарий проверки (агентский)\n\nшаги\n\n## Ход работы\n\n" +
		"- Постановка: разбор черновика, " + day + " 09:00-09:10.\n" +
		"- Разработка: субагент opus/high по определению exec-high, работа a1, " + day + " 10:00-13:00.\n" +
		"- Ревью: субагент opus/high по определению review-high, работа a2, " + day + " 12:00-12:30.\n" +
		"- Слияние: shipctl merge, " + day + " 12:40.\n"
	if err := os.WriteFile(taskFilePath(root, "XR-010"), []byte(progress), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stage.Path(stage.Home(), stage.MainRoot(root), "XR-010")); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  этап: разработка, закрыт 2 часа назад, головы нет") {
		t.Fatalf("последний этап из «Хода работы» не найден:\n%s", out)
	}
	if strings.Contains(out, "этап: слияние") || strings.Contains(out, "ждёт человека") {
		t.Fatalf("под строкой не последний этап «Хода работы»:\n%s", out)
	}
	rows := listJSONRows(t, root)
	if got := rows["XR-010"]; got["stage_state"] != "закрыт" || got["stage_head"] != "головы нет" {
		t.Fatalf("поля этапа из «Хода работы»: %v", got)
	}
	// Файл задачи без строк этапов: сценарий проверки есть, касания агента нет.
	if _, has := rows["XR-011"]["stage"]; has && rows["XR-011"]["stage_state"] == "закрыт" {
		t.Fatalf("у строки без этапов в файле появился закрытый этап: %v", rows["XR-011"])
	}
	// Постановка черновика в «Ходе работы» тоже касание.
	setup := "# XR-011\n\n## Сценарий проверки\n\nшаги\n\n## Ход работы\n\n- Постановка: разбор черновика, " + day + " 09:00-09:10.\n"
	if err := os.WriteFile(taskFilePath(root, "XR-011"), []byte(setup), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stage.Path(stage.Home(), stage.MainRoot(root), "XR-011")); err != nil {
		t.Fatal(err)
	}
	out, err = cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  этап: постановка, закрыт 6 часов назад, головы нет") {
		t.Fatalf("постановка из «Хода работы» не считается касанием:\n%s", out)
	}
}

// TestLastEndedPrefersLatestEnd: синхронная вычитка записана поверх идущей
// разработки последней, а кончилась раньше неё, и последним закрытым стоит
// разработка. При равных концах побеждает записанный позже. Закрытое
// ожидание в счёт не идёт, и запись из одних закрытых ожиданий строки не
// даёт (DK-1193: снятая парковка словом строки не остаётся).
func TestLastEndedPrefersLatestEnd(t *testing.T) {
	rec := stage.Record{Stages: []stage.Stage{
		{Kind: stage.Dev, Start: stageNow.Add(-time.Hour), End: stageNow},
		{Kind: stage.Proof, Start: stageNow.Add(-30 * time.Minute), End: stageNow.Add(-20 * time.Minute)},
	}}
	if last, ok := lastEnded(rec); !ok || last.Kind != stage.Dev {
		t.Fatalf("последний закрытый %v, жду разработку", last)
	}
	rec = stage.Record{Stages: []stage.Stage{
		{Kind: stage.Merge, Start: stageNow, End: stageNow},
		{Kind: stage.Merge, Start: stageNow, End: stageNow},
		{Kind: stage.Review, Start: stageNow.Add(-time.Hour)},
	}}
	if last, ok := lastEnded(rec); !ok || last.Kind != stage.Merge {
		t.Fatalf("при равных концах взят %v", last)
	}
	if _, ok := lastEnded(stage.Record{Stages: []stage.Stage{{Kind: stage.Dev, Start: stageNow}}}); ok {
		t.Fatal("у записи без закрытых этапов нашёлся последний закрытый")
	}
	rec = stage.Record{Stages: []stage.Stage{
		{Kind: stage.Review, Start: stageNow.Add(-2 * time.Hour), End: stageNow.Add(-time.Hour)},
		{Kind: stage.WaitHuman, Start: stageNow.Add(-time.Hour), End: stageNow},
	}}
	if last, ok := lastEnded(rec); !ok || last.Kind != stage.Review {
		t.Fatalf("закрытое ожидание перекрыло этап работы: %v", last)
	}
	if _, ok := lastEnded(stage.Record{Stages: []stage.Stage{{Kind: stage.WaitEvent, Start: stageNow.Add(-time.Hour), End: stageNow}}}); ok {
		t.Fatal("запись из одного закрытого ожидания дала строку")
	}
}

func TestHeadWords(t *testing.T) {
	cases := map[peers.Life]string{
		{State: peers.Alive}:                             "голова жива",
		{State: peers.Silent, Silence: 25 * time.Minute}: "голова молчит 25 минут",
		{State: peers.Silent, Silence: 46 * time.Hour}:   "голова молчит 46 часов",
		{State: peers.Silent}:                            "голова молчит, касания не записано",
		{State: peers.Gone}:                              "головы нет",
	}
	for life, want := range cases {
		if got := headWords(life, stageNow); got != want {
			t.Errorf("%+v: %q, жду %q", life, got, want)
		}
	}
}

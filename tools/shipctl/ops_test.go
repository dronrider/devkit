package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

const boardTable = "| ID | Задача | Тип | P | R | Ссылка |\n|--------|--------|-----|---|---|--------|\n"

func section(rows string) string {
	if rows == "" {
		return "Нет.\n"
	}
	return boardTable + rows
}

const rowInProg = "| XR-001 | Починка бага | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"

// setup собирает репозиторий с доской: XR-001 в заданных секциях, стаб
// taskctl в PATH пишет вызовы в лог и имитирует правку доски.
func setup(t *testing.T, inProg, check string) (root, callLog string) {
	t.Helper()
	root = t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	board := "# Тест: задачи (префикс XR)\n\n" +
		"## In progress\n\n" + section(inProg) + "\n" +
		"## Check\n\n" + section(check) + "\n" +
		"## Backlog\n\n" + section("| XR-002 | Задел | task | P3 | 10 (0+5+0+0+5) |  |\n") + "\n" +
		"## Blocked\n\nНет.\n"
	write(t, root, "docs/TASKS.md", board)
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001: починка бага\n\n## Сценарий проверки\n\nАгентский: `git log -1`, ждём коммит правки.\n"+
			fixtureReviewLevel+"\n- гонка в close: исправлено\n- нейминг: отклонено, стиль проекта\n")
	write(t, root, "code.txt", "old\n")
	// .devkit/cmdout это место, куда DK-266 складывает полные выводы провалившихся
	// команд из сводки frame.Summarize: в репозитории проекта каталог гитигнорнут
	// правилами devkit, а тестовый репозиторий заводится без gitignore, и без этой
	// записи untracked файлы вывода смазывали бы статусные проверки в worktree.
	write(t, root, ".gitignore", ".devkit/cmdout/\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed")

	bin := t.TempDir()
	callLog = filepath.Join(bin, "calls.log")
	stub := "#!/bin/sh\necho \"$@\" >> \"" + callLog + "\"\nprintf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n"
	write(t, bin, "taskctl", stub)
	if err := os.Chmod(filepath.Join(bin, "taskctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Подъём прогона сценария после выката (DK-718) зовёт дашборд, и фикстура
	// стоит в стенде с самого начала: без неё автономный выкат дотянулся бы до
	// живого бинаря машины, а тот пошёл бы читать настоящий конфиг и живые
	// tmux-сессии.
	write(t, bin, checkRunBin, "#!/bin/sh\necho \"dashboard $@\" >> \""+callLog+
		"\"\necho 'прогон сценария поднят в tmux-сессии task-XR-001'\n")
	if err := os.Chmod(filepath.Join(bin, checkRunBin), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, callLog
}

// fixtureReviewLevel это раздел «Ревью» со строкой уровня: без неё ворот следа
// ревью отбивает merge, а предмет почти всех тестов тут не ревью. Замечания в
// фикстурах дописываются следом за строкой уровня, как их пишет taskctl.
const fixtureReviewLevel = "\n## Ревью\n\nУровень 1 до 1a2b3c4: рутина, тронут один файл.\n"

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// addRemote заводит bare-репозиторий как origin с отслеживанием main, чтобы
// в тесте отработал `git push` без аргументов. Возвращает путь bare: по нему
// проверяется история origin и имитируется работа с другой машины.
func addRemote(t *testing.T, root string) string {
	t.Helper()
	bare := t.TempDir()
	gitT(t, bare, "init", "-q", "--bare", "-b", "main")
	gitT(t, root, "remote", "add", "origin", bare)
	gitT(t, root, "push", "-qu", "origin", "main")
	return bare
}

func branchWithFix(t *testing.T, root string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "new\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
}

func TestMergeHappyPath(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "предупреждение") {
		t.Errorf("лишнее предупреждение: %q", msg)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("после merge стоим на %q", br)
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.HasPrefix(log, "docs(tasks): XR-001 в Check") || !strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("история после merge:\n%s", log)
	}
	if gitT(t, root, "branch", "--list", "xr-001-fix") != "" {
		t.Error("фичеветка не удалена")
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Errorf("taskctl move не вызван: %q", calls)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "new\n" {
		t.Fatalf("правка не доехала до main: %q", got)
	}
}

// TestMergeDeployFromConfig: без --deploy команда выката берётся из
// .devkit/deploy.local (гитигнорнут, поэтому пишется после ветки, untracked
// и preflight его не считает грязью), катится только при autonomous=true.
func TestMergeDeployFromConfig(t *testing.T) {
	deployed := func(root string) bool {
		_, err := os.Stat(filepath.Join(root, "deployed.marker"))
		return err == nil
	}

	// autonomous=true: shipctl выкатывает сам и пушит сам, без --push. Команда
	// выката сверяет origin/main с main: маркер появится, только если код уже
	// запушен на момент деплоя (пуш кода идёт до выката, сервер тянет из origin).
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	branchWithFix(t, root)
	write(t, root, ".devkit/deploy.local",
		"deploy = test \"$(git rev-parse origin/main)\" = \"$(git rev-parse main)\" && touch deployed.marker\nautonomous = true\n")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !deployed(root) || !strings.Contains(msg, "выкат прошёл") {
		t.Fatalf("автономный выкат до пуша кода или не отработал: %q", msg)
	}
	if !strings.Contains(msg, "код запушен") || !strings.Contains(msg, "доска запушена") {
		t.Fatalf("автономный merge должен пушить код и доску сам: %q", msg)
	}
	if rl := gitT(t, bare, "log", "main", "--format=%s"); !strings.Contains(rl, "fix: XR-001 правка") || !strings.Contains(rl, "docs(tasks): XR-001 в Check") {
		t.Fatalf("автопуш не уехал в origin:\n%s", rl)
	}

	// autonomous=false: команда есть, но катит пользователь, не shipctl.
	root, _ = setup(t, rowInProg, "")
	branchWithFix(t, root)
	write(t, root, ".devkit/deploy.local", "deploy = touch deployed.marker\nautonomous = false\n")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if deployed(root) || !strings.Contains(msg, "за пользователем") {
		t.Fatalf("при autonomous=false shipctl катить не должен: %q", msg)
	}

	// Явный --deploy сильнее конфига с autonomous=false.
	root, _ = setup(t, rowInProg, "")
	branchWithFix(t, root)
	write(t, root, ".devkit/deploy.local", "deploy = touch config.marker\nautonomous = false\n")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "touch deployed.marker"}); err != nil {
		t.Fatal(err)
	}
	if !deployed(root) {
		t.Fatal("явный --deploy не выполнен")
	}
	if _, err := os.Stat(filepath.Join(root, "config.marker")); err == nil {
		t.Fatal("при явном --deploy команда конфига не должна запускаться")
	}
}

// TestMergeStaleMain: отставший от origin клон отсекается до слияния. Доска
// на origin уехала вперёд (другая машина поставила задачу в Check), и merge
// на такой копии обязан упасть, не тронув ни main, ни фичеветку.
func TestMergeStaleMain(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	branchWithFix(t, root)
	tmp := t.TempDir()
	gitT(t, tmp, "clone", "-q", bare, "other")
	other := filepath.Join(tmp, "other")
	gitT(t, other, "config", "user.email", "test@test")
	gitT(t, other, "config", "user.name", "test")
	write(t, other, "docs/TASKS.md", "<!-- другая машина двинула доску -->\n")
	gitT(t, other, "add", ".")
	gitT(t, other, "commit", "-qm", "docs(tasks): XR-009 в Check")
	gitT(t, other, "push", "-q")

	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "отстал") {
		t.Fatalf("ждал отказ на отставшем main: %v", err)
	}
	if gitT(t, root, "branch", "--list", "xr-001-fix") == "" {
		t.Fatal("фичеветка тронута, отказ должен идти до слияния")
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-001-fix" {
		t.Fatalf("после отказа стоим на %q", br)
	}
}

// codeCommit кладёт на текущую ветку коммит кода с ID задачи в subject:
// такая задача в Check считается выкаченной и держит очередь.
func codeCommit(t *testing.T, root, id, file string) {
	t.Helper()
	write(t, root, file, id+"\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: "+id+" фича")
}

// TestMergeQueueBusy: занятая очередь держит прод, а не main, поэтому
// одиночный merge не отказывает, а переводит себя в поездной режим (DK-306,
// решение 3): ветка льётся в main, задача остаётся в In progress и ждёт
// свободной очереди в поезде. Сессии исполнителя к этому моменту обычно уже
// нет, и получателя у события освобождения очереди не будет.
func TestMergeQueueBusy(t *testing.T) {
	root, callLog := setup(t, rowInProg, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("занятая очередь держит выкат, а не слияние: %v", err)
	}
	if !strings.Contains(msg, "очередь занята: XR-009") ||
		!strings.Contains(msg, "слияние поездное") {
		t.Fatalf("отчёт обязан объяснить отказ от выката: %q", msg)
	}
	if !strings.Contains(msg, "в поезде: XR-001") {
		t.Fatalf("задача должна попасть в поезд: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move") {
		t.Fatalf("занятая очередь не двигает доску: %q", calls)
	}
	// Выкат остаётся запертым, пока XR-009 не прошла Check.
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err == nil ||
		!strings.Contains(err.Error(), "очередь занята: XR-009") {
		t.Fatalf("ship при занятой очереди должен отбиваться: %v", err)
	}

	// Боевой путь с поездами: код задачи под тегом deployed (уехал через
	// ship), очередь ищет по всему логу тега, а не по окну поезда.
	root, _ = setup(t, rowInProg, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	gitT(t, root, "tag", "deployed")
	branchWithFix(t, root)
	msg, err = cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("с тегом очередь тоже держит выкат, а не слияние: %v", err)
	}
	if !strings.Contains(msg, "очередь занята: XR-009") ||
		!strings.Contains(msg, "слияние поездное") {
		t.Fatalf("с тегом отчёт тоже обязан объяснить отказ от выката: %q", msg)
	}
	if !strings.Contains(msg, "в поезде: XR-001") {
		t.Fatalf("с тегом задача должна попасть в поезд: %q", msg)
	}
}

// TestMergeQueueBusyFailed: форвард-фикс проваленной задачи при занятой
// очереди в поезд не уходит. Починка провала едет выкатом, выкат очередь не
// проходит, а поезд при сломанном проде не копится: молчаливое слияние
// запирало бы починку навсегда.
func TestMergeQueueBusyFailed(t *testing.T) {
	root, _ := setup(t, rowSelfFailed, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	gitT(t, root, "tag", "deployed")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "поезд при сломанном проде не копится") {
		t.Fatalf("форвард-фикс при занятой очереди должен получать отказ с путём починки: %v", err)
	}
	if !strings.Contains(err.Error(), "очередь занята: XR-009") {
		t.Fatalf("отказ обязан назвать занявшую очередь задачу: %v", err)
	}
}

const rowCheck = "| XR-009 | Ждёт проверки | task | P2 | 30 (25+5+0+0+0) |  |\n"
const rowLLD = "| XR-005 | LLD поезда | LLD | P2 | 30 (25+5+0+0+0) | [lld/train.md](lld/train.md) |\n"

// lldCommit имитирует сделанную LLD-задачу: правка только под docs/, на прод
// не едет.
func lldCommit(t *testing.T, root, id string) {
	t.Helper()
	write(t, root, "docs/lld/train.md", "# LLD\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: "+id+" LLD поезда")
}

// TestQueueDocsOnly: задача в Check без выкаченного кода (LLD, дока ждут
// подтверждения пользователя) очередь выката не держит: merge и ship
// проходят, инвариант про непроверенный выкат, а не про секцию доски.
func TestQueueDocsOnly(t *testing.T) {
	// Одиночный merge при LLD в Check.
	root, _ := setup(t, rowInProg, rowLLD)
	lldCommit(t, root, "XR-005")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("LLD в Check не должна держать merge: %v", err)
	}

	// Ship поезда при LLD в Check.
	root, callLog := setup(t, rowInProg, rowLLD)
	lldCommit(t, root, "XR-005")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdShip(root, ShipParams{Deploy: "true"})
	if err != nil {
		t.Fatalf("LLD в Check не должна держать ship: %v", err)
	}
	if !strings.Contains(msg, "поезд выкачен (XR-001)") {
		t.Fatalf("состав поезда: %q", msg)
	}
	// Ship переводит в Check только поезд, LLD остаётся ждать как ждала.
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "XR-005") {
		t.Fatalf("ship не должен трогать LLD-задачу: %q", calls)
	}
}

func TestMergeOpenReview(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	f := filepath.Join(root, "docs", "tasks", "XR-001.md")
	data, _ := os.ReadFile(f)
	write(t, root, "docs/tasks/XR-001.md", string(data)+"- хвост без исхода, думаем\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: замечание")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "без исхода") {
		t.Fatalf("ожидал отказ по ревью, получил: %v", err)
	}
}

// Чистый вердикт с маркером списка замечанием не считается, а замечание,
// перенесённое на несколько строк с исходом на переносе, судится целиком:
// открытым остаётся только то, у чего правда нет исхода. Пустая строка между
// маркером и абзацем со словом исхода закрывает элемент, поэтому абзац чужого
// исхода замечание не закрывает. Иначе merge отбивался бы на разметке, а не на
// содержании, и расходился бы с taskctl review show (DK-277).
func TestReviewNotesMarkup(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001: починка бага\n\n"+
			"## Ревью\n\n"+
			"- Вердикт: без замечаний. Путь от симптома пройден по ops.go.\n"+
			"- длинное замечание,\n  перенесённое на две строки: исправлено\n"+
			"- гибрид: исправлено, теперь без замечаний\n"+
			"- ещё одно, без исхода\n"+
			"- открытое с пояснением\n\n"+
			"исправлено в коммите abc\n")
	open, err := openReviewNotes(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("открытыми должны быть два замечания без исхода, получили: %v", open)
	}
	for _, want := range []string{"ещё одно", "открытое с пояснением"} {
		found := false
		for _, o := range open {
			if strings.Contains(o, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("среди открытых нет %q: %v", want, open)
		}
	}
}

// Замечание, цитирующее слово исхода не в хвосте resolve-формата, остаётся
// открытым для ворот слияния (DK-503, DK-514): reviewOutcome ищет исход по
// позиции, где его пишет taskctl review resolve, а не по факту появления
// слова где-то в тексте.
func TestReviewNotesQuotedWord(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001: починка бага\n\n"+
			"## Ревью\n\n"+
			"- замечание 18 цитировало текст сценария со словами «остаются закрытыми исходом «исправлено»»\n")
	open, err := openReviewNotes(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("замечание с цитатой должно остаться открытым, получили: %v", open)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "без исхода") {
		t.Fatalf("merge должен отбиваться замечанием с цитатой, получили: %v", err)
	}
}

// Оборот «без замечаний» внутри сути замечания открытым его оставляет, и
// ворота слияния его видят (DK-469): reviewOutcome ищет чистый вердикт в
// голове элемента, куда его пишет ревьювер, а суть живого замечания 11 из
// DK-460 кончается словами «про вердикт без замечаний нет ничего» и раньше
// слияние пропускала. Вердикт, записанный рукой по той же форме, замечанием
// не считается и merge не держит.
func TestReviewNotesVerdictPhrase(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001: починка бага\n\n"+
			"## Ревью\n\n"+
			"- Строка 1 плана меняет поведение merge раньше, чем процедура учится писать запись, а порядок относительно строки 5 объявлен свободным («5 в любой момент», при том что сама строка 5 обещает увести третий разряд в редкий случай). В составе строки 1 названы RULES.board.md п. 2 и tools/shipctl/README.md, но не скилл board-ship, а именно его читает диспетчер в момент ревью: в разделе «Ревью» пп. 5-6 (SKILL.md:91-95) сказано только «строка на замечание, суть и исход», про вердикт без замечаний нет ничего. В промежутке между строками 1 и 5 отказ merge будет приходить на честной работе, а где брать запись, не написано нигде.\n"+
			"- Вердикт: без замечаний. Путь от симптома пройден по ops.go.\n")
	open, err := openReviewNotes(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("открытым должно быть только замечание с оборотом в сути, получили: %v", open)
	}
	if !strings.Contains(open[0], "Строка 1 плана") {
		t.Errorf("среди открытых нет замечания с оборотом в сути: %v", open)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "без исхода") {
		t.Fatalf("merge должен отбиваться замечанием с оборотом в сути, получили: %v", err)
	}
}

func TestMergeRedTests(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "false"})
	if err == nil || !strings.Contains(err.Error(), "красные") {
		t.Fatalf("ожидал отказ по тестам, получил: %v", err)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-001-fix" {
		t.Fatalf("после красных тестов стоим на %q", br)
	}
	if log := gitT(t, root, "log", "--format=%s", "main"); log != "seed" {
		t.Fatalf("main изменился: %s", log)
	}
}

func TestMergePreconditions(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "сливать нечего") {
		t.Fatalf("merge с main без worktree задачи должен отбиваться: %v", err)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "--test") {
		t.Fatalf("merge без --test должен отбиваться: %v", err)
	}
	if _, err := cmdMerge(root, MergeParams{ID: "XR-404", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("merge неизвестной задачи должен отбиваться: %v", err)
	}
	write(t, root, "code.txt", "dirty\n")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "незакоммиченное") {
		t.Fatalf("merge с грязным деревом должен отбиваться: %v", err)
	}
}

// foreignTaskFile заводит трекнутый файл соседней задачи и коммитит его:
// нужен для чужой правки, не пересекающейся с файлами текущего слияния.
// Untracked git status с --untracked-files=no не видит, поэтому файл должен
// быть закоммичен до того, как его правят мимо коммита.
func foreignTaskFile(t *testing.T, root string) {
	t.Helper()
	write(t, root, "docs/tasks/XR-777.md", "# XR-777: чужая задача\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-777 заведена")
}

// TestMergeIgnoresForeignDirty: несколько сессий по одной доске держат в
// общем чекауте чужое незакоммиченное, и оно не должно отбивать merge, если
// не пересекается с диапазоном слияния (DK-720).
func TestMergeIgnoresForeignDirty(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	foreignTaskFile(t, root)
	branchWithFix(t, root)
	write(t, root, "docs/tasks/XR-777.md", "# XR-777: чужая задача\n\nправка соседней сессии\n")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("чужая правка мимо файлов слияния не должна отбивать merge: %v", err)
	}
	if !strings.Contains(msg, "слита в main fast-forward") {
		t.Fatalf("слияние не прошло: %q", msg)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-777.md")); !strings.Contains(string(got), "правка соседней сессии") {
		t.Fatalf("чужая незакоммиченная правка потерялась: %q", got)
	}
}

// TestMergeRefusedOnOwnFileDirty: незакоммиченное по файлу самой задачи
// по-прежнему отбивает merge, и отказ называет именно этот путь, а не весь
// git status (в дереве рядом лежит чужая правка мимо задачи).
func TestMergeRefusedOnOwnFileDirty(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	foreignTaskFile(t, root)
	branchWithFix(t, root)
	write(t, root, "code.txt", "недокоммиченное\n")
	write(t, root, "docs/tasks/XR-777.md", "# XR-777: чужая задача\n\nправка соседней сессии\n")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "code.txt") {
		t.Fatalf("merge должен отбиться по своему файлу и назвать его: %v", err)
	}
	if strings.Contains(err.Error(), "XR-777") {
		t.Fatalf("отказ не должен упоминать чужую правку мимо задачи: %v", err)
	}
}

func TestStatus(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 Починка бага") || !strings.Contains(msg, "очередь свободна") {
		t.Fatalf("status:\n%s", msg)
	}
	root2, _ := setup(t, "", rowInProg)
	codeCommit(t, root2, "XR-001", "one.txt")
	msg, err = cmdStatus(root2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь занята: XR-001") {
		t.Fatalf("status с занятой очередью:\n%s", msg)
	}
	// LLD в Check ждёт пользователя, но очередь не держит, и status говорит
	// об этом явно, а не просто «свободна».
	root3, _ := setup(t, rowInProg, rowLLD)
	lldCommit(t, root3, "XR-005")
	msg, err = cmdStatus(root3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь свободна") || !strings.Contains(msg, "без выкаченного кода") {
		t.Fatalf("status с LLD в Check:\n%s", msg)
	}
	// Автономия без команды выката: merge всё равно будет пушить сам, и
	// status обязан об этом предупредить, а не выглядеть чисто ручным.
	write(t, root, ".devkit/deploy.local", "autonomous = true\n")
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "пушит сам") {
		t.Fatalf("status молчит про автопуш без команды выката:\n%s", msg)
	}
}

func TestRevert(t *testing.T) {
	root, callLog := setup(t, "", rowInProg) // задача уже в Check
	write(t, root, "code.txt", "broken\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 фича")
	// Коммит только по доске откатываться не должен.
	f, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	write(t, root, "docs/TASKS.md", string(f)+"<!-- маркер -->\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 в Check")

	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "old\n" {
		t.Fatalf("код не откатился: %q", got)
	}
	board, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	if !strings.Contains(string(board), "<!-- маркер -->") {
		t.Fatal("откатился коммит доски")
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.HasPrefix(log, "docs(tasks): XR-001 обратно в In progress") ||
		!strings.Contains(log, "revert: XR-001 откат") {
		t.Fatalf("история после revert:\n%s", log)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 in-progress") {
		t.Errorf("taskctl move не вызван: %q", calls)
	}
	if !strings.Contains(msg, "откачено коммитов: 1") {
		t.Fatalf("сообщение: %q", msg)
	}
	// Повторный запуск не находит что откатывать: свой revert не трогается.
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "откатывать нечего") {
		t.Fatalf("повторный revert должен отбиваться: %v", err)
	}
}

// TestRevertAutonomous: аварийный путь в автономном режиме симметричен merge.
// Revert пушит откат до повторного выката (команда выката сверяет origin/main
// с main, маркер появится только на свежем origin) и пушит доску следом.
func TestRevertAutonomous(t *testing.T) {
	root, _ := setup(t, "", rowInProg) // задача в Check
	bare := addRemote(t, root)
	write(t, root, "code.txt", "broken\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 фича")
	gitT(t, root, "push", "-q")
	write(t, root, ".devkit/deploy.local",
		"deploy = test \"$(git rev-parse origin/main)\" = \"$(git rev-parse main)\" && touch redeployed.marker\nautonomous = true\n")

	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "redeployed.marker")); err != nil {
		t.Fatalf("повторный выкат до пуша отката или не отработал: %q", msg)
	}
	if !strings.Contains(msg, "откат запушен") || !strings.Contains(msg, "повторный выкат прошёл") ||
		!strings.Contains(msg, "доска запушена") {
		t.Fatalf("автономный revert должен пушить и катить сам: %q", msg)
	}
	rl := gitT(t, bare, "log", "main", "--format=%s")
	if !strings.Contains(rl, "revert: XR-001 откат") || !strings.Contains(rl, "обратно в In progress") {
		t.Fatalf("откат и доска не уехали в origin:\n%s", rl)
	}
}

const rowInProg3 = "| XR-003 | Вторая мелочь | task | P3 | 8 (0+3+0+5+0) |  |\n"

// branchFor заводит фичеветку задачи с одним коммитом кода и остаётся на ней:
// merge запускается с фичеветки. Тестовый файл зовётся по ID задачи, чтобы у
// веток разных задач не было ложного пересечения по одному и тому же файлу.
func branchFor(t *testing.T, root, id, branch, file string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", branch, "main")
	write(t, root, file, id+"\n")
	write(t, root, strings.ToLower(id)+"_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: "+id+" правка")
}

// taskWithScenario пишет минимальный файл задачи со сценарием проверки и
// коммитит его на текущую ветку: ворот сценария проверяет наличие раздела, а не
// его содержание. Звать до branchFor, чтобы файл ушёл на main и поднялся веткой.
func taskWithScenario(t *testing.T, root, id string) {
	t.Helper()
	write(t, root, "docs/tasks/"+id+".md", "# "+id+": заголовок\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n"+fixtureReviewLevel)
	gitT(t, root, "add", "docs/tasks/"+id+".md")
	gitT(t, root, "commit", "-qm", "docs(tasks): "+id+" файл задачи")
}

// TestTrainMergeAndShip: два поездных слияния копятся без выката и без
// перевода в Check, одиночный merge на непустом поезде отбивается, ship
// катит один деплой, двигает тег и переводит обе задачи в Check разом.
func TestTrainMergeAndShip(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")

	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "в поезде: XR-001") || strings.Contains(msg, "Check") {
		t.Fatalf("поездное слияние: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move") {
		t.Fatalf("поездное слияние не должно двигать доску: %q", calls)
	}
	gitT(t, root, "rev-parse", "--verify", "deployed") // первый merge --train заводит тег

	// Коммит только по файлам задач попадает в окно тега, но членства в
	// поезде не даёт: запись «в работу» это не код. Сценарий в файле нужен,
	// чтобы дальше XR-003 прошла ворот сценария при своём слиянии.
	write(t, root, "docs/tasks/XR-003.md", "# XR-003\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n"+fixtureReviewLevel)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 файл")
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "поезд: XR-001 слиты") {
		t.Fatalf("в поезде должна быть одна XR-001, без XR-003:\n%s", st)
	}

	// Одиночный merge при непустом поезде смешал бы выкаты.
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "в поезде XR-001") {
		t.Fatalf("одиночный merge на поезде должен отбиваться: %v", err)
	}
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "в поезде: XR-001, XR-003") {
		t.Fatalf("состав поезда после второго слияния: %q", msg)
	}

	write(t, root, ".devkit/deploy.local", "deploy = touch deployed.marker\nautonomous = false\n")
	msg, err = cmdShip(root, ShipParams{Deploy: "touch shipped.marker"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err != nil {
		t.Fatalf("выкат поезда не отработал: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") || !strings.Contains(string(calls), "move XR-003 check") {
		t.Fatalf("ship должен перевести обе задачи в Check: %q", calls)
	}
	log := gitT(t, root, "log", "--format=%s", "-1")
	if !strings.Contains(log, "XR-001, XR-003 в Check поездом") {
		t.Fatalf("коммит доски после ship: %s", log)
	}
	if gitT(t, root, "rev-parse", "deployed") != gitT(t, root, "rev-parse", "main^") {
		// main^ это состояние до коммита доски: тег двигается на выкаченный код.
		t.Fatal("тег deployed не сдвинут на выкаченный main")
	}
	// Поезд после выката пуст.
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err == nil ||
		!strings.Contains(err.Error(), "поезд пуст") {
		t.Fatalf("повторный ship должен отбиваться: %v", err)
	}
}

// TestMergeTrainQueueBusy: занятая очередь запирает прод, а не main. Поездное
// слияние на прод ничего не везёт и проходит; выкат и одиночный merge на той
// же доске отбиваются как раньше.
func TestMergeTrainQueueBusy(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	gitT(t, root, "tag", "deployed") // XR-009 уехала на прод и ждёт проверки

	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatalf("поездное слияние при занятой очереди должно проходить: %v", err)
	}
	if !strings.Contains(msg, "в поезде: XR-001") {
		t.Fatalf("поездное слияние: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move") {
		t.Fatalf("поездное слияние не должно двигать доску: %q", calls)
	}

	// Выкат остаётся запертым: непроверенный выкат один.
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err == nil ||
		!strings.Contains(err.Error(), "очередь занята: XR-009") {
		t.Fatalf("ship при занятой очереди должен отбиваться: %v", err)
	}

	// Одиночный merge при занятой очереди переводит себя в поезд: событие
	// освобождения очереди может застать уже без сессии, ветке нечего ждать
	// в своём дереве. Ворота сценария действуют и тут: ship повезёт задачу
	// в Check, проверять выкат будет нечем.
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"})
	if err != nil {
		t.Fatalf("одиночный merge при занятой очереди должен литься в поезд: %v", err)
	}
	if !strings.Contains(msg, "очередь занята: XR-009") ||
		!strings.Contains(msg, "в поезде: XR-001, XR-003") {
		t.Fatalf("состав поезда после второго слияния: %q", msg)
	}
}

// TestShipPreconditions: пустой поезд и занятая очередь это чистые ошибки.
func TestShipPreconditions(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if _, err := cmdShip(root, ShipParams{}); err == nil ||
		!strings.Contains(err.Error(), "поезд пуст") {
		t.Fatalf("ship без поезда должен отбиваться: %v", err)
	}
	root, _ = setup(t, rowInProg, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	if _, err := cmdShip(root, ShipParams{}); err == nil ||
		!strings.Contains(err.Error(), "очередь занята") {
		t.Fatalf("ship при занятой очереди должен отбиваться: %v", err)
	}
}

// TestRevertFromTrain: откат задачи из невыкаченного поезда не катит деплой
// (он увёз бы на прод остальной поезд) и выводит задачу из состава.
func TestRevertFromTrain(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	addRemote(t, root)
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	// autonomous=true: без защиты revert покатил бы повторный выкат сам и увёз
	// на прод невыкаченную XR-001 вместе с откатом.
	write(t, root, ".devkit/deploy.local", "deploy = touch redeployed.marker\nautonomous = true\n")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-003", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "redeployed.marker")); err == nil {
		t.Fatalf("откат из поезда не должен катить выкат: %q", msg)
	}
	if !strings.Contains(msg, "повторный выкат не нужен") {
		t.Fatalf("сообщение отката из поезда: %q", msg)
	}
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	train, _, err := trainTasks(root, "main", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(train) != 1 || train[0] != "XR-001" {
		t.Fatalf("после отката в поезде должна остаться одна XR-001: %v", train)
	}
}

// TestTrainTagPushed: тег deployed уезжает в origin и при поездном слиянии
// (иначе вторая машина не видит поезда и одиночный merge там увезёт чужие
// правки), и при ship, уже сдвинутым на выкаченный main.
func TestTrainTagPushed(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	bare := addRemote(t, root)
	write(t, root, ".devkit/deploy.local", "deploy = touch shipped.marker\nautonomous = true\n")

	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err == nil {
		t.Fatal("merge --train не должен катить выкат даже при autonomous=true")
	}
	if gitT(t, bare, "rev-parse", "deployed") != gitT(t, root, "rev-parse", "deployed") {
		t.Fatal("тег после merge --train не уехал в origin")
	}

	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err != nil {
		t.Fatalf("автономный ship не выкатил: %q", msg)
	}
	if gitT(t, bare, "rev-parse", "deployed") != gitT(t, root, "rev-parse", "deployed") {
		t.Fatal("сдвинутый тег после ship не уехал в origin")
	}
	if !strings.Contains(msg, "код запушен") || !strings.Contains(msg, "доска запушена") {
		t.Fatalf("автономный ship должен пушить сам: %q", msg)
	}
}

// TestTrainStray: задача с кодом в окне выката, выведенная руками из
// In progress, останавливает и merge, и ship: иначе её непроверенный код
// уехал бы на прод без Check.
func TestTrainStray(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	addRemote(t, root)
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	// Задачу увели в Blocked мимо shipctl.
	data, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	moved := strings.Replace(string(data),
		"## Blocked\n\nНет.\n", "## Blocked\n\n"+section(rowInProg), 1)
	moved = strings.Replace(moved, boardTable+rowInProg, boardTable, 1)
	write(t, root, "docs/TASKS.md", moved)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): руками в Blocked")

	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err == nil ||
		!strings.Contains(err.Error(), "не в In progress") {
		t.Fatalf("ship с осиротевшей задачей должен отбиваться: %v", err)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "аномалия") {
		t.Fatalf("status молчит про осиротевшую задачу:\n%s", st)
	}
	if strings.Contains(st, "очередь свободна") {
		t.Fatalf("вердикт про свободную очередь противоречит аномалии:\n%s", st)
	}

	// Откат осиротевшей не должен катить повторный выкат: он увёз бы на прод
	// невыкаченную XR-003. После отката аномалия снята и поезд едет.
	write(t, root, ".devkit/deploy.local", "deploy = touch shipped.marker\nautonomous = true\n")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err == nil {
		t.Fatalf("откат осиротевшей задачи покатил выкат: %q", msg)
	}
	if !strings.Contains(msg, "повторный выкат не нужен") {
		t.Fatalf("сообщение отката осиротевшей: %q", msg)
	}
	msg, err = cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatalf("после отката осиротевшей поезд должен ехать: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err != nil {
		t.Fatalf("выкат поезда после снятия аномалии не отработал: %q", msg)
	}
	if !strings.Contains(msg, "поезд выкачен (XR-003)") {
		t.Fatalf("в поезде должна остаться одна XR-003: %q", msg)
	}
}

// TestShipTagPushRejected: провал пуша тега (например, хук origin запрещает
// форс-пуш тегов) не роняет ship: доска доводится до Check и прикрывает
// вторую машину занятой очередью, напоминание про тег уходит в отчёт.
func TestShipTagPushRejected(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	write(t, root, ".devkit/deploy.local", "deploy = touch shipped.marker\nautonomous = true\n")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	write(t, bare, "hooks/pre-receive",
		"#!/bin/sh\nwhile read old new ref; do case \"$ref\" in refs/tags/*) exit 1;; esac; done\nexit 0\n")
	if err := os.Chmod(filepath.Join(bare, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatalf("ship должен пережить провал пуша тега: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shipped.marker")); err != nil {
		t.Fatalf("выкат не отработал: %q", msg)
	}
	if !strings.Contains(msg, "не запушен") || !strings.Contains(msg, "git push -f origin deployed") {
		t.Fatalf("нет напоминания про тег: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("доска не доведена до Check: %q", calls)
	}
}

// TestTrainDocsNotCargo: правка только под docs/ в окне выката не делает
// задачу ни грузом поезда, ни осиротевшей: LLD-задача из Backlog не роняет
// ship и не уезжает в Check с чужим выкатом.
func TestTrainDocsNotCargo(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	lldCommit(t, root, "XR-002") // XR-002 лежит в Backlog
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "аномалия") || !strings.Contains(st, "поезд: XR-001 слиты") {
		t.Fatalf("LLD-правка не должна давать ни аномалии, ни места в поезде:\n%s", st)
	}
	msg, err := cmdShip(root, ShipParams{Deploy: "true"})
	if err != nil {
		t.Fatalf("ship с LLD-правкой в окне должен ехать: %v", err)
	}
	if !strings.Contains(msg, "поезд выкачен (XR-001)") {
		t.Fatalf("состав поезда: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "XR-002") {
		t.Fatalf("ship не должен переводить LLD-задачу: %q", calls)
	}
}

// TestRevertDocsOnly: откат задачи, правившей только docs/, не катит
// повторный выкат (прод не менялся, а деплой увёз бы копящийся поезд) и не
// двигает тег. Docs-коммит лежит под тегом, вне окна поезда: тут задачу не
// спасает защита inTrain, работает именно фильтр docs/.
func TestRevertDocsOnly(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	addRemote(t, root)
	lldCommit(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	tag := gitT(t, root, "rev-parse", "deployed")
	write(t, root, ".devkit/deploy.local", "deploy = touch redeployed.marker\nautonomous = true\n")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-003", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "redeployed.marker")); err == nil {
		t.Fatalf("откат документной правки покатил выкат: %q", msg)
	}
	if !strings.Contains(msg, "повторный выкат не нужен") {
		t.Fatalf("сообщение отката: %q", msg)
	}
	if gitT(t, root, "rev-parse", "deployed") != tag {
		t.Fatal("тег deployed сдвинут, хотя прод не менялся")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "lld", "train.md")); err == nil {
		t.Fatal("правка docs/ не откатилась")
	}
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	train, strays, err := trainTasks(root, "main", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(train) != 1 || train[0] != "XR-001" || len(strays) != 0 {
		t.Fatalf("поезд после отката: train=%v strays=%v", train, strays)
	}
}

// TestTrainRevertCustomMsg: откат с кастомным -m (белый список префиксов)
// распознаётся по слову «откат», задача выбывает из поезда.
func TestTrainRevertCustomMsg(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001", Msg: "fix: XR-001 откат правки"}); err != nil {
		t.Fatal(err)
	}
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	train, strays, err := trainTasks(root, "main", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(train) != 0 || len(strays) != 0 {
		t.Fatalf("после отката поезд должен опустеть: train=%v strays=%v", train, strays)
	}
	// Кастомное сообщение работает и границей: второй заход не находит, что
	// откатывать, а не откатывает прошлый откат вместе со старой правкой.
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001", Msg: "fix: XR-001 откат правки"}); err == nil ||
		!strings.Contains(err.Error(), "откатывать нечего") {
		t.Fatalf("второй откат должен упереться в границу: %v", err)
	}
}

func TestLoadBoardAndReview(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	if b.sectOf("XR-001") != "in-progress" || b.sectOf("XR-002") != "backlog" || b.sectOf("XR-404") != "" {
		t.Fatalf("секции разобраны неверно: %+v", b.sects)
	}
	open, err := openReviewNotes(root, "XR-001")
	if err != nil || len(open) != 0 {
		t.Fatalf("закрытое ревью считается открытым: %v %v", open, err)
	}
	if open, _ := openReviewNotes(root, "XR-404"); open != nil {
		t.Fatal("отсутствующий файл задачи не должен давать замечаний")
	}
}

// TestPushCarriesGateEnv: разрешение на пуш едет хуку pre-push вместе с самим
// пушем. Окружение вызова собирает общий пакет (DK-697), и проверяется оно
// здесь так, как его видит рубеж: хук отбивает пуш без переменной.
func TestPushCarriesGateEnv(t *testing.T) {
	isolateGit(t)
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	hook := filepath.Join(root, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n[ \"$DEVKIT_PUSH_OK\" = 1 ] || exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "code.txt", "ещё\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "chore: правка")
	if _, err := git(root, "push"); err != nil {
		t.Fatalf("рубеж pre-push отбил пуш: %v", err)
	}
}

// Без taskctl не начинается ни start, ни merge, и отказ обязан называть
// работающую команду установки: старая подсказка звала собирать утилиту из
// devkit/taskctl, каталога, которого после переезда (DK-139, DK-140) нет.
func TestMissingTaskctlNamesTheInstallCommand(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	for _, c := range []struct {
		name string
		err  error
	}{
		{"start", func() error { _, err := cmdStart(root, StartParams{ID: "XR-001"}); return err }()},
		{"preflight", func() error { _, err := preflight(root); return err }()},
	} {
		if c.err == nil {
			t.Fatalf("%s: без taskctl в PATH команда не отказала", c.name)
		}
		msg := c.err.Error()
		if !strings.Contains(msg, "devkitctl.py update") {
			t.Fatalf("%s: отказ не называет команду установки: %s", c.name, msg)
		}
		if strings.Contains(msg, "devkit/taskctl") {
			t.Fatalf("%s: отказ зовёт в каталог, которого нет после переезда: %s", c.name, msg)
		}
	}
}

// TestCmdoutFrameCatchesSignificant это регрессионный тест DK-266: на выводе со
// значимым маркером (FAIL, panic:) в середине и хвостом без маркеров бывшая
// tail(out) показывала только последние 30 строк, и значимая строка из середины
// пропадала. cmdoutFrame строит сводку по формату LLD, и значимая строка видна
// агенту в блоке significant, а полный вывод лежит в файле по path. Тест падает
// на прежнем tail (в выводе нет ни significant, ни path), на новом проходит.
func TestCmdoutFrameCatchesSignificant(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	// Двести строк: в середине строки 40 и 80 несут FAIL и panic:, последние 30
	// строк (171..200) без маркеров. Прежняя tail(out) отдала бы только хвост.
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		switch i {
		case 40:
			b.WriteString("FAIL middle_row\n")
		case 80:
			b.WriteString("panic: middle_row_two\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	rendered := cmdoutFrame(root, "test", b.String())
	for _, want := range []string{
		"exit: 1",
		"lines_total: 200",
		"significant:",
		"FAIL middle_row",
		"panic: middle_row_two",
		"tail:",
		"line 200",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("в выжимке нет %q:\n%s", want, rendered)
		}
	}
	// path указывает на существующий файл внутри .devkit/cmdout.
	pathLine := ""
	for _, l := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		if strings.HasPrefix(l, "path: ") {
			pathLine = strings.TrimPrefix(l, "path: ")
			break
		}
	}
	if pathLine == "" {
		t.Fatalf("в выжимке нет path:\n%s", rendered)
	}
	if info, err := os.Stat(pathLine); err != nil || info.Size() == 0 {
		t.Errorf("файл вывода по path не читается или пуст: %s (err=%v)", pathLine, err)
	}
	// Хвост без маркеров: значимые строки из середины в хвост не попадают.
	for _, bad := range []string{"FAIL middle_row", "panic: middle_row_two"} {
		tailStart := strings.Index(rendered, "tail:\n")
		if tailStart < 0 {
			t.Fatal("хвост не найден в выжимке")
		}
		if strings.Contains(rendered[tailStart:], bad) {
			t.Errorf("значимая строка %q попала в хвост, а хвост был без маркеров", bad)
		}
	}
}

// TestCmdoutFrameShortOutput: ниже порога выжимка не строится, полный вывод
// отдаётся как есть, к нему приписывается path к файлу. Это путь коротких
// статусных выводов git (status --porcelain с парой строк), где резать нечего.
func TestCmdoutFrameShortOutput(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	out := " M code.txt\n?? draft.txt\n"
	rendered := cmdoutFrame(root, "git-status", out)
	if !strings.HasPrefix(rendered, out) {
		t.Errorf("короткий вывод не прошёл как есть: %q", rendered)
	}
	if !strings.Contains(rendered, "path: ") {
		t.Errorf("path пропал в коротком выводе: %q", rendered)
	}
	if strings.Contains(rendered, "lines_total") {
		t.Errorf("поле lines_total не должно присутствовать ниже порога: %q", rendered)
	}
}

// branchSewnWithMain собирает длинную ветку, которую догнали слиянием, а не
// ребейзом: ветка и main правят одну строку, конфликт разведён один раз в
// merge-коммите. Ребейз такую ветку расплющивает и возвращает конфликт по
// коммиту, поэтому звать его после сшивки нельзя (DK-637).
func branchSewnWithMain(t *testing.T, root string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "правка ветки\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	gitT(t, root, "checkout", "-q", "main")
	write(t, root, "code.txt", "правка main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "chore: правка на main")
	gitT(t, root, "checkout", "-q", "xr-001-fix")
	if out, err := exec.Command("git", "-C", root, "merge", "main").CombinedOutput(); err == nil {
		t.Fatalf("сшивка обязана конфликтовать, иначе тест не про то:\n%s", out)
	}
	write(t, root, "code.txt", "сшито руками\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "merge: XR-001 сшивка с main")
}

// TestMergeSewnBranchKeepsHistory: ветка, вобравшая main слиянием, сливается
// без ребейза. До DK-637 ребейз звался безусловно, расплющивал сшивку и падал
// на возвращённом конфликте, так что длинная ветка не сливалась вообще никак.
func TestMergeSewnBranchKeepsHistory(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchSewnWithMain(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("сшитая ветка не слилась: %v", err)
	}
	if !strings.Contains(msg, "слита в main fast-forward") {
		t.Fatalf("слияние не fast-forward: %q", msg)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "сшито руками\n" {
		t.Fatalf("на main уехало не разведённое слиянием: %q", got)
	}
	log := gitT(t, root, "log", "--format=%s")
	for _, want := range []string{"fix: XR-001 правка", "merge: XR-001 сшивка с main"} {
		if !strings.Contains(log, want) {
			t.Fatalf("ребейз расплющил историю, нет %q:\n%s", want, log)
		}
	}
}

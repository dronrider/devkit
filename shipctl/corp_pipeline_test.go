package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Тесты конвейера корп-контура (DK-087, DK-124, DK-074 «Конвейер: что
// остаётся от shipctl»): дерево задачи заводится в клоне и несёт код проекта,
// ветка по ключу тикета из ссылки зеркальной строки, вызов trackctl take в
// start, отказ merge/ship/revert честной строкой, отказ от подсчёта очереди
// в status и починка бага DK-081 (status спрашивал ветку у боковой
// директории, а не у клона). Фикстура и помощники редиректа (corpWrite,
// corpGitT, corpBoard) общие с corp_test.go, помощники доски (gitT,
// boardTable) общие с ops_test.go: все три файла делят один пакет.
//
// Фикстура повторяет ровно то, что кладёт настоящее подключение, а не удобное
// для теста. Таблица доски снята с доски, которую заводит taskctl init в
// боковой директории (семь колонок, ссылка последняя), строка задачи с той,
// что пишет taskctl add --link, привязка с той, что оставляет devkitctl corp
// (ensure_binding кладёт один repo) и дописывает человек по
// trackctl/README.md (contour, key, branch), а боковая директория остаётся
// без единого коммита, потому что подключение заводит её git-репозиторием, но
// ничего в него не коммитит. Первый заход DK-087 фикстуру этой правды не
// повторял (боковая директория несла коммиты и ключ key), поэтому зелёные
// тесты разошлись с находками на настоящем подключении, а DK-124 нашёлся уже
// на проде: фикстура не знала ни ключа key, ни ссылки на тикет, и подстановка
// локального ID в шаблон ветки выглядела в ней верной.

// corpTrack это привязка боковой директории для фикстуры. Key и Ticket врозь
// нарочно: ключ проекта живёт в привязке, ключ конкретного тикета в ссылке
// строки доски, и подмена одного другим это ровно баг DK-124.
type corpTrack struct {
	Branch, Repo string
	Key          string // ключ проекта в привязке; пусто значит «контур ещё не настроен»
	Ticket       string // ключ тикета в ссылке зеркальной строки; пусто значит ссылку «-», как её пишет taskctl без --link
	NoRepo       bool   // без ключа repo: неполная привязка, которую devkitctl corp не заводит, но которую руками испортить можно
}

// corpBoardTable это шапка таблицы доски, которую кладёт taskctl init:
// семь колонок, ссылка последняя. Старый шестиколоночный boardTable из
// ops_test.go остаётся под домашние тесты.
const corpBoardTable = "| ID | Задача | Тип | P | R | Цена | Ссылка |\n" +
	"|--------|--------|-----|---|---|------|--------|\n"

type trackMode int

const (
	trackOK      trackMode = iota // стаб trackctl отвечает успехом
	trackFail                     // стаб trackctl падает (подложный отказ адаптера)
	trackMissing                  // trackctl не кладётся в PATH вовсе
)

// corpPipeline строит связку клон + боковая директория: клон с закоммиченным
// README на main и заведённым редиректом devkit.local (код проекта живёт
// здесь), боковая директория рядом с доской (одна задача id в Backlog) и
// привязкой, без единого коммита. Стабы taskctl и trackctl лежат в общем
// PATH, каждый пишет свои вызовы в собственный лог; trackctl по mode либо
// отвечает успехом, либо падает, либо не кладётся вовсе.
func corpPipeline(t *testing.T, id string, tr corpTrack, mode trackMode) (clone, local, taskLog, trackLog string) {
	t.Helper()
	base := t.TempDir()
	clone = filepath.Join(base, "proj")
	local = filepath.Join(base, "proj-local")

	corpWrite(t, filepath.Join(clone, "README.md"), "проект\n")
	corpGitT(t, clone, "init", "-q", "-b", "main")
	corpGitT(t, clone, "config", "user.email", "test@test")
	corpGitT(t, clone, "config", "user.name", "test")
	corpGitT(t, clone, "add", ".")
	corpGitT(t, clone, "commit", "-q", "-m", "init")
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")

	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	corpGitT(t, local, "init", "-q", "-b", "main")
	corpGitT(t, local, "config", "user.email", "test@test")
	corpGitT(t, local, "config", "user.name", "test")

	prefix := strings.Split(id, "-")[0]
	// Ссылка строки: у зеркальной строки она ведёт на тикет (так её пишет
	// taskctl add --link), у обычной стоит прочерк.
	link := "-"
	if tr.Ticket != "" {
		link = "[" + tr.Ticket + "](https://tracker.example.com/browse/" + tr.Ticket + ")"
	}
	board := "# Тест: доска (префикс " + prefix + ")\n\n" +
		"## In progress\n\nНет.\n\n" +
		"## Check\n\nНет.\n\n" +
		"## Backlog\n\n" + corpBoardTable +
		"| " + id + " | Тикет | task | P3 | 10 (0+5+0+0+5) | S | " + link + " |\n\n" +
		"## Blocked\n\nНет.\n"
	corpWrite(t, filepath.Join(local, "docs", "TASKS.md"), board)

	// Ровно то, что лежит в привязке подключённого проекта: комментарий и repo
	// от devkitctl corp (ensure_binding), contour, key и branch от человека по
	// trackctl/README.md.
	var tb strings.Builder
	tb.WriteString("# Привязка проекта к трекеру (trackctl/README.md). Ключ repo кладёт\n")
	tb.WriteString("# подключение: по нему обвязка клона находится после переклонирования.\n")
	if tr.Key != "" {
		tb.WriteString("contour = corp\n")
		tb.WriteString("key = " + tr.Key + "\n")
	}
	if tr.Branch != "" {
		tb.WriteString("branch = \"" + tr.Branch + "\"\n")
	}
	if !tr.NoRepo {
		repo := tr.Repo
		if repo == "" {
			repo = "../proj"
		}
		tb.WriteString("repo = " + repo + "\n")
	}
	corpWrite(t, filepath.Join(local, corpTrackerPath), tb.String())
	if tr.NoRepo {
		// Без repo дерево задачи заводится в самой боковой директории
		// (домашний путь), которому, как и дома, нужен хотя бы один коммит:
		// это отдельный от находки 4 случай, привязка тут заведомо неполная.
		corpGitT(t, local, "add", ".")
		corpGitT(t, local, "commit", "-q", "-m", "seed")
	}

	bin := t.TempDir()
	taskLog = filepath.Join(bin, "calls.log")
	taskStub := "#!/bin/sh\necho \"$@\" >> \"" + taskLog + "\"\nprintf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n"
	corpWrite(t, filepath.Join(bin, "taskctl"), taskStub)
	if err := os.Chmod(filepath.Join(bin, "taskctl"), 0o755); err != nil {
		t.Fatal(err)
	}

	trackLog = filepath.Join(bin, "track.log")
	if mode != trackMissing {
		var trackStub string
		if mode == trackFail {
			trackStub = "#!/bin/sh\necho \"$@\" >> \"" + trackLog + "\"\necho подложный адаптер отказал >&2\nexit 1\n"
		} else {
			trackStub = "#!/bin/sh\necho \"$@\" >> \"" + trackLog + "\"\necho взят в работу\n"
		}
		corpWrite(t, filepath.Join(bin, "trackctl"), trackStub)
		if err := os.Chmod(filepath.Join(bin, "trackctl"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	path := os.Getenv("PATH")
	if mode == trackMissing {
		// На машине разработчика trackctl обычно уже стоит в PATH (тем же
		// devkit), и голое prepend его бы не спрятало: LookPath просто нашёл
		// бы стаб первым, а без стаба заглянул бы дальше и нашёл настоящий.
		// Мутация «trackctl не найден» проверяется по-честному: реальный
		// бинарь вычищается из PATH совсем.
		path = pathWithout(path, "trackctl")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+path)
	return clone, local, taskLog, trackLog
}

// pathWithout вычищает из PATH директории, где лежит exe: используется,
// чтобы честно спрятать команду, уже стоящую на машине глобально.
func pathWithout(path, exe string) string {
	var kept []string
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, exe)); err == nil {
			continue
		}
		kept = append(kept, dir)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// TestCorpStartBranchTemplate: ветка строится по шаблону привязки с ключом
// тикета из ссылки зеркальной строки (DK-074 «Граница доски и тикета»), а не
// по локальному ID доски и не по домашнему «строчный ID плюс слаг». Префикс
// доски (LOC) и ключ проекта (ABC) здесь разные, а номера расходятся, поэтому
// подмена ключа тикета локальным ID видна по значению, а не только по
// падению: это и есть баг DK-124, ловится он тут ровно так. Шаблон с
// префиксом «feature/» и сохранением регистра отличает результат и от
// домашней ветки. Ловит и находку 1 DK-087: дерево задачи заводится в клоне и
// несёт код проекта, а не одну доску боковой директории.
func TestCorpStartBranchTemplate(t *testing.T) {
	clone, local, taskLog, trackLog := corpPipeline(t, "LOC-3",
		corpTrack{Branch: "feature/{key}-{slug}", Key: "ABC", Ticket: "ABC-42"}, trackOK)
	msg, err := cmdStart(local, StartParams{ID: "LOC-3", Slug: "add-x"})
	if err != nil {
		t.Fatal(err)
	}
	want := "feature/ABC-42-add-x"
	wt := filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-abc-42")
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != want {
		t.Fatalf("ветка построена как %q, ожидалась %q по ключу тикета из ссылки строки", br, want)
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("в отчёте нет имени ветки по шаблону: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatalf("дерево задачи %s не несёт код проекта (заведено не в клоне): %v", wt, err)
	}
	// Директория дерева задачи это запись в .git корп-клона, то есть такой же
	// корп-артефакт, как ветка: локальному ID доски там не место.
	if strings.Contains(wt, "loc-3") {
		t.Fatalf("дерево задачи названо локальным ID доски: %q", wt)
	}
	calls, _ := os.ReadFile(taskLog)
	if !strings.Contains(string(calls), "move LOC-3 in-progress") {
		t.Fatalf("задача не переведена в In progress: %q", calls)
	}
	tcalls, _ := os.ReadFile(trackLog)
	if !strings.Contains(string(tcalls), "take ABC-42") {
		t.Fatalf("trackctl take позван не с ключом тикета: %q", tcalls)
	}
	if strings.Contains(string(tcalls), "LOC-3") {
		t.Fatalf("в трекер ушёл локальный ID доски, такого тикета там нет: %q", tcalls)
	}
	if !strings.Contains(msg, "взят в работу") {
		t.Fatalf("в отчёте нет ответа трекера: %q", msg)
	}
}

// TestCorpStartPrefixEqualsKey: префикс доски совпадает с ключом проекта, то
// есть ровно та связка, на которой нашёлся DK-124. Ключ тикета всё равно
// берётся из ссылки, а не из ID строки, и номера тут расходятся нарочно:
// строка AC-001 зеркалит тикет AC-777. Совпадение префиксов не должно ни
// ронять start, ни возвращать подстановку ID.
func TestCorpStartPrefixEqualsKey(t *testing.T) {
	clone, local, _, trackLog := corpPipeline(t, "AC-001",
		corpTrack{Key: "AC", Ticket: "AC-777"}, trackOK)
	msg, err := cmdStart(local, StartParams{ID: "AC-001", Slug: "login"})
	if err != nil {
		t.Fatal(err)
	}
	want := "AC-777-login"
	wt := filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-ac-777")
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != want {
		t.Fatalf("ветка построена как %q, ожидалась %q по ключу тикета", br, want)
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("в отчёте нет имени ветки: %q", msg)
	}
	tcalls, _ := os.ReadFile(trackLog)
	if !strings.Contains(string(tcalls), "take AC-777") {
		t.Fatalf("trackctl take позван не с ключом тикета: %q", tcalls)
	}
}

// TestCorpStartRowWithoutTicket: контур настроен (в привязке есть key), а
// строка доски на тикет не ведёт. Имя ветки корп-репозитория брать неоткуда,
// локальный ID туда не едет, поэтому start отказывает и называет, чем
// чинится. Ловит мутацию «молча подставил локальный ID»: именно так вёл себя
// код до DK-124.
func TestCorpStartRowWithoutTicket(t *testing.T) {
	_, local, taskLog, _ := corpPipeline(t, "LOC-3", corpTrack{Key: "ABC"}, trackOK)
	_, err := cmdStart(local, StartParams{ID: "LOC-3", Slug: "add-x"})
	if err == nil {
		t.Fatal("строка без ссылки на тикет обязана валить start в корп-контуре")
	}
	if !strings.Contains(err.Error(), "не ведёт на тикет ABC-N") {
		t.Fatalf("отказ не называет, чего не хватает: %v", err)
	}
	if calls, _ := os.ReadFile(taskLog); strings.Contains(string(calls), "move") {
		t.Fatalf("отказ обязан быть до перевода доски: %q", calls)
	}
}

// TestCorpStartNoTrackerKey: привязка сразу после devkitctl corp, в ней один
// repo. Ключ тикета вылавливать нечем, но доска не встаёт из-за ненастроенного
// трекера: дерево задачи заводится, ветка остаётся домашней, а находка идёт
// строкой в отчёт, а не молчком. Ловит обе мутации: отказ на ненастроенном
// контуре и молчание про домашнее имя ветки.
func TestCorpStartNoTrackerKey(t *testing.T) {
	clone, local, _, _ := corpPipeline(t, "LOC-3", corpTrack{Ticket: "ABC-42"}, trackOK)
	msg, err := cmdStart(local, StartParams{ID: "LOC-3", Slug: "add-x"})
	if err != nil {
		t.Fatalf("ненастроенный трекер не должен ронять start: %v", err)
	}
	wt := filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-loc-3")
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != "loc-3-add-x" {
		t.Fatalf("без ключа key ветка должна остаться домашней, а вышла %q", br)
	}
	if !strings.Contains(msg, "без ключа key") {
		t.Fatalf("домашнее имя ветки не объяснено в отчёте: %q", msg)
	}
}

// TestCorpStartTwice: второй заход на ту же задачу упирается в дерево прошлого
// захода. Ветка и дерево названы ключом тикета, и искать их по локальному ID
// значило бы завести задаче вторую ветку молча.
func TestCorpStartTwice(t *testing.T) {
	_, local, _, _ := corpPipeline(t, "LOC-3",
		corpTrack{Branch: "feature/{key}-{slug}", Key: "ABC", Ticket: "ABC-42"}, trackOK)
	if _, err := cmdStart(local, StartParams{ID: "LOC-3", Slug: "add-x"}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdStart(local, StartParams{ID: "LOC-3", Slug: "add-y"})
	if err == nil || !strings.Contains(err.Error(), "уже в работе") {
		t.Fatalf("второй заход обязан найти дерево прошлого: %v", err)
	}
}

// TestCorpStartTrackctlMissing ловит мутацию «отсутствие trackctl уронило
// start»: доска не должна вставать из-за трекера, отсутствие называется
// вслух, а не проглатывается.
func TestCorpStartTrackctlMissing(t *testing.T) {
	_, local, taskLog, _ := corpPipeline(t, "ABC-1", corpTrack{}, trackMissing)
	msg, err := cmdStart(local, StartParams{ID: "ABC-1"})
	if err != nil {
		t.Fatalf("trackctl вне PATH не должен ронять start: %v", err)
	}
	if !strings.Contains(msg, "trackctl не найден в PATH") {
		t.Fatalf("отсутствие trackctl не названо вслух: %q", msg)
	}
	calls, _ := os.ReadFile(taskLog)
	if !strings.Contains(string(calls), "move ABC-1 in-progress") {
		t.Fatalf("доска обязана перевестись в In progress вне зависимости от трекера: %q", calls)
	}
}

// TestCorpStartTrackctlFails: подложный адаптер отказал, start всё равно
// доводит взятие в работу до конца, отказ трекера идёт строкой в отчёт.
func TestCorpStartTrackctlFails(t *testing.T) {
	_, local, _, trackLog := corpPipeline(t, "ABC-1", corpTrack{}, trackFail)
	msg, err := cmdStart(local, StartParams{ID: "ABC-1"})
	if err != nil {
		t.Fatalf("отказ trackctl take не должен ронять start: %v", err)
	}
	if !strings.Contains(msg, "trackctl take не прошёл") {
		t.Fatalf("отказ трекера не назван вслух: %q", msg)
	}
	tcalls, _ := os.ReadFile(trackLog)
	if !strings.Contains(string(tcalls), "take ABC-1") {
		t.Fatalf("trackctl take не был позван: %q", tcalls)
	}
}

// TestCorpStartNoCommitsInLocal воспроизводит ровно состояние сразу после
// devkitctl corp (находка 4): боковая директория без единого коммита. Раньше
// start спрашивал main или master у неё и падал на «не нашёл ветку main или
// master», хотя код и история лежат в клоне.
func TestCorpStartNoCommitsInLocal(t *testing.T) {
	_, local, _, _ := corpPipeline(t, "ABC-1", corpTrack{}, trackOK)
	if out, err := corpGit(local, "log"); err == nil {
		t.Fatalf("фикстура должна быть без коммитов в боковой директории, а нашёлся лог: %q", out)
	}
	if _, err := cmdStart(local, StartParams{ID: "ABC-1"}); err != nil {
		t.Fatalf("start не должен падать на боковой директории без коммитов: %v", err)
	}
}

// TestCorpStartBindingWithoutRepo: привязка есть, но без ключа repo (обвязка
// неполная и руками испорченная, devkitctl corp так не оставляет). Ветка по
// шаблону компании в дереве без кода компании была бы половинчатым и
// обманчивым состоянием (замечание ревью DK-087), поэтому без клона ветка
// остаётся домашней тем же порядком, что и дерево задачи: оба переключаются
// одним условием. trackctl take при этом всё равно зовётся: он работает от
// ключа тикета (самого ID), а не от местоположения клона.
func TestCorpStartBindingWithoutRepo(t *testing.T) {
	_, local, _, trackLog := corpPipeline(t, "ABC-1", corpTrack{NoRepo: true}, trackOK)
	msg, err := cmdStart(local, StartParams{ID: "ABC-1", Slug: "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := "abc-1-x"
	wt := filepath.Join(filepath.Dir(local), filepath.Base(local)+"-abc-1")
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != want {
		t.Fatalf("без ключа repo ветка должна остаться домашней: %q, ожидалась %q", br, want)
	}
	if !strings.Contains(msg, "без ключа repo") {
		t.Fatalf("неполная привязка не названа вслух: %q", msg)
	}
	tcalls, _ := os.ReadFile(trackLog)
	if !strings.Contains(string(tcalls), "take ABC-1") {
		t.Fatalf("trackctl take обязан зваться и без ключа repo, он не завязан на местоположение клона: %q", tcalls)
	}
}

// TestCorpMergeShipRevertRefuse ловит мутацию «merge в корп-контуре прошёл»:
// слияние и выкат в корп-контуре ведёт MR-флоу компании, merge, ship и
// revert обязаны отказывать честной строкой с ручными шагами, а не молчать
// или изображать слияние, которого нет.
func TestCorpMergeShipRevertRefuse(t *testing.T) {
	_, local, _, _ := corpPipeline(t, "ABC-1", corpTrack{}, trackOK)
	if _, err := cmdMerge(local, MergeParams{ID: "ABC-1", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "trackctl submit") {
		t.Fatalf("merge в корп-контуре должен отказывать с подсказкой про MR-флоу: %v", err)
	}
	if _, err := cmdShip(local, ShipParams{}); err == nil ||
		!strings.Contains(err.Error(), "trackctl submit") {
		t.Fatalf("ship в корп-контуре должен отказывать: %v", err)
	}
	if _, err := cmdRevert(local, RevertParams{ID: "ABC-1"}); err == nil ||
		!strings.Contains(err.Error(), "trackctl submit") {
		t.Fatalf("revert в корп-контуре должен отказывать: %v", err)
	}
}

// TestCorpStatusBranchFromClone чинит DK-081: status называл веткой ветку
// боковой директории (где живёт только доска), а не рабочую ветку клона.
// Ловит и мутацию «status спросил ветку у корня»: ветки боковой директории
// (main) и клона (feature-work) заведомо разные, подмена источника видна по
// значению, а не только по падению.
func TestCorpStatusBranchFromClone(t *testing.T) {
	clone, local, _, _ := corpPipeline(t, "ABC-1", corpTrack{}, trackOK)
	corpGitT(t, clone, "checkout", "-qb", "feature-work")

	msg, err := cmdStatus(local)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "ветка: feature-work") {
		t.Fatalf("status должен называть рабочую ветку клона: %q", msg)
	}
	if strings.Contains(msg, "ветка: main") {
		t.Fatalf("status назвал веткой ветку боковой директории: %q", msg)
	}
	if !strings.Contains(msg, "корп-контур:") {
		t.Fatalf("status должен объяснить, почему очередь не считается: %q", msg)
	}
	if strings.Contains(msg, "выкат:") || strings.Contains(msg, "поезд:") || strings.Contains(msg, "очередь свободна") {
		t.Fatalf("status не должен считать очередь, поезд и выкат в корп-контуре: %q", msg)
	}
}

// TestCorpStatusNoCommitsInLocal воспроизводит находку из «Найденного по
// ходу серии» дословно: боковая директория без единого коммита (доска ещё не
// коммитилась) роняла status на git rev-parse. Фикстура собрана руками, как
// и раскладывает её devkitctl corp: только repo в привязке, ни одного
// коммита в боковой директории.
func TestCorpStatusNoCommitsInLocal(t *testing.T) {
	base := t.TempDir()
	clone := filepath.Join(base, "proj")
	local := filepath.Join(base, "proj-local")
	corpWrite(t, filepath.Join(clone, "README.md"), "проект\n")
	corpGitT(t, clone, "init", "-q", "-b", "main")
	corpGitT(t, clone, "config", "user.email", "test@test")
	corpGitT(t, clone, "config", "user.name", "test")
	corpGitT(t, clone, "add", ".")
	corpGitT(t, clone, "commit", "-q", "-m", "init")
	corpGitT(t, clone, "checkout", "-qb", "dk-099-feature")
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")

	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	corpGitT(t, local, "init", "-q", "-b", "main")
	corpBoard(t, local)
	corpWrite(t, filepath.Join(local, corpTrackerPath), "repo = ../proj\n")
	// Ни одного коммита в local: воспроизводит ровно репро DK-081, и ровно то
	// состояние, в котором подключение оставляет боковую директорию.

	msg, err := cmdStatus(local)
	if err != nil {
		t.Fatalf("status не должен падать на боковой директории без коммитов: %v", err)
	}
	if !strings.Contains(msg, "ветка: dk-099-feature") {
		t.Fatalf("status должен назвать ветку клона: %q", msg)
	}
}

// TestCorpHomeUnaffected ловит мутацию «корп-поведение включилось в
// домашнем проекте»: без редиректа и без .devkit/tracker.local start и
// status обязаны вести себя ровно как раньше, ни одна корп-строка не должна
// просочиться в отчёт.
func TestCorpHomeUnaffected(t *testing.T) {
	root, callLog := setup(t, "", "")
	msg, err := cmdStart(root, StartParams{ID: "XR-002", Slug: "wt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"трекер", "привязка", "trackctl", "корп-контур"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("домашний start не должен упоминать %q: %q", bad, msg)
		}
	}
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-xr-002")
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002-wt" {
		t.Fatalf("домашняя ветка должна остаться прежней: %q", br)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-002 in-progress") {
		t.Fatalf("доска не переведена: %q", calls)
	}

	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "корп-контур") {
		t.Fatalf("домашний status не должен упоминать корп-контур: %q", st)
	}
	if !strings.Contains(st, "очередь свободна") {
		t.Fatalf("домашний status должен по-прежнему считать очередь: %q", st)
	}
}

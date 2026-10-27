package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

func TestScenariosCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка: %v\n%s", err, out)
	}
	root := setup(t)
	t.Setenv("DEVKIT_TMUX", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("DEVKIT_HEADLESS", "1")
	run := func(wantOK bool, args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-C", root, "decide"}, args...)...).CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	mustContain := func(text string, words ...string) {
		t.Helper()
		for _, word := range words {
			if !strings.Contains(text, word) {
				t.Fatalf("нет %q:\n%s", word, text)
			}
		}
	}
	path := taskFilePath(root, "XR-005")
	write := func(doc string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string { return readTask(t, root, "XR-005") }
	write(read() + "\n## Пользовательские сценарии\n\n### СЦ-1\nситуация: выбор\nдействие: назвать пункты\nрезультат: выбранные ID\n\n### СЦ-2\nситуация: пример\n\n### СЦ-3\nситуация: перерыв\n")
	run(true, "XR-005", "--ask", "сценарии", "--scenarios", "--hint", "СЦ-1, СЦ-3: выбор и продолжение",
		"--option", "СЦ-1 (слово пользователя): выбрать", "--option", "СЦ-2 (вывод из примера): собрать", "--option", "СЦ-3 (предложение агента): продолжить", "что включаем?")
	keyRe := regexp.MustCompile(`снимок сценариев: (sc1-[a-f0-9]{64})`)
	chat := func() (string, string) {
		t.Helper()
		out := run(true, "XR-005", "сценарии", "--chat")
		match := keyRe.FindStringSubmatch(out)
		if match == nil {
			t.Fatalf("нет снимка: %s", out)
		}
		return match[1], out
	}
	answer := func(key, text string) string {
		return run(true, "XR-005", "сценарии", "--snapshot", key, "--answer", text)
	}
	failUnchanged := func(key, text, word string) {
		t.Helper()
		before := read()
		got := run(false, "XR-005", "сценарии", "--snapshot", key, "--answer", text)
		mustContain(got, word)
		if read() != before {
			t.Fatal("отказ частично изменил файл")
		}
	}
	key, block := chat()
	mustContain(block, "1. СЦ-1. выбрать", "3. СЦ-3. продолжить", "рекомендую 1, 3", "слово пользователя", "ситуация: выбор")
	if strings.Index(block, "ситуация: выбор") > strings.Index(block, "снимок сценариев:") {
		t.Fatal("карточки стоят после выбора")
	}
	before := read()
	key2, _ := chat()
	if key != key2 || read() != before {
		t.Fatal("повтор печати изменил снимок")
	}
	mustContain(run(true, "XR-005", "сценарии", "--snapshot", key), "1=СЦ-1", "choiceEvent")
	failUnchanged(key, "сценарии 1, 99, плюс новый", "позиция 99")
	failUnchanged(key, "сценарии СЦ-9", "СЦ-9")
	failUnchanged(key, "плюс 9999999999999999999999999999999", "позиция")
	failUnchanged("sc1-unknown", "сценарии 1", "неизвестен")
	failUnchanged(key, "", "ответ пуст")
	before = read()
	mustContain(run(false, "XR-005", "сценарии", "--answer", "сценарии 1"), "--snapshot")
	if read() != before {
		t.Fatal("ответ без снимка записан")
	}
	text := " сценарии 1, 3  "
	result := answer(key, text)
	mustContain(result, "выбрано: СЦ-1, СЦ-3", "сверить зависимые")
	before = read()
	if got := answer(key, text); got != result || read() != before {
		t.Fatalf("повтор доставки изменился: %q / %q", got, result)
	}
	mustContain(read(), `"text":" сценарии 1, 3  "`)
	open := run(true, "XR-005", "--open")
	mustContain(open, "выбрано: СЦ-1, СЦ-3")
	key, _ = chat()
	mustContain(answer(key, "минус СЦ-3; плюс 2"), "выбрано: СЦ-1, СЦ-2")
	key, _ = chat()
	own := `плюс "увидеть причину отказа, затем исправить ввод"`
	result = answer(key, own)
	mustContain(result, "СЦ-4", "добавлено: СЦ-4")
	before = read()
	if answer(key, own) != result || read() != before {
		t.Fatal("повтор своего сценария выдал новый ID")
	}
	mustContain(read(), "следующий ID: СЦ-5", "СЦ-4 (слово пользователя): увидеть причину отказа, затем исправить ввод")
	// Повтор собственной команды допустим даже после дописывания карточки.
	write(strings.Replace(read(), "\nситуация: выбор\n", "\nситуация: уточнённый выбор\n", 1))
	before = read()
	if answer(key, own) != result || read() != before {
		t.Fatal("дополнение карточки сломало повтор доставки")
	}
	key, _ = chat()
	empty := answer(key, "ничего")
	mustContain(empty, "прежний состав отменён", "выбор открыт")
	before = read()
	if answer(key, "ничего") != empty || read() != before {
		t.Fatal("повтор отказа изменил историю")
	}
	f, _ := taskform.FindFork(read(), "сценарии")
	if !f.HoldsStart() || f.Decided() {
		t.Fatal("отказ от всех закрыл выбор")
	}
	afterEmpty := run(true, "XR-005", "--open")
	if strings.Contains(afterEmpty, "решено человеком") || strings.Contains(afterEmpty, "выбрано:") {
		t.Fatal("прежний состав показан действующим после отказа")
	}
	mustContain(run(true, "XR-005", "сценарии"), "история: решено человеком")
	key, _ = chat()
	failUnchanged(key, "плюс 2", "нужна основа")
	mustContain(answer(key, "по рекомендации; плюс 2"), "выбрано: СЦ-1, СЦ-2, СЦ-3")
	// Снятие всех имеет тот же открытый исход.
	key, _ = chat()
	mustContain(answer(key, "минус 1; минус 2; минус 3"), "отказ от всех сохранён")
	key, _ = chat()
	result = answer(key, "сценарии 1; плюс 3")
	// A -> B -> A не оживляет первую пару снимка и ответа.
	oldKey := key
	oldText := "сценарии 1; плюс 3"
	key, _ = chat()
	answer(key, "сценарии 2")
	key, _ = chat()
	answer(key, oldText)
	failUnchanged(oldKey, oldText, "устарел")
	// Перестановка меняет снимок; текущие позиции по-прежнему выбирают ID.
	key, _ = chat()
	before = read()
	first := "  - вариант: СЦ-1 (слово пользователя): выбрать"
	second := "  - вариант: СЦ-2 (вывод из примера): собрать"
	changed := strings.Replace(before, first, "__FIRST__", 1)
	changed = strings.Replace(changed, second, first, 1)
	changed = strings.Replace(changed, "__FIRST__", second, 1)
	write(changed)
	failUnchanged(key, "сценарии 1", "прежние позиции: 1=СЦ-1")
	key, block = chat()
	mustContain(block, "1. СЦ-2. собрать")
	answer(key, "сценарии 1, 3")
	f, _ = taskform.FindFork(read(), "сценарии")
	if f.Answer != "СЦ-2, СЦ-3" {
		t.Fatal(f.Answer)
	}
	// Снятие старой строки не возвращает выданный ID.
	write(strings.Replace(read(), "  - вариант: СЦ-4 (слово пользователя): увидеть причину отказа, затем исправить ввод\n", "", 1))
	before = read()
	mustContain(run(false, "XR-005", "сценарии", "--option", "СЦ-4 (слово пользователя): ещё раз"), "уже выдан")
	if read() != before {
		t.Fatal("переиспользование ID изменило файл")
	}
	key, _ = chat()
	mustContain(answer(key, "плюс новый сценарий"), "добавлено: СЦ-5")
	// По старому снимку не принимаются новые команды после изменения карточки или довода.
	key, _ = chat()
	write(strings.Replace(read(), "\nситуация: пример\n", "\nситуация: другой пример\n", 1))
	failUnchanged(key, "сценарии 2", "карточки")
	key, _ = chat()
	write(strings.Replace(read(), "выбор и продолжение", "другой довод", 1))
	failUnchanged(key, "сценарии 2", "довод")
	// Повреждение JSON обнаруживается до повтора и не оставляет запись.
	key, _ = chat()
	goodSnapshotDoc := read()
	write(strings.Replace(read(), "  - снимок: "+key+" {", "  - снимок: "+key+" { ", 1))
	failUnchanged(key, "сценарии 2", "SHA-256")
	write(goodSnapshotDoc)
	// Ключ из другой записи нельзя перенести вместе с JSON.
	run(true, "XR-002", "--ask", "сценарии", "--scenarios", "--hint", "СЦ-1: нужен", "--option", "СЦ-1 (слово пользователя): другое", "что включаем?")
	foreign := run(true, "XR-002", "сценарии", "--chat")
	foreignKey := keyRe.FindStringSubmatch(foreign)[1]
	foreignDoc := readTask(t, root, "XR-002")
	var snapshotLine string
	for _, line := range strings.Split(foreignDoc, "\n") {
		if strings.HasPrefix(line, "  - снимок: ") {
			snapshotLine = line
		}
	}
	doc, err := taskform.AppendToFork(read(), "сценарии", snapshotLine)
	if err != nil {
		t.Fatal(err)
	}
	write(doc)
	failUnchanged(foreignKey, "сценарии 1", "другой записи")
	// Прямо заданный состав создаётся без вопроса и содержит исходные слова.
	run(true, "XR-003", "--ask", "сценарии", "--scenarios", "--hint", "СЦ-1: нужен", "--option", "СЦ-1 (слово пользователя): задано", "--given", "СЦ-1", "--said", "берём только заданный сценарий", "что включаем?")
	given, _ := taskform.FindFork(readTask(t, root, "XR-003"), "сценарии")
	if !given.Decided() || given.Answer != "СЦ-1" || given.ChoiceEvent != 1 {
		t.Fatalf("перенос: %+v", given)
	}
	mustContain(run(true, "XR-003", "сценарии"), "берём только заданный сценарий")
	// Пометка сверки читается новым процессом, подтверждение снимает её по порядку строк.
	run(true, "XR-005", "--ask", "срок", "--hint", "30 дней", "сколько хранить?")
	run(true, "XR-005", "срок", "--by", "человек", "30 дней")
	doc, _ = taskform.AppendToFork(read(), "срок", "  - требует сверки 2026-10-26: состав изменился")
	write(doc)
	mustContain(run(true, "XR-005", "--open"), "«срок» (требует сверки)", "30 дней", "состав изменился")
	key, _ = chat()
	before = read()
	mustContain(answer(key, "сценарии 2, обсудим, по рекомендации"), "не разобрано")
	if read() != before {
		t.Fatal("неразобранный текст применил рекомендацию")
	}
	run(true, "XR-005", "срок", "--by", "человек", "30 дней, подтверждаю")
	if strings.Contains(run(true, "XR-005", "--open"), "«срок»") {
		t.Fatal("подтверждённый вопрос остался открытым")
	}
	doc, _ = taskform.AppendToFork(read(), "срок", "  - требует сверки 2026-10-25: ещё одно изменение")
	write(doc)
	mustContain(run(true, "XR-005", "--chat"), "«срок»")
	// Обычный вопрос и старый состав продолжают прежний первый ответ.
	run(true, "XR-004", "--ask", "доступ", "--hint", "есть", "--option", "нет", "есть доступ?")
	mustContain(run(true, "XR-004", "--chat"), "1. рекомендую: есть", chatAnswerHint)
	run(true, "XR-004", "--answer", "доступ 2")
	if strings.Contains(run(true, "XR-004", "--open"), "«доступ»") {
		t.Fatal("обычный вопрос открыт после ответа")
	}
	run(true, "XR-004", "--ask", "кейсы", "--hint", lineupMid, "--option", lineupNar, "--option", lineupWide, "какой состав?")
	run(true, "XR-004", "--answer", "минус 3")
	legacy, _ := taskform.FindFork(readTask(t, root, "XR-004"), "кейсы")
	if !legacy.Decided() || !strings.Contains(legacy.Answer, "2 кейса") {
		t.Fatalf("старый состав: %+v", legacy)
	}
}

func TestScenarioWriteDetectsConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.md")
	if err := os.WriteFile(path, []byte("другой текст"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := decideWrite(path, "прежний текст", "ответ"); err == nil {
		t.Fatal("записан ответ поверх чужой правки")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "другой текст" {
		t.Fatal("чужая правка потеряна")
	}
}

func TestScenarioRetryKeepsWaitingAndByUsesSnapshot(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "сценарии", Scenarios: true, Hint: "СЦ-1: нужен", Opts: []string{"СЦ-1 (слово пользователя): выбрать", "СЦ-2 (предложение агента): продолжить"}, Text: "что включаем?"})
	block := chatRun(t, root, DecideParams{ID: "XR-005", Name: "сценарии", Chat: true}, &askDeps{}, botEnv)
	key := regexp.MustCompile(`снимок сценариев: (sc1-[a-f0-9]{64})`).FindStringSubmatch(block)[1]
	askPath := askInMain(t, root, "XR-005")
	waiting, _ := os.ReadFile(askPath)
	before := readTask(t, root, "XR-005")
	p := DecideParams{ID: "XR-005", Name: "сценарии", Snapshot: key, Answer: "сценарии 1, 9", Now: decideDay}
	if _, err := cmdDecide(root, p); err == nil {
		t.Fatal("плохая позиция принята")
	}
	after, _ := os.ReadFile(askPath)
	if string(waiting) != string(after) || readTask(t, root, "XR-005") != before {
		t.Fatal("отказ изменил ожидание или файл")
	}
	p.Answer = "плюс свой сценарий"
	result := decideRun(t, root, p)
	if askStands(askPath) {
		t.Fatal("принятый ответ оставил ожидание")
	}
	askInMain(t, root, "XR-005")
	before = readTask(t, root, "XR-005")
	if got := decideRun(t, root, p); got != result || !askStands(askPath) || readTask(t, root, "XR-005") != before {
		t.Fatal("повтор изменил результат, файл или новое ожидание")
	}
	block = chatRun(t, root, DecideParams{ID: "XR-005", Name: "сценарии", Chat: true}, &askDeps{}, botEnv)
	freshKey := regexp.MustCompile(`снимок сценариев: (sc1-[a-f0-9]{64})`).FindStringSubmatch(block)[1]
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "сценарии", Snapshot: freshKey, By: "человек", Text: "СЦ-2"})
	f, _ := taskform.FindFork(readTask(t, root, "XR-005"), "сценарии")
	if f.Answer != "СЦ-2" {
		t.Fatal("--by человек обошёл проверку набора")
	}
	askInMain(t, root, "XR-005")
	before = readTask(t, root, "XR-005")
	if _, err := cmdDecide(root, p); err == nil || !askStands(askPath) || readTask(t, root, "XR-005") != before {
		t.Fatal("устаревшая повторная доставка изменила запись или ожидание")
	}
}

func TestScenarioOpenShowsChoiceBeforeQuestions(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "доступ", Hint: "есть", Text: "есть доступ?"})
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "сценарии", Scenarios: true, Hint: "СЦ-1: путь задан", Opts: []string{"СЦ-1 (слово пользователя): выбрать"}, Given: "СЦ-1", Said: "берём выбор", Text: "что включаем?"})
	out := decideRun(t, root, DecideParams{ID: "XR-005", Open: true})
	if strings.Index(out, "«сценарии»") > strings.Index(out, "«доступ»") {
		t.Fatal("при продолжении вопросы предшествуют текущему составу")
	}
	before := readTask(t, root, "XR-005")
	_, err := cmdDecide(root, DecideParams{ID: "XR-005", Answer: "сценарии 1", Now: decideDay})
	if err == nil || !strings.Contains(err.Error(), "--snapshot") || readTask(t, root, "XR-005") != before {
		t.Fatal("повторный именованный ответ без снимка не отклонён")
	}
}

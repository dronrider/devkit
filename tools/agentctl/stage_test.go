package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// stageAt это момент отметки в тестах команды: часы в выводе сверяются с ним, и
// живое время сделало бы проверку плавающей.
var stageAt = time.Date(2026, 8, 15, 14, 30, 0, 0, time.Local)

// stageRoot готовит корень и уводит записи во временный дом: они лежат на уровне
// машины, и без подмены тест писал бы в живой ~/.devkit/runs.
func stageRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func TestCmdStageVerifyRecordsRunner(t *testing.T) {
	root := stageRoot(t)
	if _, err := cmdStage(root, "T-001", stage.Verify, "", "sonnet", stageAt); err != nil {
		t.Fatalf("отметка прогона: %v", err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), root, "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok || live.Kind != stage.Verify {
		t.Fatalf("живой этап не проверка: %+v", live)
	}
	name, ok := stage.VerifyRunner(live.Note)
	if !ok || name != "sonnet" {
		t.Fatalf("прогонявший не достался из записи %q: %q, %v", live.Note, name, ok)
	}
}

func TestCmdStageVerifyNeedsBy(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", stage.Verify, "", "", stageAt)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("проверка без --by прошла: %v", err)
	}
}

func TestCmdStageByOnlyForVerify(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", stage.WaitHuman, "", "sonnet", stageAt)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("--by у ожидания прошёл: %v", err)
	}
}

// TestCmdStageRefusesToolWrittenKinds: этапы работы ставят инструменты (хук
// спавна, shipctl), и ручная отметка отбивается с именем писателя. Иначе
// диспетчер по памяти клал бы вторую строку об одном ревью (DK-911).
func TestCmdStageRefusesToolWrittenKinds(t *testing.T) {
	root := stageRoot(t)
	cases := map[string]string{
		stage.Dev:    "exec-*",
		stage.Review: "review-*",
		stage.Proof:  "proofread",
		stage.Rework: "после ревью",
		stage.Merge:  "shipctl merge",
		stage.Deploy: "shipctl ship",
		stage.Setup:  "хук старта",
	}
	for kind, who := range cases {
		_, err := cmdStage(root, "T-001", kind, "", "", stageAt)
		if err == nil || !strings.Contains(err.Error(), who) {
			t.Fatalf("этап %s руками прошёл либо отказ не назвал писателя: %v", kind, err)
		}
	}
	if _, err := os.Stat(stage.Path(stage.Home(), stage.MainRoot(root), "T-001")); err == nil {
		t.Fatal("отбитый вид всё равно завёл запись")
	}
}

// TestCmdStageRefusesOldWords: слово прежнего словаря отбивается с подсказкой,
// каким ожиданием его писать.
func TestCmdStageRefusesOldWords(t *testing.T) {
	root := stageRoot(t)
	for _, kind := range []string{stage.Outside, stage.Ask} {
		_, err := cmdStage(root, "T-001", kind, "", "", stageAt)
		if err == nil || !strings.Contains(err.Error(), stage.WaitHuman) {
			t.Fatalf("старое слово %s принято либо отказ без подсказки: %v", kind, err)
		}
	}
}

func TestCmdStageOpensWait(t *testing.T) {
	root := stageRoot(t)
	out, err := cmdStage(root, "T-001", stage.WaitHuman, "ждём выбора между двумя раскладками", "", stageAt)
	if err != nil {
		t.Fatalf("отметка этапа: %v", err)
	}
	for _, want := range []string{"T-001", stage.WaitHuman, "14:30"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в ответе нет %q:\n%s", want, out)
		}
	}
	path := stage.Path(stage.Home(), stage.MainRoot(root), "T-001")
	if !strings.Contains(out, path) {
		t.Fatalf("ответ не называет запись, куда легла отметка:\n%s", out)
	}
	rec, err := stage.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok || live.Kind != stage.WaitHuman || !live.Start.Equal(stageAt) {
		t.Fatalf("этап записан не тем: %+v", live)
	}
	if live.Note != "ждём выбора между двумя раскладками" {
		t.Fatalf("текст записи потерялся: %q", live.Note)
	}
}

func TestCmdStageRejectsUnknownKind(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", "деплой", "", "", stageAt)
	if err == nil {
		t.Fatal("неизвестный вид деятельности принят командой")
	}
	// Отказ обязан назвать словарь: гадать, чем «деплой» отличается от
	// «выката», читателю нечем.
	for _, want := range []string{"деплой", stage.Deploy, stage.WaitQueue} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	if _, err := os.Stat(stage.Path(stage.Home(), stage.MainRoot(root), "T-001")); err == nil {
		t.Fatal("отбитый вид всё равно завёл запись")
	}
}

func TestCmdStageShowsLiveAndPack(t *testing.T) {
	root := stageRoot(t)
	home := stage.Home()
	main := stage.MainRoot(root)
	if err := stage.Open(home, main, "T-001", stage.Dev, "субагент opus/high", stageAt); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdStage(root, "T-001", stage.WaitHuman, "ждём ответа", "", stageAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := cmdStage(root, "T-001", "", "", "", stageAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := "stage: " + stage.WaitHuman + "\nsince: 2026-08-15T15:30:00\nnote: ждём ответа\n" +
		"до него в пакете:\n  " + stage.Dev + " с 2026-08-15T14:30:00"
	if out != want {
		t.Fatalf("вывод живого состояния разошёлся с ожидаемым\nжду:\n%s\nвижу:\n%s", want, out)
	}
	// Показ ничего не отмечает: иначе каждый взгляд на состояние добавлял бы
	// этап, и пакет распухал бы от одного чтения.
	rec, err := stage.Load(stage.Path(home, main, "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 2 {
		t.Fatalf("показ состояния тронул пакет: %+v", rec.Stages)
	}
}

// TestCmdStageShowsClosedPack: закрытый писателем этап живым не печатается, а
// стоит в пакете со своим концом.
func TestCmdStageShowsClosedPack(t *testing.T) {
	root := stageRoot(t)
	home := stage.Home()
	main := stage.MainRoot(root)
	if err := stage.Put(home, main, "T-001", stage.Stage{Kind: stage.Review, Start: stageAt, End: stageAt.Add(20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdStage(root, "T-001", "", "", "", stageAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := "stage: нет, последний этап закрыт писателем\nдо него в пакете:\n  " + stage.Review + " с 2026-08-15T14:30:00 до 2026-08-15T14:50:00"
	if out != want {
		t.Fatalf("вывод закрытого пакета\nжду:\n%s\nвижу:\n%s", want, out)
	}
}

func TestCmdStageShowsEmptyRecordInWords(t *testing.T) {
	root := stageRoot(t)
	out, err := cmdStage(root, "T-404", "", "", "", stageAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "этапов не отмечено") {
		t.Fatalf("пустая запись отвечает не словами:\n%s", out)
	}
	if !strings.Contains(out, "T-404") {
		t.Fatalf("в ответе нет задачи, о которой спрашивали:\n%s", out)
	}
}

// TestStageCommandArgs: разбор аргументов живёт в main, и из библиотечного
// вызова его не видно. Вид стоит вторым позиционным, --note где угодно, а лишний
// позиционный отбивается, а не выбрасывается молча.
func TestStageCommandArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := writeBoard(t)
	out, err := goRunAgent(t, root, "stage", "--note", "ждём ответа", "T-001", stage.WaitHuman)
	if err != nil {
		t.Fatalf("stage с флагом перед позиционными: %v\n%s", err, out)
	}
	if !strings.Contains(out, stage.WaitHuman) {
		t.Fatalf("вид деятельности не доехал до записи:\n%s", out)
	}
	rec, err := stage.Load(stage.Path(home, stage.MainRoot(root), "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok || live.Kind != stage.WaitHuman || live.Note != "ждём ответа" {
		t.Fatalf("команда записала не то: %+v", live)
	}
	if out, err := goRunAgent(t, root, "stage", "T-001", stage.WaitHuman, "лишнее"); err == nil {
		t.Fatalf("лишний позиционный проглочен молча:\n%s", out)
	}
}

// TestCmdStageShowsLiveUnderClosed (DK-911, замечание ревью): закрытая
// вычитка поверх живой разработки живого не гасит, показ печатает разработку
// живой, а вычитку в пакете со своим концом.
func TestCmdStageShowsLiveUnderClosed(t *testing.T) {
	root := stageRoot(t)
	home := stage.Home()
	main := stage.MainRoot(root)
	if err := stage.Open(home, main, "T-001", stage.Dev, "субагент opus/high", stageAt); err != nil {
		t.Fatal(err)
	}
	if err := stage.Put(home, main, "T-001", stage.Stage{Kind: stage.Proof, Start: stageAt.Add(10 * time.Minute), End: stageAt.Add(20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdStage(root, "T-001", "", "", "", stageAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := "stage: " + stage.Dev + "\nsince: 2026-08-15T14:30:00\nnote: субагент opus/high\n" +
		"до него в пакете:\n  " + stage.Proof + " с 2026-08-15T14:40:00 до 2026-08-15T14:50:00"
	if out != want {
		t.Fatalf("вывод живого под закрытым\nжду:\n%s\nвижу:\n%s", want, out)
	}
}

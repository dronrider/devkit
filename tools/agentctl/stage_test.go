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
	if _, err := cmdStage(root, "T-001", stage.Verify, "", "sonnet", 0, 0, stageAt); err != nil {
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
	_, err := cmdStage(root, "T-001", stage.Verify, "", "", 0, 0, stageAt)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("проверка без --by прошла: %v", err)
	}
}

func TestCmdStageByOnlyForVerify(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", stage.Dev, "", "sonnet", 0, 0, stageAt)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("--by у разработки прошёл: %v", err)
	}
}

// TestCmdStageReviewRecordsWork: --by, --turns и --minutes у ревью кладут
// ходы и минуты в запись, а VerifyRunner (тот же критерий, что у ворот
// проверки) не путает ревьювера с прогонявшим сценарий.
func TestCmdStageReviewRecordsWork(t *testing.T) {
	root := stageRoot(t)
	if _, err := cmdStage(root, "T-001", stage.Review, "", "sonnet", 44, 9, stageAt); err != nil {
		t.Fatalf("отметка ревью с ходами и минутами: %v", err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), root, "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok || live.Kind != stage.Review {
		t.Fatalf("живой этап не ревью: %+v", live)
	}
	turns, minutes, ok := stage.ParseWork(live.Note)
	if !ok || turns != 44 || minutes != 9 {
		t.Fatalf("ходы и минуты не достались из записи %q: %d, %d, %v", live.Note, turns, minutes, ok)
	}
	if _, ok := stage.VerifyRunner(live.Note); ok {
		t.Fatalf("ревью спутано с прогоном сценария: %q", live.Note)
	}
}

// TestCmdStageReviewWorkNeedsBoth: ходы и минуты только парой, поодиночке
// считать нечего.
func TestCmdStageReviewWorkNeedsBoth(t *testing.T) {
	root := stageRoot(t)
	if _, err := cmdStage(root, "T-001", stage.Review, "", "sonnet", 44, 0, stageAt); err == nil {
		t.Fatal("ходы без минут прошли")
	}
	if _, err := cmdStage(root, "T-001", stage.Review, "", "sonnet", 0, 9, stageAt); err == nil {
		t.Fatal("минуты без ходов прошли")
	}
}

// TestCmdStageReviewWorkNeedsBy: без модели работа обезличена, запись отбита.
func TestCmdStageReviewWorkNeedsBy(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", stage.Review, "", "", 44, 9, stageAt)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("ходы и минуты без --by прошли: %v", err)
	}
}

// TestCmdStageWorkOnlyForReview: другим видам ходы и минуты не идут, у них
// нет бюджета ревью, против которого их сверяют.
func TestCmdStageWorkOnlyForReview(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", stage.Verify, "", "sonnet", 44, 9, stageAt)
	if err == nil || !strings.Contains(err.Error(), stage.Review) {
		t.Fatalf("ходы и минуты у проверки прошли: %v", err)
	}
}

func TestCmdStageOpensStage(t *testing.T) {
	root := stageRoot(t)
	out, err := cmdStage(root, "T-001", stage.Ask, "ждём выбора между двумя раскладками", "", 0, 0, stageAt)
	if err != nil {
		t.Fatalf("отметка этапа: %v", err)
	}
	for _, want := range []string{"T-001", stage.Ask, "14:30"} {
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
	if !ok || live.Kind != stage.Ask || !live.Start.Equal(stageAt) {
		t.Fatalf("этап записан не тем: %+v", live)
	}
	if live.Note != "ждём выбора между двумя раскладками" {
		t.Fatalf("текст записи потерялся: %q", live.Note)
	}
}

func TestCmdStageRejectsUnknownKind(t *testing.T) {
	root := stageRoot(t)
	_, err := cmdStage(root, "T-001", "деплой", "", "", 0, 0, stageAt)
	if err == nil {
		t.Fatal("неизвестный вид деятельности принят командой")
	}
	// Отказ обязан назвать словарь: гадать, чем «деплой» отличается от
	// «разработки», читателю нечем.
	for _, want := range []string{"деплой", stage.Dev, stage.Outside} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	if _, err := os.Stat(stage.Path(stage.Home(), stage.MainRoot(root), "T-001")); err == nil {
		t.Fatal("отбитый вид всё равно завёл запись")
	}
}

func TestCmdStageAccumulatesPack(t *testing.T) {
	root := stageRoot(t)
	if _, err := cmdStage(root, "T-001", stage.Dev, "субагент opus/high", "", 0, 0, stageAt); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdStage(root, "T-001", stage.Review, "субагент sonnet/high", "", 0, 0, stageAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 2 {
		t.Fatalf("жду два этапа в пакете, вижу %d: %+v", len(rec.Stages), rec.Stages)
	}
	if rec.Stages[0].Kind != stage.Dev || rec.Stages[1].Kind != stage.Review {
		t.Fatalf("порядок этапов разошёлся с порядком вызовов: %+v", rec.Stages)
	}
}

func TestCmdStageShowsLiveAndPack(t *testing.T) {
	root := stageRoot(t)
	if _, err := cmdStage(root, "T-001", stage.Dev, "субагент opus/high", "", 0, 0, stageAt); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdStage(root, "T-001", stage.Ask, "ждём ответа", "", 0, 0, stageAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := cmdStage(root, "T-001", "", "", "", 0, 0, stageAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := "stage: " + stage.Ask + "\nsince: 2026-08-15T15:30:00\nnote: ждём ответа\n" +
		"до него в пакете:\n  " + stage.Dev + " с 2026-08-15T14:30:00"
	if out != want {
		t.Fatalf("вывод живого состояния разошёлся с ожидаемым\nжду:\n%s\nвижу:\n%s", want, out)
	}
	// Показ ничего не отмечает: иначе каждый взгляд на состояние добавлял бы
	// этап, и пакет распухал бы от одного чтения.
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 2 {
		t.Fatalf("показ состояния тронул пакет: %+v", rec.Stages)
	}
}

func TestCmdStageShowsEmptyRecordInWords(t *testing.T) {
	root := stageRoot(t)
	out, err := cmdStage(root, "T-404", "", "", "", 0, 0, stageAt)
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
	out, err := goRunAgent(t, root, "stage", "--note", "ждём ответа", "T-001", stage.Ask)
	if err != nil {
		t.Fatalf("stage с флагом перед позиционными: %v\n%s", err, out)
	}
	if !strings.Contains(out, stage.Ask) {
		t.Fatalf("вид деятельности не доехал до записи:\n%s", out)
	}
	rec, err := stage.Load(stage.Path(home, stage.MainRoot(root), "T-001"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok || live.Kind != stage.Ask || live.Note != "ждём ответа" {
		t.Fatalf("команда записала не то: %+v", live)
	}
	if out, err := goRunAgent(t, root, "stage", "T-001", stage.Ask, "лишнее"); err == nil {
		t.Fatalf("лишний позиционный проглочен молча:\n%s", out)
	}
}

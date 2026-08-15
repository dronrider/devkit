package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// stageAt это момент начала этапа в тестах: одно значение на весь файл, чтобы
// сравнивать секунды, а не пересчитывать их в каждом месте.
var stageAt = time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)

func TestRowStageNeedsLiveSession(t *testing.T) {
	stages := map[string]stageMark{
		"XR-001": {Kind: stage.Dev, Since: stageAt.Unix()},
		"XR-002": {Kind: stage.Outside, Since: stageAt.Unix()},
	}
	cases := []struct {
		name, id, run, want string
	}{
		{"живая работа несёт вид деятельности", "XR-001", "tmux", stage.Dev},
		{"оборванный этап за работу не выдаётся", "XR-001", runGone, ""},
		{"строка без работы вида не получает", "XR-001", "", ""},
		{"ожидание снаружи живой сессии не требует", "XR-002", runGone, stage.Outside},
		{"записи нет, значит и вида нет", "XR-404", "tmux", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, since := rowStage(stages, c.run, c.id)
			if kind != c.want {
				t.Fatalf("вид деятельности %q, жду %q", kind, c.want)
			}
			if c.want == "" && since != 0 {
				t.Fatalf("вида нет, а время начала пришло: %d", since)
			}
			if c.want != "" && since != stageAt.Unix() {
				t.Fatalf("время начала %d, жду %d", since, stageAt.Unix())
			}
		})
	}
}

func TestLiveStagesReadsRecordsOfProject(t *testing.T) {
	home, proj := t.TempDir(), "/projects/demo"
	if err := stage.Open(home, proj, "XR-001", stage.Dev, "субагент opus/high", stageAt); err != nil {
		t.Fatal(err)
	}
	// Живой этап это последний: ревью пришло на смену разработке.
	if err := stage.Open(home, proj, "XR-001", stage.Review, "субагент sonnet/high", stageAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := stage.Open(home, "/projects/other", "XR-009", stage.Dev, "чужой проект", stageAt); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: &Config{Home: home}}
	got := s.liveStages(proj)
	if len(got) != 1 {
		t.Fatalf("жду одну запись своего проекта, вижу %d: %+v", len(got), got)
	}
	mark := got["XR-001"]
	if mark.Kind != stage.Review {
		t.Fatalf("живой этап %q, жду %q", mark.Kind, stage.Review)
	}
	if mark.Since != stageAt.Add(time.Hour).Unix() {
		t.Fatalf("время начала %d, жду %d", mark.Since, stageAt.Add(time.Hour).Unix())
	}
}

func TestBoardRunsCarriesStage(t *testing.T) {
	raw := json.RawMessage(`{"prefix":"XR","sections":[{"key":"in-progress","rows":[
		{"id":"XR-001","title":"Живая","sect":"in-progress"},
		{"id":"XR-002","title":"Оборванная","sect":"in-progress"}]}]}`)
	works := []Work{{ID: "XR-001", Kind: "task", Via: "tmux"}}
	stages := map[string]stageMark{
		"XR-001": {Kind: stage.Dev, Since: stageAt.Unix()},
		"XR-002": {Kind: stage.Dev, Since: stageAt.Unix()},
	}
	var doc struct {
		Sections []struct {
			Rows []boardRow `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(boardRuns(raw, works, stages), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]boardRow{}
	for _, sec := range doc.Sections {
		for _, row := range sec.Rows {
			rows[row.ID] = row
		}
	}
	live := rows["XR-001"]
	if live.Run != "tmux" || live.Stage != stage.Dev || live.StageSince != stageAt.Unix() {
		t.Fatalf("живая строка: run=%q stage=%q since=%d", live.Run, live.Stage, live.StageSince)
	}
	gone := rows["XR-002"]
	if gone.Run != runGone {
		t.Fatalf("строка без сессии помечена %q, жду %q", gone.Run, runGone)
	}
	if gone.Stage != "" || gone.StageSince != 0 {
		t.Fatalf("оборванный этап уехал на экран: stage=%q since=%d", gone.Stage, gone.StageSince)
	}
}

func TestBoardRunsWithoutStagesKeepsRun(t *testing.T) {
	raw := json.RawMessage(`{"prefix":"XR","sections":[{"key":"in-progress","rows":[
		{"id":"XR-001","title":"Живая","sect":"in-progress"}]}]}`)
	out := boardRuns(raw, []Work{{ID: "XR-001", Kind: "task", Via: "tmux"}}, nil)
	var doc struct {
		Sections []struct {
			Rows []boardRow `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	row := doc.Sections[0].Rows[0]
	if row.Run != "tmux" {
		t.Fatalf("признак работы потерялся: %q", row.Run)
	}
	if row.Stage != "" {
		t.Fatalf("без единой записи строка получила вид деятельности %q", row.Stage)
	}
}

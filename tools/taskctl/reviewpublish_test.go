package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeReviewConf(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.conf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubAPI подменяет исполнитель подпроцесса и паузу: живого трекера прогону не
// нужно, а десятки секунд паузы ждать тем более. Возвращает журнал команд и
// счётчик пауз, по ним и судится публикация.
func stubAPI(t *testing.T) (*[]string, *int) {
	t.Helper()
	var calls []string
	pauses := 0
	oldRun, oldSleep := runPublish, sleepPublish
	runPublish = func(script string) ([]byte, error) {
		calls = append(calls, script)
		if strings.Contains(script, "--request POST") || strings.HasPrefix(script, "gh api") {
			return []byte(fmt.Sprintf(`{"id":"disc%d","notes":[]}`, len(calls))), nil
		}
		return []byte(`{"diff_refs":{"base_sha":"b1","start_sha":"s1","head_sha":"h1"}}`), nil
	}
	sleepPublish = func(time.Duration) { pauses++ }
	t.Cleanup(func() { runPublish, sleepPublish = oldRun, oldSleep })
	return &calls, &pauses
}

func draftStates(t *testing.T, root, id string) []string {
	t.Helper()
	d, err := loadReviewDraft(reviewDraftAbs(root, id), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range d.Blocks {
		s := b.State
		if b.Thread != "" {
			s += " " + b.Thread
		}
		out = append(out, s)
	}
	return out
}

// twoDrafts кладёт два замечания: первое с привязкой к строке диффа, второе
// итогом уровня.
func twoDrafts(t *testing.T, root string) {
	t.Helper()
	if _, err := cmdReviewDraft(root, "XR-005", "ворота merge не видят раздел",
		reviewDraftParams{File: "tools/shipctl/ops.go", Line: 214}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewDraft(root, "XR-005", "проверен живой путь по DoD",
		reviewDraftParams{Label: "итог"}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

// TestPublishConfirmWithoutApproved: умолчание confirm без одобренных блоков
// отказывает и черновики не трогает. Иначе замечания уезжали бы в чужой MR
// мимо человека, ради которого режим и заведён.
func TestPublishConfirmWithoutApproved(t *testing.T) {
	root := setupDraft(t)
	calls, _ := stubAPI(t)
	twoDrafts(t, root)
	_, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "нет одобренных замечаний") {
		t.Fatalf("отказ confirm: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("в трекер ушли команды при отказе: %v", *calls)
	}
	if got := draftStates(t, root, "XR-005"); got[0] != reviewStateNew || got[1] != reviewStateNew {
		t.Fatalf("состояния черновиков тронуты: %v", got)
	}
}

// TestPublishConfirmApproved: уходит ровно одобренное, id треда из ответа API
// ложится в блок, соседний черновик остаётся на месте.
func TestPublishConfirmApproved(t *testing.T) {
	root := setupDraft(t)
	calls, pauses := stubAPI(t)
	twoDrafts(t, root)
	if _, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "тред disc2") {
		t.Fatalf("сообщение без id треда: %q", msg)
	}
	got := draftStates(t, root, "XR-005")
	if got[0] != reviewStatePublished+" disc2" || got[1] != reviewStateNew {
		t.Fatalf("состояния после публикации: %v", got)
	}
	if *pauses != 0 {
		t.Fatalf("одна публикация, а пауз %d", *pauses)
	}
	// Первым запросом идут три sha позиции, вторым сам тред: без diff_refs
	// GitLab тред на строку диффа не принимает.
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "/merge_requests/42") ||
		!strings.Contains((*calls)[1], "/discussions") {
		t.Fatalf("команды трекера: %v", *calls)
	}
	post := (*calls)[1]
	for _, want := range []string{
		`"PRIVATE-TOKEN: $GITLAB_TOKEN"`,
		"'body=issue: ворота merge не видят раздел'",
		"'position[new_path]=tools/shipctl/ops.go'",
		"'position[new_line]=214'",
		"'position[head_sha]=h1'",
	} {
		if !strings.Contains(post, want) {
			t.Fatalf("в команде публикации нет %q:\n%s", want, post)
		}
	}
}

// TestPublishAutoAll: при publish = auto черновики одобряются сами, уходят все,
// и между публикациями держится пауза.
func TestPublishAutoAll(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	calls, pauses := stubAPI(t)
	twoDrafts(t, root)
	msg, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "черновиков одобрено 2") || !strings.Contains(msg, "ушло замечаний 2") {
		t.Fatalf("сообщение: %q", msg)
	}
	for i, st := range draftStates(t, root, "XR-005") {
		if !strings.HasPrefix(st, reviewStatePublished+" disc") {
			t.Fatalf("блок %d не опубликован: %s", i+1, st)
		}
	}
	if *pauses != 1 {
		t.Fatalf("пауз между двумя публикациями %d, ждём 1", *pauses)
	}
	// Итоговый комментарий уровня идёт тредом без позиции и без метки.
	last := (*calls)[len(*calls)-1]
	if strings.Contains(last, "position[") || !strings.Contains(last, "'body=проверен живой путь по DoD'") {
		t.Fatalf("итоговый комментарий ушёл не тем запросом:\n%s", last)
	}
}

// TestPublishTwiceKeepsThreads: опубликованное второй раз не уходит, дубля в
// чужом MR не будет.
func TestPublishTwiceKeepsThreads(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	calls, _ := stubAPI(t)
	twoDrafts(t, root)
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	before := append([]string(nil), *calls...)
	firstRun := draftStates(t, root, "XR-005")
	_, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "публиковать нечего") {
		t.Fatalf("повторная публикация: %v", err)
	}
	if len(*calls) != len(before) {
		t.Fatalf("повтор сходил в трекер: было %d команд, стало %d", len(before), len(*calls))
	}
	for i, st := range draftStates(t, root, "XR-005") {
		if st != firstRun[i] {
			t.Fatalf("блок %d сменил тред при повторе: %s против %s", i+1, st, firstRun[i])
		}
	}
}

// TestPublishTrackerByLink: трекер и имя переменной с токеном берутся из
// ссылки на MR, а не из конфига машины.
func TestPublishTrackerByLink(t *testing.T) {
	cases := map[string]struct{ kind, project, num, tokenEnv string }{
		"https://gl.example.com/group/sub/proj/-/merge_requests/42": {"gitlab", "group/sub/proj", "42", "GITLAB_TOKEN"},
		"https://github.com/owner/repo/pull/7":                      {"github", "owner/repo", "7", "GITHUB_TOKEN"},
		"https://git.example.com/owner/repo/pulls/3/files":          {"gitea", "owner/repo", "3", "GITEA_TOKEN"},
	}
	for link, want := range cases {
		got, err := trackerFromMR(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if got.Kind != want.kind || got.Project != want.project || got.Number != want.num || got.TokenEnv != want.tokenEnv {
			t.Fatalf("%s разобран как %+v", link, got)
		}
	}
	if _, err := trackerFromMR("https://example.com/owner/repo/issues/3"); err == nil {
		t.Fatal("ссылка не на MR должна отбиваться")
	}
}

// TestPublishScriptShape: у GitHub и Gitea тред на строку диффа привязывается
// к sha ревью из шапки, а токен едет именем переменной.
func TestPublishScriptShape(t *testing.T) {
	bl := reviewBlock{File: "tools/x.go", Line: "12", Label: reviewLabelIssue, State: reviewStateApproved, Text: "поправь"}
	gh, err := trackerFromMR("https://github.com/owner/repo/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	script, err := publishScript(gh, bl, "a1b2c3d", diffRefs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repos/owner/repo/pulls/7/comments", "'commit_id=a1b2c3d'", "'line=12'", "'body=issue: поправь'"} {
		if !strings.Contains(script, want) {
			t.Fatalf("GitHub: нет %q в %s", want, script)
		}
	}
	gt, err := trackerFromMR("https://git.example.com/owner/repo/pulls/3")
	if err != nil {
		t.Fatal(err)
	}
	script, err = publishScript(gt, bl, "a1b2c3d", diffRefs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Authorization: token $GITEA_TOKEN"`, `"new_position":12`, `"commit_id":"a1b2c3d"`} {
		if !strings.Contains(script, want) {
			t.Fatalf("Gitea: нет %q в %s", want, script)
		}
	}
}

// TestPublishNoMR: без ссылки в шапке публиковать некуда, и команда говорит,
// что дописать.
func TestPublishNoMR(t *testing.T) {
	root := setup(t)
	stubAPI(t)
	if _, err := cmdReviewDraft(root, "XR-005", "текст", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "нет ссылки на MR") {
		t.Fatalf("отказ без MR: %v", err)
	}
}

// TestReviewConfKeys: умолчание confirm и вилка паузы, опечатка в режиме это
// отказ, а не тихая публикация.
func TestReviewConfKeys(t *testing.T) {
	root := setup(t)
	c, err := loadReviewConf(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Publish != publishConfirm || c.PauseMin != defaultPauseMin || c.PauseMax != defaultPauseMax {
		t.Fatalf("умолчания конфига: %+v", c)
	}
	writeReviewConf(t, root, "level1 = 5 минут, 20 ходов\npublish = auto\npause = 3-9\n")
	c, err = loadReviewConf(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Publish != publishAuto || c.PauseMin != 3*time.Second || c.PauseMax != 9*time.Second {
		t.Fatalf("конфиг разобран как %+v", c)
	}
	for i := 0; i < 20; i++ {
		if d := c.pause(); d < 3*time.Second || d >= 9*time.Second {
			t.Fatalf("пауза %v вне вилки", d)
		}
	}
	writeReviewConf(t, root, "publish = ага\n")
	if _, err := loadReviewConf(root); err == nil || !strings.Contains(err.Error(), "жду confirm или auto") {
		t.Fatalf("опечатка режима: %v", err)
	}
	writeReviewConf(t, root, "pause = скоро\n")
	if _, err := loadReviewConf(root); err == nil || !strings.Contains(err.Error(), "pause") {
		t.Fatalf("опечатка паузы: %v", err)
	}
}

// TestPublishFailureKeepsRest: отказ API на середине пачки оставляет уже
// опубликованное записанным, а не теряет id тредов.
func TestPublishFailureKeepsRest(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	twoDrafts(t, root)
	n := 0
	old := runPublish
	runPublish = func(script string) ([]byte, error) {
		n++
		switch {
		case strings.Contains(script, "/discussions"):
			if n > 2 {
				return nil, fmt.Errorf("HTTP 403")
			}
			return []byte(`{"id":"disc1"}`), nil
		default:
			return []byte(`{"diff_refs":{"base_sha":"b1","start_sha":"s1","head_sha":"h1"}}`), nil
		}
	}
	oldSleep := sleepPublish
	sleepPublish = func(time.Duration) {}
	t.Cleanup(func() { runPublish, sleepPublish = old, oldSleep })
	_, err := cmdReviewPublish(root, "XR-005", CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "не ушло в MR") {
		t.Fatalf("отказ API: %v", err)
	}
	got := draftStates(t, root, "XR-005")
	if got[0] != reviewStatePublished+" disc1" || got[1] != reviewStateApproved {
		t.Fatalf("после отказа состояния: %v", got)
	}
}

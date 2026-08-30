package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// runTaskctl гоняет собранный бинарь по синтетической доске: разбор аргументов
// живёт в main и на отказе выходит из процесса, из теста функцией его не позвать.
func runTaskctl(bin, dir string, args ...string) (string, error) {
	out, err := exec.Command(bin, append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

// today это дата, которую команды разбора пишут в файл черновика.
func today() string { return time.Now().Format(draftDateLayout) }

// newDraft заводит черновик на синтетической доске и возвращает его ID.
func newDraft(t *testing.T, root, text string) string {
	t.Helper()
	msg, err := cmdDraft(root, text, "mid", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(msg, ":")
	return id
}

func draftBody(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(draftFile(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDraftDeferWritesMark: пометка живёт разделом «Грумминг» в самом файле
// черновика, повторный вызов причину заменяет, а не копит вторую строку.
func TestDraftDeferWritesMark(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы\n\nхук берёт адрес из окружения")

	msg, err := cmdDraftDefer(root, id, "ждём повторного случая с git status", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "отложен "+today()) {
		t.Fatalf("сообщение команды: %q", msg)
	}
	body := draftBody(t, root, id)
	want := fmt.Sprintf("\n%s\n\n- %s, отложен: ждём повторного случая с git status\n", draftGroomHeading, today())
	if !strings.HasSuffix(body, want) {
		t.Fatalf("файл черновика:\n%s\nждал в хвосте:\n%s", body, want)
	}
	if !strings.Contains(body, "## Черновик\n\n### Ситуация\n\nхук берёт адрес из окружения") {
		t.Fatalf("пометка съела текст черновика:\n%s", body)
	}

	if _, err := cmdDraftDefer(root, id, "ждём ответа смежника", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body = draftBody(t, root, id)
	if n := strings.Count(body, ", отложен: "); n != 1 {
		t.Fatalf("повторный defer оставил %d строк пометки:\n%s", n, body)
	}
	if !strings.Contains(body, "ждём ответа смежника") || strings.Contains(body, "git status") {
		t.Fatalf("причина не заменилась:\n%s", body)
	}
	if n := strings.Count(body, draftGroomHeading); n != 1 {
		t.Fatalf("заголовков раздела %d:\n%s", n, body)
	}

	if _, err := cmdDraftDefer(root, id, "", false, CommitOpts{}); err == nil {
		t.Fatal("defer без причины должен отказывать")
	}
	if _, err := cmdDraftDefer(root, "XR-404", "причина", false, CommitOpts{}); err == nil {
		t.Fatal("defer по чужому ID должен отказывать")
	}
}

// TestDraftDeferCommitsOnlyDraft: коммит забирает файл черновика и ничего
// больше, доска пометкой не двигается.
func TestDraftDeferCommitsOnlyDraft(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "идея на потом")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	msg, err := cmdDraftDefer(root, id, "ждём релиза смежника", false, CommitOpts{Msg: "docs(tasks): " + id + " отложен"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ", коммит ") {
		t.Fatalf("сообщение без хеша коммита: %q", msg)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != draftRel(id) {
		t.Fatalf("в коммите не только черновик: %q", files)
	}
}

// TestDraftDeferClear: --clear снимает раздел целиком, вместе с заголовком, и
// на черновике без пометки не падает.
func TestDraftDeferClear(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы")
	before := draftBody(t, root, id)

	msg, err := cmdDraftDefer(root, id, "", true, CommitOpts{})
	if err != nil {
		t.Fatalf("--clear без пометки не должен падать: %v", err)
	}
	if !strings.Contains(msg, "пометки об отложенном не было") {
		t.Fatalf("сообщение: %q", msg)
	}
	if body := draftBody(t, root, id); body != before {
		t.Fatalf("холостой --clear тронул файл:\n%s", body)
	}

	if _, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftDefer(root, id, "", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body := draftBody(t, root, id)
	if strings.Contains(body, draftGroomHeading) || strings.Contains(body, "отложен") {
		t.Fatalf("--clear оставил след раздела:\n%s", body)
	}
	if !strings.Contains(body, "уведомитель шумит из песочницы") {
		t.Fatalf("--clear унёс текст черновика:\n%s", body)
	}
	if _, err := cmdDraftDefer(root, id, "причина", true, CommitOpts{}); err == nil {
		t.Fatal("--clear с причиной должен отказывать")
	}
}

// TestDraftListShowsDeferMark: заход по накопителю начинается с draft list, и
// без пометки в выводе процедура открывала бы все файлы, чтобы узнать, кого уже
// разбирали.
func TestDraftListShowsDeferMark(t *testing.T) {
	root := setup(t)
	first := newDraft(t, root, "первая идея")
	second := newDraft(t, root, "вторая идея")
	ageDraft(t, root, first, 3)
	if _, err := cmdDraftDefer(root, first, "ждём повторного случая", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}

	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := fmt.Sprintf("%s (3 дня, средний, отложен %s): первая идея", first, today())
	if !strings.Contains(out, wantFirst) {
		t.Fatalf("draft list:\n%s\nждал строку: %s", out, wantFirst)
	}
	if !strings.Contains(out, second+" (сегодня, средний): вторая идея") {
		t.Fatalf("неотложенный черновик печатается иначе, чем раньше:\n%s", out)
	}

	list, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, fmt.Sprintf("%s (сегодня, средний), %s (3 дня, средний, отложен)", second, first)) {
		t.Fatalf("хвост list:\n%s", list)
	}
}

// TestDraftAgeSurvivesDefer: возраст считается по строке «записан», а не по
// времени правки, иначе первая же пометка делала бы трёхдневный черновик
// сегодняшним, то есть ломала бы ровно тот счётчик, ради которого заводится.
func TestDraftAgeSurvivesDefer(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "старая идея")
	ageDraft(t, root, id, 5)

	if _, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (5 дней, средний, отложен") {
		t.Fatalf("возраст сбит пометкой:\n%s", out)
	}
}

// TestDraftPrioMark: метка уровня разбора живёт строкой в шапке файла, рядом
// со строкой «записан», повторная простановка уровень меняет, а --clear
// снимает и на черновике без метки не падает.
func TestDraftPrioMark(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы\n\nхук берёт адрес из окружения")

	msg, err := cmdDraftPrio(root, id, "high", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "приоритет разбора высокий") {
		t.Fatalf("сообщение команды: %q", msg)
	}
	body := draftBody(t, root, id)
	writtenAt := strings.Index(body, draftWrittenPrefix+today())
	prioAt := strings.Index(body, "приоритет: высокий")
	if writtenAt < 0 || prioAt < writtenAt {
		t.Fatalf("метка стоит не рядом со строкой «записан»:\n%s", body)
	}
	if !strings.Contains(body, "## Черновик\n\n### Ситуация\n\nхук берёт адрес из окружения") {
		t.Fatalf("метка съела текст черновика:\n%s", body)
	}

	if _, err := cmdDraftPrio(root, id, "mid", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body = draftBody(t, root, id)
	if n := strings.Count(body, "приоритет: "); n != 1 {
		t.Fatalf("повторный prio оставил %d строк метки:\n%s", n, body)
	}
	if !strings.Contains(body, "приоритет: средний") || strings.Contains(body, "высокий") {
		t.Fatalf("уровень не заменился:\n%s", body)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (сегодня, средний): уведомитель шумит из песочницы") {
		t.Fatalf("draft list молчит про метку:\n%s", out)
	}

	msg, err = cmdDraftPrio(root, id, "", true, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "метка разбора снята") {
		t.Fatalf("сообщение --clear: %q", msg)
	}
	if body := draftBody(t, root, id); strings.Contains(body, "приоритет:") {
		t.Fatalf("--clear оставил метку:\n%s", body)
	}
	msg, err = cmdDraftPrio(root, id, "", true, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "метки разбора не было") {
		t.Fatalf("повторный --clear молчит: %q", msg)
	}

	if _, err := cmdDraftPrio(root, id, "urgent", false, CommitOpts{}); err == nil {
		t.Fatal("уровень не из шкалы должен отказывать")
	}
	if _, err := cmdDraftPrio(root, id, "high", true, CommitOpts{}); err == nil {
		t.Fatal("--clear с уровнем должен отказывать")
	}
	if _, err := cmdDraftPrio(root, id, "", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "жду уровень: taskctl draft prio "+id) {
		t.Fatalf("отказ без уровня не подсказывает форму команды: %v", err)
	}
	if _, err := cmdDraftPrio(root, "XR-404", "high", false, CommitOpts{}); err == nil {
		t.Fatal("prio по чужому ID должен отказывать")
	}
}

// TestDraftPrioSortsList: один порядок для печати, json и хвоста list: high,
// mid, low, немаркированные, отложенные, внутри уровня по возрастанию ID.
func TestDraftPrioSortsList(t *testing.T) {
	root := setup(t)
	low := newDraft(t, root, "низкий уровень")
	// Немаркированный черновик теперь заводится только снятием метки: запись
	// без уровня отбивается, а группа в сортировке остаётся ради тех, что
	// записаны до DK-520.
	plain := newDraft(t, root, "немаркированная идея")
	first := newDraft(t, root, "первый высокий")
	second := newDraft(t, root, "второй высокий")
	mid := newDraft(t, root, "средний уровень")
	wait := newDraft(t, root, "отложенная идея")
	for id, level := range map[string]string{low: "low", first: "high", second: "high", mid: "mid"} {
		if _, err := cmdDraftPrio(root, id, level, false, CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cmdDraftPrio(root, plain, "", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftDefer(root, wait, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}

	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ln := range strings.Split(out, "\n") {
		id, _, _ := strings.Cut(ln, " ")
		order = append(order, id)
	}
	want := []string{first, second, mid, low, plain, wait}
	if !slices.Equal(order, want) {
		t.Fatalf("порядок накопителя %v, жду %v:\n%s", order, want, out)
	}
	if !strings.Contains(out, first+" (сегодня, высокий): первый высокий") {
		t.Fatalf("печать без русского слова уровня:\n%s", out)
	}

	list, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	tail := fmt.Sprintf("%s (сегодня, высокий), %s (сегодня, высокий), %s (сегодня, средний), "+
		"%s (сегодня, низкий), %s (сегодня), %s (сегодня, средний, отложен)", first, second, mid, low, plain, wait)
	if !strings.Contains(list, tail) {
		t.Fatalf("хвост list:\n%s\nждал: %s", list, tail)
	}

	jsonOut, err := cmdDraftListJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Drafts []jsonDraft `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("накопитель не разобрался: %v\n%s", err, jsonOut)
	}
	for i, id := range want {
		if got.Drafts[i].ID != id {
			t.Fatalf("json-порядок разошёлся с печатью: [%d] %s, жду %s", i, got.Drafts[i].ID, id)
		}
	}
	if got.Drafts[0].Prio != "high" || got.Drafts[3].Prio != "low" {
		t.Fatalf("поле prio не отдаёт уровень: %+v %+v", got.Drafts[0], got.Drafts[3])
	}
	if got.Drafts[4].Prio != "" {
		t.Fatalf("у немаркированного оказалось имя уровня: %+v", got.Drafts[4])
	}
}

// TestDraftPrioAndDefer: откладывание метку не трогает, отложенный стоит после
// всех независимо от метки, а снятая пометка возвращает черновик на место по
// метке.
func TestDraftPrioAndDefer(t *testing.T) {
	root := setup(t)
	marked := newDraft(t, root, "важная идея")
	plain := newDraft(t, root, "прочая идея")
	if _, err := cmdDraftPrio(root, marked, "high", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftDefer(root, marked, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body := draftBody(t, root, marked)
	if !strings.Contains(body, "приоритет: высокий") || !strings.Contains(body, ", отложен: ждём повода") {
		t.Fatalf("defer съел метку или метка съела пометку:\n%s", body)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, marked+" (сегодня, высокий, отложен "+today()+")") {
		t.Fatalf("печать не собрала обе пометки:\n%s", out)
	}
	if strings.Index(out, marked) < strings.Index(out, plain) {
		t.Fatalf("отложенный с меткой стоит раньше немаркированного:\n%s", out)
	}

	if _, err := cmdDraftDefer(root, marked, "", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err = cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(out, marked) > strings.Index(out, plain) {
		t.Fatalf("после снятия пометки метка не вернула черновик наверх:\n%s", out)
	}
}

// TestDraftPrioEnsureWritten: первой записи в старый черновик строка «записан»
// ставится из времени правки до самой записи, как у defer, иначе пометка
// сбивала бы ровно тот возраст, ради которого черновик показывают.
func TestDraftPrioEnsureWritten(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "черновик старого формата")
	body := "# " + id + ": черновик старого формата\n\n## Черновик\n\nчерновик старого формата\n"
	if err := os.WriteFile(draftFile(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -4)
	if err := os.Chtimes(draftFile(root, id), old, old); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdDraftPrio(root, id, "low", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "дата записи проставлена ("+old.Format(draftDateLayout)+")") {
		t.Fatalf("команда молчит про проставленную дату: %q", msg)
	}
	got := draftBody(t, root, id)
	if !strings.Contains(got, draftWrittenPrefix+old.Format(draftDateLayout)+"\nприоритет: низкий") {
		t.Fatalf("метка встала не рядом с проставленной строкой «записан»:\n%s", got)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (4 дня, низкий)") {
		t.Fatalf("возраст сбился меткой:\n%s", out)
	}
}

// TestDraftPrioBodyLineIgnored: строка «приоритет:» в теле идеи это текст, а
// не метка, и правка метки её не трогает.
func TestDraftPrioBodyLineIgnored(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "идея с строкой в теле")
	body := "# " + id + ": идея с строкой в теле\n\n" + draftWrittenPrefix + today() +
		"\n\n## Черновик\n\nприоритет: высокий\nтекст идеи\n"
	if err := os.WriteFile(draftFile(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := findDraft(drafts, id); d == nil || d.Prio != "" {
		t.Fatalf("строка из тела легла меткой: %+v", d)
	}
	if _, err := cmdDraftPrio(root, id, "mid", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := draftBody(t, root, id)
	if !strings.Contains(got, draftWrittenPrefix+today()+"\nприоритет: средний") {
		t.Fatalf("метка не встала в шапку:\n%s", got)
	}
	if !strings.Contains(got, "## Черновик\n\nприоритет: высокий\n") {
		t.Fatalf("команда тронула строку в теле идеи:\n%s", got)
	}
	drafts, err = loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := findDraft(drafts, id); d == nil || d.Prio != "mid" {
		t.Fatalf("меткой легла строка из тела: %+v", d)
	}
}

// TestDraftPrioWrittenInBody: строка «записан ...» в теле идеи это текст, а не
// дата записи шапки: точка вставки метки ищется только до первого «## », и
// строка тела не мешает ни проставить дату, ни поставить метку в шапку.
func TestDraftPrioWrittenInBody(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "идея со строкой записан в теле")
	body := "# " + id + ": идея со строкой записан в теле\n\n## Черновик\n\n" +
		draftWrittenPrefix + "разговор с смежниками во вторник\nтекст идеи\n"
	if err := os.WriteFile(draftFile(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdDraftPrio(root, id, "high", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "дата записи проставлена") {
		t.Fatalf("строка тела скрыла отсутствие даты в шапке: %q", msg)
	}
	got := draftBody(t, root, id)
	head := got[:strings.Index(got, "## ")]
	if !strings.Contains(head, draftWrittenPrefix+today()+"\nприоритет: высокий") {
		t.Fatalf("метка не встала в шапку рядом с проставленной датой:\n%s", got)
	}
	if !strings.Contains(got, draftWrittenPrefix+"разговор с смежниками во вторник\n") {
		t.Fatalf("команда тронула строку в теле идеи:\n%s", got)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := findDraft(drafts, id); d == nil || d.Prio != "high" {
		t.Fatalf("список читает черновик немаркированным: %+v", d)
	}
}

// TestAddPromotionDropsPrio: метка не переживает черновик, перенос add --id
// выкидывает строку метки из файла задачи, потому что метаданные там не
// дублируются.
func TestAddPromotionDropsPrio(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "идея с уровнем", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftPrio(root, "XR-008", "high", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	if _, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Оформленная", Type: "task", Rank: "25+4+2+0+2", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "приоритет:") {
		t.Fatalf("метка пережила перенос:\n%s", data)
	}
	if !strings.Contains(string(data), "идея с уровнем") {
		t.Fatalf("текст черновика потерян:\n%s", data)
	}
}

// TestDraftAttachDropsPrio: приписка выкидывает строку метки из раздела
// «Из черновика» тем же правилом, что и дата записи.
func TestDraftAttachDropsPrio(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "своя репродукция через метку")
	if _, err := cmdDraftPrio(root, id, "mid", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftAttach(root, id, "XR-002", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-002.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "приоритет:") {
		t.Fatalf("метка пережила приписку:\n%s", data)
	}
	if !strings.Contains(string(data), "своя репродукция через метку") {
		t.Fatalf("текст черновика потерян:\n%s", data)
	}
}

// TestDraftAgeFallsBackToModTime: черновик, записанный до появления строки
// «записан», считается по времени правки, а первая же запись в него эту строку
// проставляет, пока время правки ещё то самое.
func TestDraftAgeFallsBackToModTime(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "черновик старого формата")
	// Файл без строки «записан»: ровно то, что лежит в живом накопителе.
	body := "# " + id + ": черновик старого формата\n\n## Черновик\n\nчерновик старого формата\n"
	if err := os.WriteFile(draftFile(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -4)
	if err := os.Chtimes(draftFile(root, id), old, old); err != nil {
		t.Fatal(err)
	}

	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (4 дня): черновик старого формата") {
		t.Fatalf("фолбэк на время правки не сработал:\n%s", out)
	}

	msg, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "дата записи проставлена ("+old.Format(draftDateLayout)+")") {
		t.Fatalf("команда молчит про проставленную дату: %q", msg)
	}
	if got := draftBody(t, root, id); !strings.Contains(got, draftWrittenPrefix+old.Format(draftDateLayout)) {
		t.Fatalf("строка «записан» не проставлена:\n%s", got)
	}
	out, err = cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (4 дня, отложен") {
		t.Fatalf("после пометки возраст сбился:\n%s", out)
	}
}

// TestDraftAttachToTaskWithFile: у задачи с готовым файлом путей два, доска не
// трогается вовсе.
func TestDraftAttachToTaskWithFile(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "своя репродукция через -m")
	if _, err := cmdDraftDefer(root, id, "ждём разбора", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")
	boardBefore, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}

	msg, err := cmdDraftAttach(root, id, "XR-002", CommitOpts{Msg: fmt.Sprintf("docs(tasks): %s уехал в XR-002", id)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "приписан к XR-002") {
		t.Fatalf("сообщение: %q", msg)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf("## Из черновика %s «своя репродукция через -m» (%s%s)", id, draftWrittenPrefix, today())
	for _, want := range []string{head, "### Черновик", "#### Ситуация", "отложен: ждём разбора"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("в файле задачи нет %q:\n%s", want, data)
		}
	}
	// Дата записи живёт в заголовке раздела, а голой строкой в теле она
	// повисла бы без контекста: черновик её несёт всегда, значит это штатный
	// вид приписки, а не краевой случай.
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == draftWrittenPrefix+today() {
			t.Fatalf("строка даты записи уехала в тело раздела:\n%s", data)
		}
	}
	if strings.Contains(string(data), "\n# "+id) {
		t.Fatalf("заголовок черновика уехал вторым H1:\n%s", data)
	}
	if _, err := os.Stat(draftFile(root, id)); !os.IsNotExist(err) {
		t.Fatalf("черновик остался на месте: %v", err)
	}
	boardAfter, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(boardAfter) != string(boardBefore) {
		t.Fatal("attach на задачу с файлом тронул доску")
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/tasks/XR-002.md", draftRel(id)} {
		if !strings.Contains(files, want) {
			t.Fatalf("в коммите нет %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, "docs/TASKS.md") {
		t.Fatalf("доска уехала в коммит:\n%s", files)
	}
	if st := gitOut(t, root, "status", "--porcelain", "docs"); st != "" {
		t.Fatalf("после attach в docs/ осталось незакоммиченное: %q", st)
	}
}

// TestDraftAttachCreatesTaskFile: файла у задачи не было, значит путей три, и
// третий это доска со ссылкой, переставленной на новый файл (прецедент
// cmdReviewAdd). Строка XR-004 до этого ссылается на чужой файл, как строки
// задач цели ссылаются на файл цели: ссылка переезжает, и это ожидаемый исход.
func TestDraftAttachCreatesTaskFile(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Link: "[tasks/XR-005.md](tasks/XR-005.md)"}); err != nil {
		t.Fatal(err)
	}
	dropTaskFile(t, root, "XR-004")
	id := newDraft(t, root, "то же семейство, что у хвоста")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	msg, err := cmdDraftAttach(root, id, "XR-004", CommitOpts{Msg: fmt.Sprintf("docs(tasks): %s уехал в XR-004", id)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файл docs/tasks/XR-004.md создан") {
		t.Fatalf("сообщение: %q", msg)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if row := b.find("XR-004"); row == nil || row.Link != "[tasks/XR-004.md](tasks/XR-004.md)" {
		t.Fatalf("ссылка в строке: %+v", row)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-004"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "то же семейство, что у хвоста") {
		t.Fatalf("текст черновика потерян:\n%s", data)
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/TASKS.md", "docs/tasks/XR-004.md", draftRel(id)} {
		if !strings.Contains(files, want) {
			t.Fatalf("в коммите нет %s:\n%s", want, files)
		}
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("после attach lint должен быть зелёным: %v", finds)
	}
}

// TestDraftAttachUnknownTask: приписывать некуда, значит команда отказывает,
// ничего не тронув.
func TestDraftAttachUnknownTask(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "идея")
	if _, err := cmdDraftAttach(root, id, "XR-404", CommitOpts{}); err == nil {
		t.Fatal("attach на задачу вне доски должен отказывать")
	}
	if _, err := os.Stat(draftFile(root, id)); err != nil {
		t.Fatalf("отказавший attach унёс черновик: %v", err)
	}
	if _, err := cmdDraftAttach(root, "XR-404", "XR-002", CommitOpts{}); err == nil {
		t.Fatal("attach несуществующего черновика должен отказывать")
	}
}

// TestDraftDrop: протухший черновик удаляется, удаление уезжает в индекс (без
// этого коммит команды его не заберёт), а без причины команда отказывает.
func TestDraftDrop(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "предмета больше нет")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	if _, err := cmdDraftDrop(root, id, "  ", CommitOpts{}); err == nil {
		t.Fatal("drop без причины должен отказывать")
	}
	if _, err := os.Stat(draftFile(root, id)); err != nil {
		t.Fatalf("отказавший drop тронул файл: %v", err)
	}

	msg, err := cmdDraftDrop(root, id, "код давно переписан, репродукции нет", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "код давно переписан") {
		t.Fatalf("команда не печатает причину: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, id)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}
	if st := gitOut(t, root, "diff", "--cached", "--name-status"); !strings.Contains(st, "D\t"+draftRel(id)) {
		t.Fatalf("удаление не в индексе: %q", st)
	}
}

// TestDraftSectionDemotesAllHeadings: у приписки опускаются заголовки любого
// уровня, иначе «### » черновика встало бы вровень с опущенным «## », а решётка
// внутри блока кода это текст примера, и её трогать нечем.
func TestDraftSectionDemotesAllHeadings(t *testing.T) {
	text := "# XR-008: заголовок\n\nзаписан 2026-08-05\n\n## Черновик\n\nтело\n\n### Подробности\n\n```\n## это пример, а не заголовок\n```\n"
	got := draftSection("XR-008", text)
	for _, want := range []string{
		"## Из черновика XR-008 «заголовок» (записан 2026-08-05)",
		"\n### Черновик\n",
		"\n#### Подробности\n",
		"\n## это пример, а не заголовок\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("в разделе нет %q:\n%s", want, got)
		}
	}
}

// TestDraftUncommittedRemoved: черновик, заведённый и ещё не закоммиченный
// (штатный ход, когда разбор идёт следом за записью), гиту неизвестен, git rm
// по нему падает. Такой файл удаляется обычным rm и в pathspec коммита не едет,
// иначе git commit валился бы на нём целиком, унося и правку файла задачи.
func TestDraftUncommittedRemoved(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)

	dropped := newDraft(t, root, "записал и сразу выбросил")
	msg, err := cmdDraftDrop(root, dropped, "предмета нет", CommitOpts{Msg: "docs(tasks): " + dropped + " удалён"})
	if err != nil {
		t.Fatalf("drop незакоммиченного черновика: %v", err)
	}
	if strings.Contains(msg, ", коммит ") {
		t.Fatalf("коммитить нечего, черновика гит не знал: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, dropped)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}

	attached := newDraft(t, root, "записал и сразу приписал")
	head := gitOut(t, root, "rev-parse", "HEAD")
	if _, err := cmdDraftAttach(root, attached, "XR-002", CommitOpts{Msg: "docs(tasks): " + attached + " уехал в XR-002"}); err != nil {
		t.Fatalf("attach незакоммиченного черновика: %v", err)
	}
	if _, err := os.Stat(draftFile(root, attached)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}
	if gitOut(t, root, "rev-parse", "HEAD") == head {
		t.Fatal("attach не создал коммита с файлом задачи")
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/tasks/XR-002.md" {
		t.Fatalf("в коммите не только файл задачи: %q", files)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-002"))
	if err != nil || !strings.Contains(string(data), "записал и сразу приписал") {
		t.Fatalf("текст черновика не доехал: %q, %v", data, err)
	}
	if st := gitOut(t, root, "status", "--porcelain", "docs"); st != "" {
		t.Fatalf("после разбора в docs/ осталось незакоммиченное: %q", st)
	}
}

// TestDraftPromoteKeepsGroom: оформление отложенного черновика через add --id
// перевозит пометки «Грумминга» в «Ход работы» файла задачи под строкой о
// черновике, и история разбора не теряется.
func TestDraftPromoteKeepsGroom(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы")
	if _, err := cmdDraftDefer(root, id, "ждём повторного случая", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, id)
	if _, err := cmdAdd(root, AddParams{ID: id, Title: "Оформленная", Type: "bug", Rank: "25+4+2+5+2", Cost: "S", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), draftGroomHeading) {
		t.Fatalf("раздел «Грумминг» переехал в файл задачи как есть:\n%s", data)
	}
	want := "## Ход работы\n\n- Из черновика " + id + " «уведомитель шумит из песочницы»: записан " + today() + ", оформлен " + today() + ".\n- " + today() + ", отложен: ждём повторного случая\n"
	if !strings.Contains(string(data), want) {
		t.Fatalf("пометка не переехала в «Ход работы»:\n%s\nждал:\n%s", data, want)
	}
}

// TestDraftSubcommandsNotTakenForText: подкоманда узнаётся точным совпадением
// первого позиционного аргумента, иначе «draft defer <ID> ...» молча завёл бы
// черновик с текстом «defer». Проверяется на собранном бинаре: разбор
// аргументов живёт в main, и вызов без ID выходит из процесса.
func TestDraftSubcommandsNotTakenForText(t *testing.T) {
	root := setup(t)
	bin := buildTaskctl(t)

	out, err := runTaskctl(bin, root, "draft", "defer")
	if err == nil {
		t.Fatalf("draft defer без ID должен отказывать:\n%s", out)
	}
	if !strings.Contains(out, "жду: draft defer") {
		t.Fatalf("подсказка не про форму команды:\n%s", out)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("отказавший defer завёл черновик: %+v", drafts)
	}

	if out, err := runTaskctl(bin, root, "draft", "--prio", "mid", "обычная запись черновика"); err != nil {
		t.Fatalf("запись черновика сломана новыми подкомандами: %v\n%s", err, out)
	}
	drafts, err = loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Title != "обычная запись черновика" {
		t.Fatalf("накопитель после обычной записи: %+v", drafts)
	}
	if out, err := runTaskctl(bin, root, "draft", "list"); err != nil {
		t.Fatalf("draft list сломан: %v\n%s", err, out)
	} else if !strings.Contains(out, "обычная запись черновика") {
		t.Fatalf("draft list:\n%s", out)
	}
}

// TestDraftDropWarnsAboutMentions: снятая запись оставляет за собой мёртвые
// ссылки. Хранилища у связей нет, они читаются из упоминаний ID прозой
// постановки, и починить их за человека команда не может, поэтому она называет
// места. Молчание тут неотличимо от того, что ссылок нет. Архив не смотрится:
// закрытую работу править никто не пойдёт, а ID целым словом отличает снятый
// XR-48 от соседнего XR-483.
func TestDraftDropWarnsAboutMentions(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "предмет пропал")
	put := func(rel, text string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("docs/tasks/XR-002.md", "# XR-002\n\nупирается в "+id+", ждём разбора\n")
	put("docs/tasks/archive/2026/XR-900.md", "# XR-900\n\nтут тоже помянут "+id+"\n")
	put("docs/tasks/XR-003.md", "# XR-003\n\nсовсем другой предмет\n")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик и соседи")

	msg, err := cmdDraftDrop(root, id, "предмета больше нет", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "docs/tasks/XR-002.md") {
		t.Fatalf("команда не сказала, что на %s ещё ссылаются: %q", id, msg)
	}
	if !strings.Contains(msg, "ещё ссылаются") {
		t.Fatalf("предупреждение не названо словами: %q", msg)
	}
	if strings.Contains(msg, "XR-900") {
		t.Fatalf("в предупреждение попал архив, а его править никто не пойдёт: %q", msg)
	}
	if strings.Contains(msg, "XR-003") {
		t.Fatalf("в предупреждение попал файл без упоминания: %q", msg)
	}

	// Ссылок нет, и говорить не о чем: хвост молчит. Ссылка на снятый ID
	// убирается из соседа руками: освободившийся номер достаётся следующей
	// записи, и без уборки она нашла бы упоминание сама себя.
	put("docs/tasks/XR-002.md", "# XR-002\n\nсосед без упоминаний\n")
	quiet := newDraft(t, root, "на эту запись никто не сослался")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "вторая запись")
	msg, err = cmdDraftDrop(root, quiet, "предмета нет", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "ещё ссылаются") {
		t.Fatalf("предупреждение выдумано на пустом месте: %q", msg)
	}
}

// TestMentionsOfWholeWord: ID ищется целым словом, иначе снятый XR-48 находился
// бы в каждом упоминании XR-483, и предупреждение врало бы о местах.
func TestMentionsOfWholeWord(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "docs", "tasks", "XR-002.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("# XR-002\n\nтут только XR-483 и XR-4831\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hits := mentionsOf(root, "XR-48"); len(hits) != 0 {
		t.Fatalf("XR-48 нашёлся внутри соседнего номера: %v", hits)
	}
	if hits := mentionsOf(root, "XR-483"); len(hits) != 1 || hits[0] != "docs/tasks/XR-002.md" {
		t.Fatalf("целое упоминание не нашлось: %v", hits)
	}
}

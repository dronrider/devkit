package spend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// turnLine это ход ассистента с расходом, как его пишет харнес.
func turnLine(req string, out, in, read, write int) string {
	rec := map[string]any{
		"type":      "assistant",
		"requestId": req,
		"message": map[string]any{
			"usage": map[string]any{
				"output_tokens":               out,
				"input_tokens":                in,
				"cache_read_input_tokens":     read,
				"cache_creation_input_tokens": write,
			},
		},
	}
	data, _ := json.Marshal(rec)
	return string(data) + "\n"
}

// work кладёт в каталог субагентов поток работы и её спутника.
func work(t *testing.T, dir, id, kind, parent string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".jsonl"), []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"agentType": kind, "spawnDepth": 1}
	if parent != "" {
		meta["parentAgentId"] = parent
		meta["spawnDepth"] = 2
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// subagents это каталог потоков субагентов сессии в подменном доме.
func subagents(home, slug, session string) string {
	return filepath.Join(Dir(home), slug, session, "subagents")
}

// TestReadDedupsByRequest: запись с usage повторяется по числу итераций
// ответа, и все копии несут одинаковые числа. Без склейки по requestId объём
// сессии вырос бы в разы, поэтому повтор не считается вторым ходом.
func TestReadDedupsByRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-a1.jsonl")
	body := turnLine("req-1", 100, 2, 3000, 400) +
		turnLine("req-1", 100, 2, 3000, 400) +
		turnLine("req-2", 50, 1, 1000, 0)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if u.Turns != 2 || u.Output != 150 || u.Input != 3 || u.CacheRead != 4000 || u.CacheWrite != 400 {
		t.Fatalf("расход %+v, ждал два хода с выводом 150", u)
	}
}

// TestReadSkipsJunk: харнес пишет журнал на ходу, и обрыв записи при отмене
// сессии не повод рвать свод. Реплика человека и пустой usage в счёт не идут.
func TestReadSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-a1.jsonl")
	body := "{битая строка\n" +
		"\n" +
		`{"type":"user","message":{"content":"привет"}}` + "\n" +
		turnLine("req-1", 10, 0, 0, 0) +
		turnLine("req-zero", 0, 0, 0, 0)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if u.Turns != 1 || u.Output != 10 {
		t.Fatalf("расход %+v, ждал один ход с выводом 10", u)
	}
}

// TestReadTakesLongLine: ход с длинным результатом инструмента доходит до
// мегабайтов, и обычного буфера чтения на такую строку не хватает.
func TestReadTakesLongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-a1.jsonl")
	long := `{"type":"user","message":{"content":"` + strings.Repeat("x", 1<<20) + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(long+turnLine("req-1", 7, 0, 0, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if u.Turns != 1 || u.Output != 7 {
		t.Fatalf("расход %+v: длинная строка съела следующий ход", u)
	}
}

// TestWorkFileFindsByGlob: работа исполнителя ведётся из дерева задачи, а
// пакет этапов закрывает основной чекаут, поэтому поток ищется и без сессии,
// по слепку любого проекта.
func TestWorkFileFindsByGlob(t *testing.T) {
	home := t.TempDir()
	dir := subagents(home, "-Users-rider-projects-devkit-dk-912", "sess-1")
	work(t, dir, "a1", "exec-high", "", turnLine("req-1", 5, 0, 0, 0))
	if _, ok := WorkFile(home, "sess-1", "a1"); !ok {
		t.Fatal("поток не нашёлся по сессии этапа")
	}
	if _, ok := WorkFile(home, "", "a1"); !ok {
		t.Fatal("поток не нашёлся глобом без сессии")
	}
	if _, ok := WorkFile(home, "чужая", "a1"); !ok {
		t.Fatal("промах по сессии не откатился к глобу")
	}
	if _, ok := WorkFile(home, "sess-1", "a2"); ok {
		t.Fatal("нашёлся поток работы, которой нет")
	}
}

// TestTreeNestsByParent: вложенная работа ложится под ту, которая её подняла,
// и её расход входит в итог родителя. Родитель назван прямо в спутнике
// потока, разбирать ответы инструмента Agent для этого не нужно.
func TestTreeNestsByParent(t *testing.T) {
	home := t.TempDir()
	dir := subagents(home, "slug", "sess-1")
	work(t, dir, "exec1", "exec-high", "", turnLine("req-1", 100, 0, 0, 0))
	work(t, dir, "proof1", "proofread", "exec1", turnLine("req-2", 40, 0, 0, 0))
	work(t, dir, "deep1", "general-purpose", "proof1", turnLine("req-3", 5, 0, 0, 0))
	work(t, dir, "review1", "review-high", "", turnLine("req-4", 70, 0, 0, 0))

	n, ok := Tree(home, "sess-1", "exec1")
	if !ok {
		t.Fatal("дерево работы не собралось")
	}
	if len(n.Kids) != 1 || n.Kids[0].Work != "proof1" || n.Kids[0].Kind != "proofread" {
		t.Fatalf("дети работы %+v, ждал одну вычитку", n.Kids)
	}
	if len(n.Kids[0].Kids) != 1 || n.Kids[0].Kids[0].Work != "deep1" {
		t.Fatalf("вложенность глубже одного уровня потеряна: %+v", n.Kids[0])
	}
	if got := n.Total().Output; got != 145 {
		t.Fatalf("итог работы с вложенными = %d, ждал 145", got)
	}
	if got := n.Usage.Output; got != 100 {
		t.Fatalf("свой расход работы = %d, ждал 100", got)
	}
}

// TestTreeNestsByToolUse: старые спутники родителя не называют, и работа
// узнаётся по номеру вызова инструмента, который стоит в потоке родителя.
func TestTreeNestsByToolUse(t *testing.T) {
	home := t.TempDir()
	dir := subagents(home, "slug", "sess-1")
	parent := turnLine("req-1", 100, 0, 0, 0) +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_777","name":"Agent"}]}}` + "\n"
	work(t, dir, "exec1", "exec-high", "", parent)
	if err := os.WriteFile(filepath.Join(dir, "agent-proof1.jsonl"), []byte(turnLine("req-2", 40, 0, 0, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	old := `{"agentType":"proofread","toolUseId":"toolu_777","spawnDepth":2}`
	if err := os.WriteFile(filepath.Join(dir, "agent-proof1.meta.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	n, ok := Tree(home, "sess-1", "exec1")
	if !ok {
		t.Fatal("дерево работы не собралось")
	}
	if len(n.Kids) != 1 || n.Kids[0].Work != "proof1" {
		t.Fatalf("дети работы %+v, ждал вычитку, найденную по номеру вызова", n.Kids)
	}
	if got := n.Total().Output; got != 140 {
		t.Fatalf("итог работы = %d, ждал 140", got)
	}
}

// TestTreeMissingWork: работы нет в журналах харнеса claude (заход второго
// харнеса либо стёртый журнал), и дерево честно не собирается.
func TestTreeMissingWork(t *testing.T) {
	home := t.TempDir()
	if _, ok := Tree(home, "sess-1", "a1"); ok {
		t.Fatal("дерево собралось на пустом доме")
	}
}

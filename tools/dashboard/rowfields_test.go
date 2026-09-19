package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Экран задачи получает строку доски типизированной boardRow, а не сырым
// ответом taskctl (границы DK-1054). Поле jsonRow (tools/taskctl/json.go),
// которого нет в boardRow, работает молча: тому же классу поломки чинил
// сторож ранга DK-650, у даты правки строки (moved) своего не было, и чип
// app.js висел мёртвым с появления. Тест гоняет настоящий taskctl на
// синтетической доске и сверяет ключи ответа с полями boardRow рефлексией:
// jsonRow лежит в чужом модуле (package main tools/taskctl), исходник его
// тест не разбирает, а верит только тому, что утилита правда отдаёт.

// jsonRowFieldExceptions называет поля ответа taskctl list --json, для
// которых отсутствие в boardRow осознанно: значение доезжает до экрана
// задачи не строкой доски, а другим каналом.
var jsonRowFieldExceptions = map[string]string{
	// Armed и Arm это состояние взвода строки (LLD DK-933, решение 1). До
	// экрана задачи они едут не строкой доски, а отдельным запросом
	// taskDeps (taskctl dep list --json, handleTask кладёт их в ответ
	// верхним уровнем): формат и рубеж держит chain_test.go, второй копии
	// поля в boardRow не нужно.
	"armed": "едет отдельным полем ответа через taskctl dep list --json, держит chain_test.go",
	"arm":   "едет отдельным полем ответа через taskctl dep list --json, держит chain_test.go",
}

// TestBoardRowCarriesEveryJSONField сверяет перечень полей ответа
// taskctl list --json (jsonRow) с типом boardRow: поле, которое утилита
// отдаёт, а тип дашборда не читает, молча пропадает с экрана задачи, как
// пропадала дата правки строки до DK-1054.
func TestBoardRowCarriesEveryJSONField(t *testing.T) {
	proj := rowFieldsBoard(t)
	raw := runTaskctl(t, proj, "list", "--json")

	var board struct {
		Sections []struct {
			Rows []map[string]json.RawMessage `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(raw), &board); err != nil {
		t.Fatalf("ответ list --json не разобрался: %v\n%s", err, raw)
	}
	seen := map[string]bool{}
	for _, sec := range board.Sections {
		for _, row := range sec.Rows {
			for k := range row {
				seen[k] = true
			}
		}
	}

	// Проверка самой фикстуры: если какое-то из необязательных полей
	// перестало приходить, сторож молча проверяет пустое множество.
	for _, k := range []string{"after", "held_by", "accept", "adjustments", "moved", "notes", "block", "fail"} {
		if !seen[k] {
			t.Fatalf("фикстура не подняла поле %q ответа list --json: сторож ничего не проверит", k)
		}
	}

	have := boardRowJSONFields()
	var missing []string
	for k := range seen {
		if have[k] {
			continue
		}
		if _, ok := jsonRowFieldExceptions[k]; ok {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("ответ taskctl list --json несёт поля %v, которых нет в типе boardRow "+
			"(tools/dashboard/tasks.go) и нет в списке осознанных исключений jsonRowFieldExceptions: "+
			"поле молча пропадёт с экрана задачи", missing)
	}
}

// TestTaskRowCarriesMovedDate проверяет дату правки строки в ответе ручки
// задачи запросом через настоящий HTTP-хендлер, а не строкой в тексте
// app.js: чип на экране задачи рисуется из row.moved (formPage, рядом с
// комментарием про полосу чипов), а сервер поле не отдавал с появления чипа
// (DK-1054).
func TestTaskRowCarriesMovedDate(t *testing.T) {
	e := newTestEnv(t)
	if err := os.Remove(filepath.Join(e.proj, "docs", "TASKS.md")); err != nil {
		t.Fatal(err)
	}
	taskctlFixture(t, e.bin)
	sandboxRealNotifier(t, e)

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", e.proj,
			"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	runTaskctl(t, e.proj, "init", "--prefix", "XR", "--name", "demo")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	runTaskctl(t, e.proj, "add", "--id", "XR-001", "--title", "Первая",
		"--rank", "25+1+0+0+0", "--cost", "M", "--accept", "agent")
	git("add", "-A")
	git("commit", "-q", "-m", "add")

	task := getTask(t, e.loggedClient(t), e, "XR-001")
	moved, _ := taskRowField(t, task, "moved").(string)
	if moved == "" {
		t.Fatalf("ответ ручки задачи без даты правки строки (row.moved): %v", task["row"])
	}
	if want := time.Now().Format("2006-01-02"); moved != want {
		t.Errorf("дата правки строки в ответе %q, ждал сегодняшнюю %q", moved, want)
	}
}

// boardRowJSONFields перечисляет json-теги полей boardRow рефлексией по
// типу: свой тип дашборд читает напрямую, без разбора исходника.
func boardRowJSONFields() map[string]bool {
	out := map[string]bool{}
	typ := reflect.TypeOf(boardRow{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

// rowFieldsBoard поднимает синтетическую доску настоящим taskctl, где у
// строк заведены все необязательные поля ответа list --json, кроме взвода
// (armed/arm, см. jsonRowFieldExceptions, тот канал проверяет chain_test.go):
// после (after) и держащее ребро (held_by) даёт dep add, вид приёмки
// (accept) - mixed с барьером, поправку к рангу (adjustments) - цена M,
// провал (fail) и блокировку (block) - move и fail из in-progress. Дата
// правки (moved) и заметка о возрасте строки (notes) приходят сами, пока
// дерево доски чистое, поэтому каждый шаг коммитится настоящим git тут же.
func rowFieldsBoard(t *testing.T) string {
	t.Helper()
	buildTaskctl(t)
	t.Setenv("HOME", t.TempDir())
	proj := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", proj,
			"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", msg)
	}

	git("init", "-q", "-b", "main")
	runTaskctl(t, proj, "init", "--prefix", "XR", "--name", "demo")
	commit("init")

	runTaskctl(t, proj, "add", "--id", "XR-001", "--title", "Первая",
		"--rank", "50+3+1+0+1", "--cost", "L", "--accept", "agent")
	runTaskctl(t, proj, "add", "--id", "XR-002", "--title", "Вторая",
		"--rank", "25+2+1+0+2", "--cost", "M", "--accept", "mixed", "--barrier", "секрет")
	runTaskctl(t, proj, "add", "--id", "XR-003", "--title", "Третья",
		"--rank", "0+1+0+0+0", "--cost", "S", "--accept", "agent")
	commit("add")

	runTaskctl(t, proj, "dep", "add", "XR-002", "XR-001")
	commit("dep")

	runTaskctl(t, proj, "move", "XR-001", "in-progress")
	commit("move-XR-001")
	runTaskctl(t, proj, "fail", "XR-001", "--reason", "тест провала")
	commit("fail-XR-001")

	runTaskctl(t, proj, "move", "XR-003", "in-progress")
	commit("move-XR-003")
	runTaskctl(t, proj, "move", "XR-003", "blocked", "--reason", "тест блокировки")
	commit("block-XR-003")

	return proj
}

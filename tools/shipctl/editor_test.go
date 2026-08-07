package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const altToken = "sk-секрет-второй-подписки"

// altHome заводит машинный слой второй подписки на временном HOME: каталог
// конфига с settings.json из переданных ключей env. Пустая карта значит
// «каталога нет вовсе». Возвращает сам каталог конфига.
func altHome(t *testing.T, env map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, altConfigHome)
	if env == nil {
		return dir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"env": env})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, altSettingsName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func altEnv(base string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL":   base,
		"ANTHROPIC_AUTH_TOKEN": altToken,
		"ANTHROPIC_MODEL":      "glm-4.6",
	}
}

// stubEditor кладёт в PATH подставной code, который пишет в лог свои аргументы
// и CLAUDE_CONFIG_DIR. По этому логу видно и то, что редактор звался, и то, с
// каким каталогом конфига.
func stubEditor(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "code.log")
	write(t, bin, editorBin, "#!/bin/sh\necho \"CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR $*\" >> \""+log+"\"\n")
	if err := os.Chmod(filepath.Join(bin, editorBin), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// TestCodeDryRun: dry-run печатает окружение второй подписки и ничего не
// делает. Редактор не зовётся, дерево задачи не заводится, токен в отчёт не
// попадает.
func TestCodeDryRun(t *testing.T) {
	root, callLog := setup(t, "", "")
	dir := altHome(t, altEnv("https://endpoint.example/anthropic/"))
	log := stubEditor(t)

	msg, err := cmdCode(root, CodeParams{ID: "XR-002", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR=" + dir,
		"base URL: https://endpoint.example/anthropic",
		"модель: glm-4.6",
		"токен: есть",
		editorBin + " -n " + treePath(root, "XR-002"),
		"dry-run: редактор не запускался",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в отчёте dry-run нет %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, altToken) {
		t.Fatal("токен утёк в отчёт dry-run")
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("dry-run позвал редактор")
	}
	if wt := gitT(t, root, "worktree", "list"); strings.Contains(wt, "xr-002") {
		t.Fatalf("dry-run завёл дерево задачи:\n%s", wt)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move") {
		t.Fatalf("dry-run двинул доску: %q", calls)
	}
}

// TestCodeOpensWindow: запуск без dry-run заводит дерево задачи тем же путём,
// что start, и открывает окно редактора с каталогом конфига второй подписки.
func TestCodeOpensWindow(t *testing.T) {
	root, callLog := setup(t, "", "")
	dir := altHome(t, altEnv("https://endpoint.example/anthropic"))
	log := stubEditor(t)

	msg, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err != nil {
		t.Fatal(err)
	}
	// Путь берётся из git: на macOS временный каталог лежит под симлинком
	// /var -> /private/var, и git отдаёт дерево уже развёрнутым.
	tree, err := filepath.EvalSymlinks(treePath(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := gitT(t, root, "worktree", "list"); !strings.Contains(wt, tree) {
		t.Fatalf("дерево задачи не заведено:\n%s", wt)
	}
	if calls, _ := os.ReadFile(callLog); !strings.Contains(string(calls), "move XR-002 in-progress") {
		t.Fatalf("задача не переведена в In progress: %q", calls)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal("редактор не звался:", err)
	}
	want := "CLAUDE_CONFIG_DIR=" + dir + " -n " + tree + "\n"
	if string(got) != want {
		t.Fatalf("редактор позван не так:\n%q\nждали\n%q", got, want)
	}
	if !strings.Contains(msg, "окно открыто") {
		t.Fatalf("в отчёте нет строки про открытое окно:\n%s", msg)
	}
	if strings.Contains(msg, altToken) {
		t.Fatal("токен утёк в отчёт")
	}

	// Второй запуск по той же задаче не заводит второго дерева и не спотыкается
	// о занятую ветку: окно на дереве открывают по многу раз в день.
	if _, err := cmdCode(root, CodeParams{ID: "XR-002"}); err != nil {
		t.Fatalf("повторный запуск отбился: %v", err)
	}
	if got, _ := os.ReadFile(log); strings.Count(string(got), tree) != 2 {
		t.Fatalf("повторный запуск не открыл окно:\n%s", got)
	}
}

// TestCodeMachineLayerGaps: нехватка машинного слоя это отказ с командой
// починки, а не окно, молча ушедшее на первую подписку.
func TestCodeMachineLayerGaps(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"каталога нет", nil, "devkitctl doctor --fix"},
		{"пустой endpoint", map[string]string{"ANTHROPIC_AUTH_TOKEN": altToken}, "ANTHROPIC_BASE_URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, _ := setup(t, "", "")
			altHome(t, c.env)
			log := stubEditor(t)
			_, err := cmdCode(root, CodeParams{ID: "XR-002"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ждали отказ со словами %q, получили: %v", c.want, err)
			}
			if _, err := os.Stat(log); err == nil {
				t.Fatal("окно открылось без машинного слоя")
			}
		})
	}
}

// TestProbeAnswers: пробник ходит в endpoint Anthropic-совместимым запросом и
// разбирает ответ. Печать окружения не отличает живую подписку от протухшего
// токена, а пробник отличает.
func TestProbeAnswers(t *testing.T) {
	var gotAuth, gotVersion, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotVersion = r.Header.Get("anthropic-version")
		body, _ := json.Marshal(map[string]any{"model": "glm-4.6", "type": "message"})
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	root, _ := setup(t, "", "")
	altHome(t, altEnv(srv.URL))
	stubEditor(t)
	msg, err := cmdCode(root, CodeParams{ID: "XR-002", DryRun: true, Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "200, модель в ответе glm-4.6") {
		t.Fatalf("в отчёте нет модели из ответа:\n%s", msg)
	}
	if gotAuth != "Bearer "+altToken {
		t.Fatalf("токен ушёл не заголовком Authorization: %q", gotAuth)
	}
	if gotVersion == "" {
		t.Error("запрос без заголовка anthropic-version, Anthropic-совместимый endpoint его ждёт")
	}
	if !strings.Contains(gotBody, "glm-4.6") {
		t.Errorf("модель не ушла в теле запроса: %q", gotBody)
	}
}

// TestProbeFailures: нерабочий endpoint это отказ, а не открытое окно, и токен
// не печатается даже когда чужая сторона вернула его в теле ответа.
func TestProbeFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"token ` + altToken + ` протух"}`))
	}))
	defer srv.Close()

	root, _ := setup(t, "", "")
	altHome(t, altEnv(srv.URL))
	log := stubEditor(t)
	_, err := cmdCode(root, CodeParams{ID: "XR-002", Probe: true})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("ждали отказ по ответу 401, получили: %v", err)
	}
	if strings.Contains(err.Error(), altToken) {
		t.Fatalf("токен утёк в текст ошибки: %v", err)
	}
	if !strings.Contains(err.Error(), "<токен>") {
		t.Fatalf("тело ответа не зачищено от токена: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("окно открылось при мёртвом endpoint")
	}
}

// TestProbeWithoutToken: без токена пробник отказывается сразу, не ходя в сеть,
// и говорит, какого ключа не хватает.
func TestProbeWithoutToken(t *testing.T) {
	root, _ := setup(t, "", "")
	altHome(t, map[string]string{
		"ANTHROPIC_BASE_URL": "https://endpoint.example/anthropic",
		"ANTHROPIC_MODEL":    "glm-4.6",
	})
	stubEditor(t)
	msg, err := cmdCode(root, CodeParams{ID: "XR-002", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "токен: нет") {
		t.Fatalf("dry-run без токена должен называть признак:\n%s", msg)
	}
	if _, err := cmdCode(root, CodeParams{ID: "XR-002", DryRun: true, Probe: true}); err == nil ||
		!strings.Contains(err.Error(), "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("ждали отказ пробника без токена: %v", err)
	}
}

// TestCodeRefusesCorp: вторая подписка личная, и на код компании окно не
// открывает; отбиться надо до того, как заведено дерево задачи.
func TestCodeRefusesCorp(t *testing.T) {
	root, _ := setup(t, "", "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	log := stubEditor(t)
	write(t, root, corpTrackerPath, "key = XR\nrepo = /nowhere\n")
	_, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err == nil || !strings.Contains(err.Error(), "корп-контур") {
		t.Fatalf("ждали отказ в корп-контуре, получили: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("окно открылось на коде компании")
	}
}

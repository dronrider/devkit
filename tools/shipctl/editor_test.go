package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const altToken = "sk-секрет-второй-подписки"

// altHome заводит машинный слой второй подписки на временном HOME: объявление
// харнеса в конфиге харнесов плюс каталог конфига с settings.json из переданных
// ключей env. Пустая карта значит «каталога нет вовсе». Возвращает сам каталог
// конфига.
func altHome(t *testing.T, env map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".devkit", "claude-glm")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(home, altMachineConfig)), 0o755); err != nil {
		t.Fatal(err)
	}
	// Каталог пишется от ~/, как его пишет пользователь: разворачивание тильды
	// это часть чтения машинного ключа, и проверяется оно тут же.
	conf := "enabled = [\"claude-code\", \"" + altHarness + "\"]\n\n[" + altHarness + "]\n" +
		altHomeKey + " = \"~/.devkit/claude-glm\"\n"
	if err := os.WriteFile(filepath.Join(home, altMachineConfig), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
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
// и каталог конфига из окружения. По этому логу видно и то, что редактор
// звался, и то, что подписка окну достаётся не запуском, а директорией.
func stubEditor(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "code.log")
	write(t, bin, editorBin, "#!/bin/sh\necho \"cfg=$CLAUDE_CONFIG_DIR $*\" >> \""+log+"\"\n")
	if err := os.Chmod(filepath.Join(bin, editorBin), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return log
}

// TestCodeDryRun: dry-run печатает окружение второй подписки и ничего не
// делает. Редактор не зовётся, копия окна не заводится, токен в отчёт не
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
		"копия окна: " + windowTree(root) + " (ещё не заведена",
		"машинный слой: " + filepath.Join(dir, altSettingsName),
		"base URL: https://endpoint.example/anthropic",
		"модель: glm-4.6",
		"токен: есть",
		"заголовок окна: [" + strings.ToUpper(windowSuffix()) + "] ",
		editorBin + " " + windowTree(root),
		"dry-run: редактор не запускался",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в отчёте dry-run нет %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, " -n ") {
		t.Errorf("окно второй подписки одно, нового не просим:\n%s", msg)
	}
	if strings.Contains(msg, altToken) {
		t.Fatal("токен утёк в отчёт dry-run")
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("dry-run позвал редактор")
	}
	if _, err := os.Stat(windowTree(root)); err == nil {
		t.Fatal("dry-run завёл копию окна")
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move") {
		t.Fatalf("dry-run двинул доску: %q", calls)
	}
}

// TestCodeOpensWindow: запуск без dry-run заводит копию окна, кладёт в неё
// ветку задачи тем же путём, что start, и открывает окно на копии. Подписка
// достаётся окну директорией, а не запуском: в окружении процесса редактора
// каталога конфига нет.
func TestCodeOpensWindow(t *testing.T) {
	root, callLog := setup(t, "", "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	log := stubEditor(t)

	msg, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err != nil {
		t.Fatal(err)
	}
	// Путь берётся из git: на macOS временный каталог лежит под симлинком
	// /var -> /private/var, и git отдаёт дерево уже развёрнутым.
	tree, err := filepath.EvalSymlinks(windowTree(root))
	if err != nil {
		t.Fatal(err)
	}
	if wt := gitT(t, root, "worktree", "list"); !strings.Contains(wt, tree) {
		t.Fatalf("копия окна не заведена:\n%s", wt)
	}
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002" {
		t.Fatalf("в копии окна стоит %q, а ветка задачи xr-002", br)
	}
	if calls, _ := os.ReadFile(callLog); !strings.Contains(string(calls), "move XR-002 in-progress") {
		t.Fatalf("задача не переведена в In progress: %q", calls)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal("редактор не звался:", err)
	}
	want := "cfg= " + tree + "\n"
	if string(got) != want {
		t.Fatalf("редактор позван не так:\n%q\nждали\n%q", got, want)
	}
	if !strings.Contains(msg, "окно открыто") {
		t.Fatalf("в отчёте нет строки про открытое окно:\n%s", msg)
	}
	if strings.Contains(msg, altToken) {
		t.Fatal("токен утёк в отчёт")
	}

	// Второй запуск по той же задаче не заводит второй копии и не спотыкается
	// о занятую ветку: окно открывают по многу раз в день.
	if _, err := cmdCode(root, CodeParams{ID: "XR-002"}); err != nil {
		t.Fatalf("повторный запуск отбился: %v", err)
	}
	if got, _ := os.ReadFile(log); strings.Count(string(got), tree) != 2 {
		t.Fatalf("повторный запуск не открыл окно:\n%s", got)
	}
	if _, linked, err := worktrees(root); err != nil {
		t.Fatal(err)
	} else if len(linked) != 1 {
		t.Fatalf("копия окна одна на все задачи, а деревьев %d: %v", len(linked), linked)
	}
}

// TestCodeWindowFiles: окружение подписки и заголовок окна лежат в самой
// копии, поэтому окно, открытое мимо shipctl (док, «Open Recent»), ходит во
// вторую подписку и называется её ярусом. Файлы эти машинные, в гит они не
// едут: в первом токен, второй это настройка вида.
func TestCodeWindowFiles(t *testing.T) {
	root, _ := setup(t, "", "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	stubEditor(t)
	if _, err := cmdCode(root, CodeParams{ID: "XR-002"}); err != nil {
		t.Fatal(err)
	}
	tree := windowTree(root)

	envPath := filepath.Join(tree, windowEnvFile)
	var doc struct {
		Env map[string]string `json:"env"`
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://endpoint.example/anthropic",
		"ANTHROPIC_AUTH_TOKEN": altToken,
		"ANTHROPIC_MODEL":      "glm-4.6",
		harnessEnv:             altHarness,
	}
	for k, v := range want {
		if doc.Env[k] != v {
			t.Errorf("в %s ключ %s это %q, ждали %q", envPath, k, doc.Env[k], v)
		}
	}
	if st, err := os.Stat(envPath); err != nil {
		t.Fatal(err)
	} else if mode := st.Mode() & 0o777; mode&0o077 != 0 {
		t.Errorf("у %s права %o, а там лежит токен второй подписки", envPath, mode)
	}

	title, err := os.ReadFile(filepath.Join(tree, windowTitleFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(title), windowTitle(root)) {
		t.Errorf("в настройках копии нет заголовка %q:\n%s", windowTitle(root), title)
	}

	for _, name := range []string{windowEnvFile, windowTitleFile} {
		out, err := exec.Command("git", "-C", tree, "check-ignore", "-q", name).CombinedOutput()
		if err != nil {
			t.Errorf("%s не спрятан от гита (%v %s): он лёг бы черновиком под ноги переключению задачи", name, err, out)
		}
	}
	if st := gitT(t, tree, "status", "--porcelain"); st != "" {
		t.Errorf("копия окна сразу после заведения не чиста:\n%s", st)
	}
}

// TestCodeSwitchesTask: следующая задача берётся в том же окне, копия одна.
// Черновики прошлой задачи при этом переносить в чужую ветку нельзя, и молчать
// про них тоже: это отказ с перечнем файлов.
func TestCodeSwitchesTask(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	stubEditor(t)
	if _, err := cmdCode(root, CodeParams{ID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	tree := windowTree(root)

	write(t, tree, "черновик.txt", "недописанное\n")
	_, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err == nil {
		t.Fatal("копия с черновиками прошлой задачи переключилась молча")
	}
	for _, want := range []string{"черновик.txt", "xr-002"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-001" {
		t.Fatalf("отказ всё же переключил ветку на %q", br)
	}

	// Своя же задача повторным запуском не отбивается: незакоммиченное лежит в
	// своей ветке, и окно просто открывают заново.
	if _, err := cmdCode(root, CodeParams{ID: "XR-001"}); err != nil {
		t.Fatalf("повторный запуск своей задачи отбился: %v", err)
	}
	if err := os.Remove(filepath.Join(tree, "черновик.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdCode(root, CodeParams{ID: "XR-002"}); err != nil {
		t.Fatal(err)
	}
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002" {
		t.Fatalf("копия не переключилась на новую задачу, в ней %q", br)
	}
	if _, linked, err := worktrees(root); err != nil {
		t.Fatal(err)
	} else if len(linked) != 1 {
		t.Fatalf("на две задачи заведено деревьев: %d (%v)", len(linked), linked)
	}
	// Ветка прошлой задачи цела: её дозреют и сольют, вернув в копию.
	if gitT(t, root, "branch", "--list", "xr-001") == "" {
		t.Error("ветка прошлой задачи пропала при переключении")
	}
}

// TestStatusNamesWindowCopy: копия окна видна в status своим именем и с
// состоянием ветки. Стоит она в списке деревьев всегда, в том числе
// отцепленной между задачами, и молчаливая строка с пустой веткой читалась бы
// поломкой.
func TestStatusNamesWindowCopy(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	stubEditor(t)
	if _, err := cmdCode(root, CodeParams{ID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна: xr-001 в ") {
		t.Fatalf("status не назвал копию окна с веткой задачи:\n%s", msg)
	}
	gitT(t, windowTree(root), "checkout", "--detach")
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна: detached") {
		t.Fatalf("status не сказал, что копия окна свободна:\n%s", msg)
	}
}

// TestCodeRefusesBusyBranch: ветка задачи выложена в другом дереве (второе окно
// или дерево субагента). Дважды одну ветку git не выкладывает, и отказ обязан
// сказать, чем её берут на чтение, а не просто «занята».
func TestCodeRefusesBusyBranch(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	stubEditor(t)
	other := filepath.Join(t.TempDir(), "чужое-дерево")
	gitT(t, root, "worktree", "add", "-b", "xr-001", other, "main")

	_, err := cmdCode(root, CodeParams{ID: "XR-001"})
	if err == nil {
		t.Fatal("ветка занята другим деревом, а команда не отбилась")
	}
	for _, want := range []string{"уже в работе", "checkout --detach"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestCodeSyncsEnvFromMachineLayer: машинный слой это хозяин ключей, и копия
// подтягивает их при каждом открытии окна. Разъехавшись, копия молча ходила бы
// в старый endpoint со старым токеном, а видно это только по счёту в конце
// недели.
func TestCodeSyncsEnvFromMachineLayer(t *testing.T) {
	root, _ := setup(t, "", "")
	dir := altHome(t, altEnv("https://старый.example/anthropic"))
	stubEditor(t)
	if _, err := cmdCode(root, CodeParams{ID: "XR-002"}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"env": altEnv("https://новый.example/anthropic")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, altSettingsName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(windowTree(root), windowEnvFile)
	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "https://новый.example/anthropic") {
		t.Fatalf("окружение копии не подтянулось из машинного слоя:\n%s", got)
	}
	if !strings.Contains(msg, envPath) {
		t.Errorf("правка окружения копии прошла молча:\n%s", msg)
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

// TestCodeConfigDirFromMachineKey: каталог конфига берётся из машинного ключа, а
// не из константы утилиты. Ключ этот один на все три читателя (раскладка
// хозяйства, окружение подпроцесса, окно редактора), и разъехавшиеся пути дали
// бы окно на дорогой подписке, считающее себя дешёвым.
func TestCodeConfigDirFromMachineKey(t *testing.T) {
	root, _ := setup(t, "", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "своя-вторая-подписка")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"env": altEnv("https://endpoint.example/anthropic")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, altSettingsName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Секция соседа стоит выше, и ключ home есть у обеих: ищется он в своей
	// секции, а не первым попавшимся в файле.
	conf := "enabled = [\"claude-code\", \"" + altHarness + "\"]\n\n[сосед]\n" +
		altHomeKey + " = \"~/чужое\"\n\n[" + altHarness + "]\n" +
		altHomeKey + " = \"" + dir + "\"\n"
	if err := os.WriteFile(filepath.Join(home, altMachineConfig), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdCode(root, CodeParams{ID: "XR-002", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "машинный слой: "+filepath.Join(dir, altSettingsName)) {
		t.Fatalf("каталог взят не из машинного ключа:\n%s", msg)
	}
	if !strings.Contains(msg, harnessEnv+"="+altHarness) {
		t.Fatalf("окну не назван харнес: без имени сессия на второй подписке считает себя домашней:\n%s", msg)
	}
}

// TestAltConfigDirValues: значение ключа home читается тем же подмножеством
// TOML, каким машинный конфиг читают agentctl и devkitctl (фикстура подмножества
// kit/harness/testdata/parse-values.toml). Разойдись три инструмента на решётке в
// строке, один и тот же файл читался бы по-разному, а расплатой было бы окно на
// дорогой подписке с мусором вместо каталога.
func TestAltConfigDirValues(t *testing.T) {
	cases := []struct {
		name, value, want, refuse string
	}{
		{
			name:  "хвостовой комментарий",
			value: `"~/.devkit/claude-glm"  # мой каталог`,
			want:  ".devkit/claude-glm",
		},
		{
			name:  "решётка внутри кавычек это часть пути",
			value: `"~/каталог # два"`,
			want:  "каталог # два",
		},
		{
			name:  "экранированная кавычка внутри пути",
			value: `"~/кавычка\"внутри"`,
			want:  `кавычка"внутри`,
		},
		{
			name:   "после значения лишнее",
			value:  `"~/.devkit/claude-glm" мусор`,
			refuse: "после значения лишнее",
		},
		{
			name:   "строка не закрыта",
			value:  `"~/.devkit/claude-glm`,
			refuse: "строка не закрыта",
		},
		{
			name:   "значение без кавычек",
			value:  `~/.devkit/claude-glm`,
			refuse: "не строка в двойных кавычках",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.MkdirAll(filepath.Join(home, ".devkit"), 0o755); err != nil {
				t.Fatal(err)
			}
			conf := "enabled = [\"claude-code\", \"" + altHarness + "\"]\n\n[" + altHarness + "]\n" +
				altHomeKey + " = " + c.value + "\n"
			if err := os.WriteFile(filepath.Join(home, altMachineConfig), []byte(conf), 0o644); err != nil {
				t.Fatal(err)
			}
			dir, err := altConfigDir()
			if c.refuse != "" {
				if err == nil {
					t.Fatalf("строка %s разобрана в %q, а разбирать её наугад нельзя", c.value, dir)
				}
				if !strings.Contains(err.Error(), c.refuse) {
					t.Fatalf("в отказе нет %q: %v", c.refuse, err)
				}
				// Отказ обязан звать чинить туда, где беда: «подписки не
				// объявлено» увело бы вписывать уже вписанный ключ.
				for _, want := range []string{altMachineConfig, altHomeKey, "[" + altHarness + "]"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("отказ не называет %q: %v", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("строка %s не разобрана: %v", c.value, err)
			}
			if want := filepath.Join(home, c.want); dir != want {
				t.Fatalf("каталог второй подписки %q, жду %q", dir, want)
			}
		})
	}
}

// TestCodeWithoutMachineKey: харнес второй подписки в машинном слое не объявлен,
// значит подписки на этой машине нет, и окно не открывается с догаданным
// каталогом, а отказ несёт готовую строку про то, куда вписать каталог.
func TestCodeWithoutMachineKey(t *testing.T) {
	root, _ := setup(t, "", "")
	t.Setenv("HOME", t.TempDir())
	log := stubEditor(t)
	_, err := cmdCode(root, CodeParams{ID: "XR-002"})
	if err == nil {
		t.Fatal("окно открылось без объявленной второй подписки")
	}
	for _, want := range []string{"вписать " + altHomeKey, "[" + altHarness + "]", altMachineConfig} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("редактор звался без объявленной второй подписки")
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

// TestProbeBeforeSideEffects: пробник подтверждает подписку раньше побочных
// действий. Задача без дерева и мёртвый endpoint: отказ обязан оставить
// репозиторий и доску нетронутыми, иначе --probe защищает только там, где
// защищать уже нечего (ревью DK-175).
func TestProbeBeforeSideEffects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"токен протух"}`))
	}))
	defer srv.Close()

	root, callLog := setup(t, "", "")
	altHome(t, altEnv(srv.URL))
	log := stubEditor(t)
	if _, err := cmdCode(root, CodeParams{ID: "XR-002", Probe: true}); err == nil {
		t.Fatal("мёртвая подписка должна кончаться отказом")
	}
	if wt := gitT(t, root, "worktree", "list"); strings.Contains(wt, "xr-002") {
		t.Errorf("отказ пробника оставил после себя дерево задачи:\n%s", wt)
	}
	if br := gitT(t, root, "branch", "--list", "xr-002"); br != "" {
		t.Errorf("отказ пробника оставил после себя ветку задачи: %q", br)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move XR-002 in-progress") {
		t.Errorf("отказ пробника сдвинул доску в In progress: %q", calls)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("окно открылось при мёртвой подписке")
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

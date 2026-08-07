package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Окно редактора на второй подписке. Клиент claude держит конфиг в каталоге, на
// который смотрит CLAUDE_CONFIG_DIR, а endpoint и токен берёт из секции env его
// settings.json. Отсюда весь рычаг: окно, запущенное с этой переменной, ходит
// во вторую подписку, соседнее окно без неё в первую, и сессии друг о друге не
// знают. Руками рычаг дёргается плохо: переменную надо экспортировать перед
// каждым запуском, а забытый экспорт молча тратит дорогую подписку.

// altConfigHome это каталог конфига второй подписки, путь от домашнего
// каталога. Слой машинный, а не проектный: подписка принадлежит машине, и ключ
// в .devkit/deploy.local каждого репозитория расходился бы копиями одного и
// того же. Гитигнорнутым каталог выходит сам, он лежит вне репозиториев;
// болванку кладёт devkitctl doctor --fix, endpoint и токен вписывает
// пользователь.
const altConfigHome = ".devkit/claude-glm"

const altSettingsName = "settings.json"

// editorBin это команда окна редактора. Имя фиксировано: сменный редактор
// потребовал бы своего ключа в машинном слое, а второго редактора у конвейера
// нет. Флаг -n просит именно новое окно: без него vscode подсунул бы дерево
// задачи вкладкой в уже открытое окно, а там конфиг первой подписки.
const editorBin = "code"

// editorLimit это предел ожидания редактора. Команда code отдаёт работу
// приложению и выходит сразу, так что предел тут только против зависшего
// запуска: без него shipctl молча стоял бы вместо отчёта.
const editorLimit = 2 * time.Minute

// probeLimit это предел ожидания пробника: он ходит в чужой endpoint, и
// висящий запрос превратил бы проверку окружения в зависшую команду.
const probeLimit = 20 * time.Second

// altSub это машинный слой второй подписки, прочитанный из её settings.json.
// Токен лежит нераскрытым полем и наружу не выходит ни строкой отчёта, ни
// текстом ошибки: у него есть только признак «есть» или «нет».
type altSub struct {
	Dir      string // значение CLAUDE_CONFIG_DIR
	Settings string // файл, откуда прочитано
	BaseURL  string
	Model    string
	token    string
	bearer   bool // токен уходит заголовком Authorization, а не x-api-key
}

func (a altSub) tokenState() string {
	if a.token == "" {
		return "нет"
	}
	return "есть"
}

// redact вычищает токен из чужого текста. Тело ответа endpoint печатается в
// ошибке пробника, и запрет на печать токена держится тут, а не надеждой на
// то, что чужая сторона его не вернёт.
func (a altSub) redact(s string) string {
	if a.token == "" {
		return s
	}
	return strings.ReplaceAll(s, a.token, "<токен>")
}

// loadAltSub читает конфиг второй подписки из машинного слоя.
func loadAltSub() (altSub, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return altSub{}, err
	}
	a := altSub{Dir: filepath.Join(home, altConfigHome)}
	a.Settings = filepath.Join(a.Dir, altSettingsName)
	raw, err := os.ReadFile(a.Settings)
	if err != nil {
		if os.IsNotExist(err) {
			return a, fmt.Errorf("нет конфига второй подписки %s: разложить болванку и узнать, каких ключей в ней не хватает: devkitctl doctor --fix", a.Settings)
		}
		return a, err
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return a, fmt.Errorf("%s не читается как json: %v; переразложить болванку: devkitctl doctor --fix", a.Settings, err)
	}
	a.BaseURL = strings.TrimRight(doc.Env["ANTHROPIC_BASE_URL"], "/")
	a.Model = doc.Env["ANTHROPIC_MODEL"]
	// Клиент берёт токен из двух ключей и по-разному кладёт их в запрос,
	// поэтому пробник запоминает, какой именно ключ заполнен.
	if t := doc.Env["ANTHROPIC_AUTH_TOKEN"]; t != "" {
		a.token, a.bearer = t, true
	} else if t := doc.Env["ANTHROPIC_API_KEY"]; t != "" {
		a.token = t
	}
	// Пустой endpoint это не полбеды, а тихий уход окна на первую подписку:
	// claude без ANTHROPIC_BASE_URL ходит туда же, куда обычная сессия.
	if a.BaseURL == "" {
		return a, fmt.Errorf("в %s пустой env.ANTHROPIC_BASE_URL: окно ушло бы на первую подписку молча; вписать endpoint второй подписки, незаполненные ключи называет devkitctl doctor", a.Settings)
	}
	return a, nil
}

type CodeParams struct {
	ID     string
	DryRun bool
	Probe  bool
}

// cmdCode открывает окно редактора на дереве задачи с конфигом второй подписки,
// а с --dry-run печатает то же окружение, ничего не запуская. Дерева ещё нет
// значит завести его тем же путём, что и shipctl start: отдельной команды для
// этого не заводится, иначе у задачи появилось бы два способа получить ветку.
func cmdCode(root string, p CodeParams) (string, error) {
	a, err := loadAltSub()
	if err != nil {
		return "", err
	}
	primary, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	// Корп-контур отбивается сразу: там дерево задачи заводится в клоне кода и
	// называется ключом тикета, а вторая подписка это личный доступ, которому в
	// репозитории компании делать нечего.
	if _, bound := loadTrackerBinding(primary); bound {
		return "", fmt.Errorf("%s это корп-контур: вторая подписка личная, и окно на код компании она не открывает; работать обычной сессией", primary)
	}
	tree, note, err := codeTree(primary, p)
	if err != nil {
		return "", err
	}
	msg := []string{}
	if note != "" {
		msg = append(msg, note)
	}
	msg = append(msg,
		"дерево задачи: "+tree,
		"CLAUDE_CONFIG_DIR="+a.Dir,
		"base URL: "+a.BaseURL,
		"модель: "+altModel(a),
		"токен: "+a.tokenState(),
		"редактор: "+strings.Join(editorArgv(tree), " "))
	if p.Probe {
		line, err := probeAlt(a)
		if err != nil {
			return "", err
		}
		msg = append(msg, line)
	}
	if p.DryRun {
		msg = append(msg, "dry-run: редактор не запускался")
		return strings.Join(msg, "\n"), nil
	}
	if err := openEditor(a, tree); err != nil {
		return "", err
	}
	msg = append(msg, "окно открыто, claude в нём ходит во вторую подписку")
	return strings.Join(msg, "\n"), nil
}

// codeTree выдаёт дерево задачи и приписку про то, откуда оно взялось.
// Dry-run ничего не заводит: он показывает окружение, а не готовит работу, и
// заведённое им дерево пришлось бы прибирать руками.
func codeTree(primary string, p CodeParams) (string, string, error) {
	wt, err := taskWorktree(primary, p.ID)
	if err != nil {
		return "", "", err
	}
	if wt != nil {
		return wt.Path, "", nil
	}
	if p.DryRun {
		return treePath(primary, p.ID), "дерева задачи ещё нет, заведёт запуск без --dry-run (тем же путём, что shipctl start)", nil
	}
	out, err := cmdStart(primary, StartParams{ID: p.ID})
	if err != nil {
		return "", "", err
	}
	wt, err = taskWorktree(primary, p.ID)
	if err != nil {
		return "", "", err
	}
	if wt == nil {
		return "", "", fmt.Errorf("start отработал, а worktree задачи %s не нашёлся:\n%s", p.ID, out)
	}
	return wt.Path, out, nil
}

func editorArgv(tree string) []string {
	return []string{editorBin, "-n", tree}
}

// altModel это модель второй подписки для отчёта: ключа в конфиге может и не
// быть, и молчаливый пропуск строки читался бы как «модель по умолчанию».
func altModel(a altSub) string {
	if a.Model == "" {
		return "не задана (env.ANTHROPIC_MODEL пуст)"
	}
	return a.Model
}

// openEditor запускает окно с подменённым каталогом конфига. Переменная
// ставится только этому процессу: соседние окна и сама сессия shipctl остаются
// на первой подписке.
func openEditor(a altSub, tree string) error {
	bin, err := exec.LookPath(editorBin)
	if err != nil {
		return fmt.Errorf("команда %s не найдена в PATH: окно открывать нечем; поставить её из vscode (палитра команд, Shell Command: Install 'code' command in PATH)", editorBin)
	}
	cmd := exec.Command(bin, "-n", tree)
	cmd.Dir = tree
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+a.Dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(editorLimit)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s не открыл окно: %v (%s)", editorBin, err, strings.TrimSpace(buf.String()))
		}
		return nil
	case <-timer.C:
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("%s не кончился за %s и убит: окно не открылось", editorBin, editorLimit)
	}
}

// probeAlt стучится в endpoint второй подписки Anthropic-совместимым запросом.
// Печать окружения показывает, что окно уйдёт по нужному адресу, но не что там
// живая подписка: протухший токен и опечатка в адресе выглядят на печати
// одинаково исправно, а различает их только ответ.
func probeAlt(a altSub) (string, error) {
	if a.token == "" {
		return "", fmt.Errorf("пробнику нечем представиться: в %s нет токена второй подписки (env.ANTHROPIC_AUTH_TOKEN пуст)", a.Settings)
	}
	if a.Model == "" {
		return "", fmt.Errorf("пробнику нечего спросить: в %s пустой env.ANTHROPIC_MODEL", a.Settings)
	}
	url := a.BaseURL + "/v1/messages"
	body, err := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("пробник не собрал запрос к %s: %v", url, err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.bearer {
		req.Header.Set("authorization", "Bearer "+a.token)
	} else {
		req.Header.Set("x-api-key", a.token)
	}
	resp, err := (&http.Client{Timeout: probeLimit}).Do(req)
	if err != nil {
		return "", fmt.Errorf("пробник не достучался до %s: %v", url, a.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	answer := strings.TrimSpace(a.redact(string(raw)))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("пробник %s: ответ %s, окно уйдёт в нерабочую подписку; проверить endpoint и токен в %s (%s)", url, resp.Status, a.Settings, tail(answer))
	}
	var ans struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &ans)
	if ans.Model == "" {
		return "", fmt.Errorf("пробник %s: ответ 200, а модели в теле нет: endpoint отвечает не по-Anthropic-совместимому (%s)", url, tail(answer))
	}
	return fmt.Sprintf("пробник %s: 200, модель в ответе %s", url, ans.Model), nil
}

package taskhead

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Признаки конца хода, которые понимает оболочка. turn-mark это отметка хука
// Stop в ~/.devkit/turns.log, и с ней голова живёт одной сессией в окне. exit
// значит, что отметки у клиента нет, и конец хода это выход процесса: тогда
// голова идёт печатной чередой даже в окне tmux.
const (
	TurnMark = "turn-mark"
	TurnExit = "exit"
)

// Head это секция [head] профиля харнеса: чем поднимается голова задачи.
// Ключи, которых тут нет, пропускаются молча. Соседние секции и будущие ключи
// этой читает каждый своим кодом, и падать на них подъёму незачем.
type Head struct {
	// Client это интерактивный клиент без заказа. Заказ приставляет оболочка.
	Client []string
	// Bin это имя бинаря, которое ищется в PATH до подъёма. Пусто значит
	// первое слово Client. Поле нужно обёртке: у второй подписки первым
	// словом стоит agentctl, а клиент идёт после `--`.
	Bin string
	// Model это флаги яруса с плейсхолдером {model}, приставляются к Client,
	// когда ярус назван.
	Model []string
	// Session это флаги имени сессии с плейсхолдером {session}. С ними команда
	// сама называет сессию живой головы и пишет её в реестр чатов с адресом
	// окна.
	Session []string
	// Resume это команда продолжения прошлой сессии с плейсхолдером
	// {session}. Её получает человек в тексте громкого зова.
	Resume []string
	// TurnEnd это признак конца хода: TurnMark или TurnExit.
	TurnEnd string
}

// HarnessEnv называет профиль харнеса, когда зовущий имени не дал. Та же
// переменная называет активный харнес у agentctl.
const HarnessEnv = "DEVKIT_HARNESS"

// DefaultHarness это профиль, которым поднимается голова, когда имени нет ни
// у зовущего, ни в HarnessEnv.
const DefaultHarness = "claude-code"

// HarnessName это имя профиля головы: названное зовущим, иначе из HarnessEnv,
// иначе DefaultHarness. Порядок один у taskctl run и у дашборда.
func HarnessName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if name = strings.TrimSpace(os.Getenv(HarnessEnv)); name != "" {
		return name
	}
	return DefaultHarness
}

// ProfilePath называет профиль харнеса name в дереве devkit.
func ProfilePath(devkit, name string) string {
	return filepath.Join(devkit, "kit", "harness", name+".toml")
}

// ReadHead читает секцию [head] профиля. Разбор узкий: строка в двойных
// кавычках и массив таких строк, остального в секции нет. Полный разбор
// профиля живёт в agentctl и devkitctl, и тянуть его сюда ради шести ключей
// значило бы третью копию парсера.
func ReadHead(path string) (Head, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Head{}, fmt.Errorf("нет профиля харнеса %s", path)
		}
		return Head{}, err
	}
	defer f.Close()
	var h Head
	sect, seen := "", false
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && !strings.Contains(line, "=") {
			sect = strings.TrimSpace(line[1 : len(line)-1])
			seen = seen || sect == "head"
			continue
		}
		if sect != "head" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return Head{}, fmt.Errorf("%s:%d: в [head] строка без знака равенства", path, n)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		var err error
		switch key {
		case "client":
			h.Client, err = parseList(val)
		case "model":
			h.Model, err = parseList(val)
		case "session":
			h.Session, err = parseList(val)
		case "resume":
			h.Resume, err = parseList(val)
		case "bin":
			h.Bin, err = parseStr(val)
		case "turn_end":
			h.TurnEnd, err = parseStr(val)
		}
		if err != nil {
			return Head{}, fmt.Errorf("%s:%d: [head] %s: %v", path, n, key, err)
		}
	}
	if err := sc.Err(); err != nil {
		return Head{}, err
	}
	if !seen {
		return Head{}, fmt.Errorf("в профиле %s нет секции [head]: чем поднимать голову задачи, не сказано", path)
	}
	if len(h.Client) == 0 {
		return Head{}, fmt.Errorf("в профиле %s в [head] нет ключа client", path)
	}
	if h.Bin == "" {
		h.Bin = h.Client[0]
	}
	switch h.TurnEnd {
	case "":
		h.TurnEnd = TurnMark
	case TurnMark, TurnExit:
	default:
		return Head{}, fmt.Errorf("в профиле %s [head] turn_end = %q, а знакомы %s и %s", path, h.TurnEnd, TurnMark, TurnExit)
	}
	return h, nil
}

// Command собирает клиента головы: Client, флаги яруса, если назван model, и
// флаги имени сессии, если назван session.
func (h Head) Command(model, session string) []string {
	cmd := append([]string{}, h.Client...)
	if model != "" {
		cmd = append(cmd, fill(h.Model, "{model}", model)...)
	}
	if session != "" {
		cmd = append(cmd, fill(h.Session, "{session}", session)...)
	}
	return cmd
}

// ResumeCommand это команда продолжения сессии sid. Пусто, когда профиль её
// не называет.
func (h Head) ResumeCommand(sid string) []string {
	if len(h.Resume) == 0 || sid == "" {
		return nil
	}
	return fill(h.Resume, "{session}", sid)
}

func fill(list []string, mark, val string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = strings.ReplaceAll(s, mark, val)
	}
	return out
}

func parseStr(v string) (string, error) {
	s, rest, err := takeStr(v)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", fmt.Errorf("после строки лишнее: %s", rest)
	}
	return s, nil
}

func parseList(v string) ([]string, error) {
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, fmt.Errorf("жду массив строк, а пришло %s", v)
	}
	body := strings.TrimSpace(v[1 : len(v)-1])
	var out []string
	for body != "" {
		s, rest, err := takeStr(body)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, ",") {
			return nil, fmt.Errorf("между строками массива нет запятой: %s", rest)
		}
		body = strings.TrimSpace(rest[1:])
	}
	return out, nil
}

// takeStr снимает с начала v строку в двойных кавычках. Экранированы в ней
// только кавычка и обратная косая черта, других в профилях нет.
func takeStr(v string) (string, string, error) {
	if !strings.HasPrefix(v, `"`) {
		return "", "", fmt.Errorf("жду строку в двойных кавычках, а пришло %s", v)
	}
	var b strings.Builder
	for i := 1; i < len(v); i++ {
		switch c := v[i]; c {
		case '\\':
			if i+1 >= len(v) {
				return "", "", fmt.Errorf("строка оборвана на обратной черте: %s", v)
			}
			i++
			b.WriteByte(v[i])
		case '"':
			return b.String(), v[i+1:], nil
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("строка без закрывающей кавычки: %s", v)
}

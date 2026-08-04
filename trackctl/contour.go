package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Секции таблицы статусов. Первые четыре это секции доски, пятая держит
// конечные статусы: своей секции доски у done нет, закрытые строки живут в
// архиве. Без отдельной секции закрытие было бы неотличимо от дырки в
// конфиге, и каждый закрытый тикет шумел бы «статус не расписан».
const (
	sectBacklog    = "backlog"
	sectInProgress = "in_progress"
	sectCheck      = "check"
	sectBlocked    = "blocked"
	sectDone       = "done"
)

var statusSections = []string{sectBacklog, sectInProgress, sectCheck, sectBlocked, sectDone}

// Как секция зовётся в доске: сообщения говорят про доску словами доски.
var boardNames = map[string]string{
	sectBacklog:    "Backlog",
	sectInProgress: "In progress",
	sectCheck:      "Check",
	sectBlocked:    "Blocked",
}

// contour это машинный контур ~/.devkit/tracker/<имя>.local: свойство
// компании, один на все её репозитории. Формат тот же, что у профилей
// харнесов, подмножество TOML. Токен в файле не лежит, ключ называет
// переменную окружения либо файл, откуда его читать.
type contour struct {
	Name      string
	Path      string
	Adapter   string
	BaseURL   string
	TokenEnv  string
	TokenFile string
	User      string
	CostS     string
	CostM     string
	CostL     string
	RankField string
	// Status это секции статусов: секция доски -> её статусы в трекере.
	// Первый элемент списка целевой для записи, остальные синонимы чтения.
	Status map[string][]string
	// Fields это обязательные поля переходов из плоских секций [fields_*]:
	// секция доски -> поле адаптера -> значение.
	Fields map[string]map[string]string
}

func contourDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".devkit", "tracker"), nil
}

func contourPath(name string) (string, error) {
	dir, err := contourDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".local"), nil
}

// loadContour читает контур по имени из привязки.
func loadContour(name string) (*contour, error) {
	path, err := contourPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("нет контура %q: жду файл %s", name, path)
		}
		return nil, err
	}
	doc, err := parseTOML(filepath.Base(path), string(data))
	if err != nil {
		return nil, err
	}
	c := &contour{
		Name:   name,
		Path:   path,
		Status: map[string][]string{},
		Fields: map[string]map[string]string{},
	}
	top := doc.table("")
	c.Adapter = top.str("adapter")
	c.BaseURL = top.str("base_url")
	c.TokenEnv = top.str("token_env")
	c.TokenFile = top.str("token_file")
	c.User = top.str("user")
	c.CostS = top.str("cost_s")
	c.CostM = top.str("cost_m")
	c.CostL = top.str("cost_l")
	c.RankField = top.str("rank_field")
	if c.Adapter == "" {
		return nil, fmt.Errorf("%s: не задан adapter", path)
	}
	if c.User == "" {
		return nil, fmt.Errorf("%s: не задан user, без него нечего передать в assign", path)
	}
	if st := doc.table("status"); st != nil {
		for _, key := range st.Keys {
			if !knownSection(key) {
				return nil, fmt.Errorf("%s: в [status] секция %q не из доски, жду одну из: %s",
					path, key, strings.Join(statusSections, ", "))
			}
			vals := st.arr(key)
			if len(vals) == 0 {
				return nil, fmt.Errorf("%s: [status].%s пуст, жду массив статусов трекера", path, key)
			}
			c.Status[key] = vals
		}
	}
	for _, name := range doc.Order {
		if !strings.HasPrefix(name, "fields_") {
			continue
		}
		sect := strings.TrimPrefix(name, "fields_")
		if !knownSection(sect) || sect == sectDone {
			return nil, fmt.Errorf("%s: секция [%s] не про секцию доски, жду одну из: %s",
				path, name, strings.Join(statusSections[:len(statusSections)-1], ", "))
		}
		t := doc.table(name)
		vals := map[string]string{}
		for _, k := range t.Keys {
			vals[k] = t.str(k)
		}
		c.Fields[sect] = vals
	}
	return c, nil
}

func knownSection(name string) bool {
	for _, s := range statusSections {
		if s == name {
			return true
		}
	}
	return false
}

// token достаёт токен трекера тем способом, который назвал контур. Сам токен в
// конфиг не пишется никогда, тем же порядком, что доступы в access.local.md.
func (c *contour) token() (string, error) {
	switch {
	case c.TokenEnv != "":
		v := os.Getenv(c.TokenEnv)
		if v == "" {
			return "", fmt.Errorf("переменная %s пуста, а из неё контур %s берёт токен", c.TokenEnv, c.Name)
		}
		return v, nil
	case c.TokenFile != "":
		data, err := os.ReadFile(expandHome(c.TokenFile))
		if err != nil {
			return "", fmt.Errorf("не прочитал токен из %s: %v", c.TokenFile, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("контур %s не назвал, откуда брать токен: жду token_env либо token_file", c.Name)
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// binding это проектная привязка .devkit/tracker.local в боковой директории:
// свойство проекта, а не компании. Формат плоский, как у deploy.local, потому
// что привязку читает и shipctl, а парсера TOML у него нет.
type binding struct {
	Path    string
	Contour string
	Key     string
	Branch  string
	Repo    string
}

const bindingPath = ".devkit/tracker.local"

func loadBinding(root string) (*binding, error) {
	path := filepath.Join(root, bindingPath)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("нет привязки к трекеру: жду файл %s с ключами contour и key", path)
		}
		return nil, err
	}
	defer f.Close()
	b := &binding{Path: path}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		switch key {
		case "contour":
			b.Contour = val
		case "key":
			b.Key = val
		case "branch":
			b.Branch = val
		case "repo":
			b.Repo = val
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if b.Contour == "" {
		return nil, fmt.Errorf("%s: не задан contour, а по нему ищется ~/.devkit/tracker/<имя>.local", path)
	}
	if b.Key == "" {
		return nil, fmt.Errorf("%s: не задан key, ключ проекта в трекере", path)
	}
	return b, nil
}

// unquote снимает кавычки со значения плоского конфига, как в shipctl.
func unquote(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// branchName разворачивает шаблон ветки привязки. Шаблон читает и shipctl,
// поэтому подстановок ровно две: ключ тикета и хвост-слаг.
func (b *binding) branchName(key, slug string) string {
	tpl := b.Branch
	if tpl == "" {
		tpl = "{key}-{slug}"
	}
	out := strings.ReplaceAll(tpl, "{key}", key)
	return strings.TrimRight(strings.ReplaceAll(out, "{slug}", slug), "-")
}

// contourNames перечисляет контуры, лежащие на машине: status показывает их,
// когда привязка называет несуществующий.
func contourNames() []string {
	dir, err := contourDir()
	if err != nil {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".local") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".local"))
	}
	sort.Strings(out)
	return out
}

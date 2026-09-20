// Package spend читает расход токенов из транскриптов харнеса claude. Харнес
// пишет журнал сессии в ~/.claude/projects/<слепок пути>/<сессия>.jsonl, а
// потоки субагентов рядом, в каталоге <сессия>/subagents/agent-<работа>.jsonl.
// На каждом ходу ассистента лежит поле usage с выводом, свежим входом и
// кэшем, и по нему считается, во что обошёлся этап задачи: номер работы
// субагента запись этапа несёт с DK-911. Разбор в docs/tasks/DK-912.md.
//
// Тот же разбор с теми же полями и тем же дедупом по requestId живёт в python
// (tools/devkitctl/context.py), и повторён он здесь намеренно: taskctl по
// правилу раскладки (README.md, раздел «Раскладка») инструмент go, а вызов
// python из команды в PATH ломает раскладку сильнее, чем второй разбор json.
package spend

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Usage это расход одного потока: ходы и четыре статьи токенов. Ход это один
// запрос к модели, а не запись журнала: запись с usage повторяется по числу
// итераций ответа, и все копии несут одинаковые числа.
type Usage struct {
	Turns      int
	Output     int
	Input      int
	CacheRead  int
	CacheWrite int
}

// Add складывает расход двух потоков.
func (u Usage) Add(v Usage) Usage {
	return Usage{
		Turns:      u.Turns + v.Turns,
		Output:     u.Output + v.Output,
		Input:      u.Input + v.Input,
		CacheRead:  u.CacheRead + v.CacheRead,
		CacheWrite: u.CacheWrite + v.CacheWrite,
	}
}

// Empty отвечает, пуст ли расход: по такому потоку печатать нечего.
func (u Usage) Empty() bool { return u == Usage{} }

// Dir это корень журналов харнеса claude в доме.
func Dir(home string) string { return filepath.Join(home, ".claude", "projects") }

// WorkFile ищет поток работы субагента. Сначала по сессии, которая этап
// писала: её каталог назван UUID сессии, и это одна проверка пути вместо
// обхода. Без сессии и при промахе идёт глоб по каталогам проектов: работа
// исполнителя ведётся из дерева задачи, а пакет этапов закрывает основной
// чекаут, и слепок пути у них разный. Каталоги при этом только перебираются,
// журналы не читаются: обход всех транскриптов машины это полтора гигабайта.
func WorkFile(home, session, work string) (string, bool) {
	if home == "" || work == "" {
		return "", false
	}
	name := "agent-" + work + ".jsonl"
	if session != "" {
		got, _ := filepath.Glob(filepath.Join(Dir(home), "*", session, "subagents", name))
		if len(got) > 0 {
			return got[0], true
		}
	}
	got, _ := filepath.Glob(filepath.Join(Dir(home), "*", "*", "subagents", name))
	if len(got) > 0 {
		return got[0], true
	}
	return "", false
}

// SessionFile ищет поток головной сессии: он лежит рядом с её каталогом
// субагентов и назван тем же UUID.
func SessionFile(home, session string) (string, bool) {
	if home == "" || session == "" {
		return "", false
	}
	got, _ := filepath.Glob(filepath.Join(Dir(home), "*", session+".jsonl"))
	if len(got) > 0 {
		return got[0], true
	}
	return "", false
}

// turn это запись журнала, из которой берётся расход. Остальные поля записи
// (текст реплики, вызовы инструментов, результаты) сюда не приезжают вовсе:
// наружу свод отдаёт суммы, и держать тела реплик в памяти незачем.
type turn struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	UUID      string `json:"uuid"`
	Message   struct {
		Usage *struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// maxLine это потолок одной строки журнала. Ход с длинным результатом
// инструмента доходит до мегабайтов, и обычного буфера чтения на него не
// хватает.
const maxLine = 64 << 20

// Read считает расход одного потока. Запись с usage повторяется по числу
// итераций ответа, поэтому ходы дедуплицируются по requestId: без склейки
// объём сессии вырос бы в разы. Битая строка пропускается молча: харнес пишет
// журнал на ходу, и обрыв записи при отмене сессии не повод рвать свод.
func Read(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()
	return read(f)
}

func read(r io.Reader) (Usage, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	seen := map[string]bool{}
	var out Usage
	for {
		line, err := readLine(br)
		if line != "" {
			var t turn
			if json.Unmarshal([]byte(line), &t) == nil && t.Type == "assistant" && t.Message.Usage != nil {
				key := t.RequestID
				if key == "" {
					key = t.UUID
				}
				u := t.Message.Usage
				if !seen[key] && u.Input+u.Output+u.CacheRead+u.CacheWrite > 0 {
					seen[key] = true
					out.Turns++
					out.Output += u.Output
					out.Input += u.Input
					out.CacheRead += u.CacheRead
					out.CacheWrite += u.CacheWrite
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// readLine отдаёт строку журнала целиком, хоть в мегабайт, и обрезает всё,
// что длиннее потолка: хвост такой строки данных о расходе не несёт.
func readLine(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, err := br.ReadString('\n')
		if b.Len() < maxLine {
			b.WriteString(chunk)
		}
		if err != nil || strings.HasSuffix(chunk, "\n") {
			return strings.TrimSpace(b.String()), err
		}
	}
}

// meta это спутник потока субагента: рядом с agent-<работа>.jsonl харнес
// кладёт agent-<работа>.meta.json с определением агента, глубиной спавна и
// номером работы родителя. Вложенность собирается по нему, а не разбором
// ответов инструмента Agent в потоке родителя: родитель назван прямо, поиск
// по телу реплик не нужен и цепочка не рвётся, когда ответ родителя не дошёл.
type meta struct {
	Work      string
	AgentType string `json:"agentType"`
	Parent    string `json:"parentAgentId"`
	ToolUseID string `json:"toolUseId"`
	Depth     int    `json:"spawnDepth"`
}

// Node это работа субагента со своим расходом и поднятыми из неё работами.
type Node struct {
	Work  string
	Kind  string
	Usage Usage
	Kids  []Node
}

// Total это расход работы вместе со всеми вложенными: вычитка, поднятая
// исполнителем, ложится в тот этап, который её поднял.
func (n Node) Total() Usage {
	out := n.Usage
	for _, k := range n.Kids {
		out = out.Add(k.Total())
	}
	return out
}

// Tree собирает работу с вложенными. Каталог субагентов читается один раз на
// работу: метаданные там мелкие, а журналы читаются только те, что вошли в
// дерево.
func Tree(home, session, work string) (Node, bool) {
	path, ok := WorkFile(home, session, work)
	if !ok {
		return Node{}, false
	}
	dir := filepath.Dir(path)
	metas := readMetas(dir)
	return node(dir, work, metas, map[string]bool{}), true
}

func node(dir, work string, metas map[string]meta, seen map[string]bool) Node {
	seen[work] = true
	n := Node{Work: work, Kind: metas[work].AgentType}
	u, err := Read(filepath.Join(dir, "agent-"+work+".jsonl"))
	if err == nil {
		n.Usage = u
	}
	for _, kid := range childrenOf(dir, work, metas) {
		if seen[kid] {
			continue
		}
		n.Kids = append(n.Kids, node(dir, kid, metas, seen))
	}
	return n
}

// childrenOf называет работы, поднятые названной. Первый признак это поле
// родителя в метаданных. Старые метаданные его не несут, и там работа
// узнаётся по номеру вызова инструмента: он стоит в потоке родителя, и
// поиск идёт по одному файлу, а не по каталогу.
func childrenOf(dir, work string, metas map[string]meta) []string {
	var byParent, byTool []string
	for _, m := range metas {
		switch {
		case m.Parent == work:
			byParent = append(byParent, m.Work)
		case m.Parent == "" && m.Depth > 1 && m.ToolUseID != "":
			byTool = append(byTool, m.Work)
		}
	}
	if len(byTool) > 0 {
		body, err := os.ReadFile(filepath.Join(dir, "agent-"+work+".jsonl"))
		if err == nil {
			for _, kid := range byTool {
				if strings.Contains(string(body), metas[kid].ToolUseID) {
					byParent = append(byParent, kid)
				}
			}
		}
	}
	sortStrings(byParent)
	return byParent
}

// readMetas читает спутники всех работ каталога. Нечитаемый спутник это
// работа без родителя, а не провал свода.
func readMetas(dir string) map[string]meta {
	out := map[string]meta{}
	paths, _ := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m meta
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		m.Work = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "agent-"), ".meta.json")
		out[m.Work] = m
	}
	return out
}

// sortStrings держит порядок работ устойчивым: свод печатается человеку, и
// порядок строк не должен зависеть от обхода карты.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

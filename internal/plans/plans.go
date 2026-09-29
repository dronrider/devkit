// Package plans читает набор шаблонов плана работ (LLD DK-972, решения 1, 2 и
// 9). Шаблон это файл подмножества TOML: шапка описывает шаблон, каждая секция
// это этап, порядок секций это порядок этапов. Набор лежит двумя слоями,
// встроенным kit/plans и проектным .devkit/plans, и одноимённый файл проекта
// замещает встроенный целиком.
package plans

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/subtoml"
)

// DirName это имя каталога набора в обоих слоях.
const DirName = "plans"

// Ворота, которые умеет спрашивать движок (решение 7). Шаблон называет их в
// ключе gate, и незнакомое слово тут это ошибка, а не предупреждение: ворота
// с опечаткой не спросит никто, а этап будет выглядеть закрытым.
var knownGates = []string{"check", "close", "merge", "ready", "push"}

// Виды приёмки строки доски (LLD DK-292), третья ось условия этапа.
var knownAccepts = []string{"agent", "mixed", "user"}

// Кто ведёт этап. Значения те же, что читает сборка плана и хук спавна.
var knownBy = []string{"сам", "субагент", "человек"}

var headKeys = []string{"name", "title", "types", "dropped"}

var stageKeys = []string{"title", "skill", "by", "agent", "trace", "gate",
	"types", "paths", "accept", "slot", "scenario"}

// Stage это этап шаблона. Ключ секции латинский, наружу идёт Title.
type Stage struct {
	Key      string
	Title    string
	Skill    string
	By       string
	Agent    string
	Trace    string
	Gate     []string
	Types    []string
	Paths    []string
	Accept   []string
	Slot     bool
	Scenario string
}

// Dropped это снятый встроенный этап с причиной (решение 9).
type Dropped struct {
	Name string
	Why  string
}

// Template это разобранный файл шаблона.
type Template struct {
	Name    string
	Title   string
	Types   []string
	Dropped []Dropped
	Stages  []Stage
	File    string
	Project bool
	Warns   []string
}

// Stage находит этап по ключу секции.
func (t *Template) Stage(key string) (Stage, bool) {
	for _, s := range t.Stages {
		if s.Key == key {
			return s, true
		}
	}
	return Stage{}, false
}

// DroppedWhy отвечает, снят ли этап, и с какой причиной.
func (t *Template) DroppedWhy(name string) (string, bool) {
	for _, d := range t.Dropped {
		if d.Name == name {
			return d.Why, true
		}
	}
	return "", false
}

// Parse разбирает текст шаблона. Имя идёт в сообщения об ошибках и сверяется с
// ключом name: файл, переименованный копированием, иначе достался бы чужому
// типу задачи молча.
func Parse(name, text string) (*Template, error) {
	base := strings.TrimSuffix(filepath.Base(name), ".toml")
	d, err := subtoml.Parse(filepath.Base(name), text)
	if err != nil {
		return nil, err
	}
	t := &Template{Name: base}
	head := d.Table("")
	if v := head.Str("name"); v != "" && v != base {
		return nil, fmt.Errorf("%s: name = %s, а файл называется %s: имя шаблона это имя файла",
			filepath.Base(name), subtoml.Quote(v), subtoml.Quote(base))
	}
	t.Title = head.Str("title")
	if t.Title == "" {
		return nil, fmt.Errorf("%s: нет title: по нему шаблон выбирают в plan templates", filepath.Base(name))
	}
	t.Types = head.Arr("types")
	for _, raw := range head.Arr("dropped") {
		who, why, ok := strings.Cut(raw, ":")
		who = strings.TrimSpace(who)
		why = strings.TrimSpace(why)
		if !ok || who == "" || why == "" {
			return nil, fmt.Errorf("%s: dropped = %s без причины: жду \"<этап>: <причина>\"",
				filepath.Base(name), subtoml.Quote(raw))
		}
		t.Dropped = append(t.Dropped, Dropped{Name: who, Why: why})
	}
	t.Warns = append(t.Warns, unknownWarns(filepath.Base(name), head.Keys, headKeys, "")...)
	slots := 0
	for _, sec := range d.Order {
		if sec == "" {
			continue
		}
		tab := d.Table(sec)
		st := Stage{
			Key:      sec,
			Title:    tab.Str("title"),
			Skill:    tab.Str("skill"),
			By:       tab.Str("by"),
			Agent:    tab.Str("agent"),
			Trace:    tab.Str("trace"),
			Gate:     tab.Arr("gate"),
			Types:    tab.Arr("types"),
			Paths:    tab.Arr("paths"),
			Accept:   tab.Arr("accept"),
			Scenario: tab.Str("scenario"),
		}
		if v, ok := tab.Get("slot"); ok && v.Kind == subtoml.KindBool {
			st.Slot = v.Bool
		}
		if st.Slot {
			slots++
		}
		if err := checkStage(filepath.Base(name), st); err != nil {
			return nil, err
		}
		t.Warns = append(t.Warns, unknownWarns(filepath.Base(name), tab.Keys, stageKeys, sec)...)
		t.Stages = append(t.Stages, st)
	}
	if len(t.Stages) == 0 {
		return nil, fmt.Errorf("%s: этапов нет: шаблон без секций плана не собирает", filepath.Base(name))
	}
	if slots > 1 {
		return nil, fmt.Errorf("%s: slot = true стоит у %d этапов, а свои пункты агента ложатся в один",
			filepath.Base(name), slots)
	}
	return t, nil
}

func checkStage(file string, s Stage) error {
	where := fmt.Sprintf("%s: [%s]", file, s.Key)
	if s.Title == "" {
		return fmt.Errorf("%s нет title: русским именем этап зовут план, ворота и пометка исключения", where)
	}
	if s.By == "" {
		return fmt.Errorf("%s нет by: жду %s", where, strings.Join(knownBy, ", "))
	}
	if !inList(knownBy, s.By) {
		return fmt.Errorf("%s by = %s, допустимы %s", where, subtoml.Quote(s.By), strings.Join(knownBy, ", "))
	}
	if s.Trace == "" {
		return fmt.Errorf("%s нет trace: этап без следа пишется trace = \"слово\"", where)
	}
	for _, g := range s.Gate {
		if !inList(knownGates, g) {
			return fmt.Errorf("%s gate = %s, ворота известны эти: %s", where, subtoml.Quote(g),
				strings.Join(knownGates, ", "))
		}
	}
	for _, a := range s.Accept {
		if !inList(knownAccepts, a) {
			return fmt.Errorf("%s accept = %s, виды приёмки эти: %s", where, subtoml.Quote(a),
				strings.Join(knownAccepts, ", "))
		}
	}
	return nil
}

// unknownWarns ловит опечатку в ключе: значение такого ключа не читает никто, и
// молчание тут неотличимо от работающей настройки.
func unknownWarns(file string, keys, known []string, section string) []string {
	var out []string
	for _, k := range keys {
		if inList(known, k) {
			continue
		}
		where := "на верхнем уровне"
		if section != "" {
			where = "в секции [" + section + "]"
		}
		out = append(out, fmt.Sprintf("%s: незнакомый ключ %s %s, пропущен", file, subtoml.Quote(k), where))
	}
	return out
}

func inList(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Set это склеенный набор: встроенный слой, поверх него проектный.
type Set struct {
	Names     []string
	Templates map[string]*Template
	Warns     []string
}

// Get достаёт шаблон по имени.
func (s *Set) Get(name string) (*Template, bool) {
	if s == nil {
		return nil, false
	}
	t, ok := s.Templates[name]
	return t, ok
}

// ByType называет шаблон, которому тип строки доски достаётся без указания.
// Первый по алфавиту среди подходящих: двух шаблонов на один тип в наборе быть
// не должно, а если организация их завела, выбор всё равно обязан быть
// одинаковым от прогона к прогону.
func (s *Set) ByType(kind string) (*Template, bool) {
	if s == nil || kind == "" {
		return nil, false
	}
	for _, n := range s.Names {
		t := s.Templates[n]
		if inList(t.Types, kind) {
			return t, true
		}
	}
	return nil, false
}

// ReadDir читает каталог шаблонов. Пропавший каталог это пустой слой, а не
// ошибка: проект без своих шаблонов живёт на встроенном наборе.
func ReadDir(dir string, project bool) ([]*Template, []string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var out []*Template
	var warns []string
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		file := filepath.Join(dir, n)
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		t, err := Parse(n, string(data))
		if err != nil {
			if project && len(data) == 0 {
				// Пустой файл проектного слоя в момент чтения (полпути записи,
				// например `--dump ... > .devkit/plans/x.toml` сам truncate-ит
				// цель до запуска agentctl) не должен ронять набор целиком:
				// встроенный шаблон того же имени остаётся рабочим запасным
				// вариантом, а находка это дело doctor. Непустой файл с
				// опечаткой (не title, не by, не gate) остаётся отказом: по
				// развилке задачи такую ошибку конфигурации молчание маскирует,
				// а не чинит.
				warns = append(warns, fmt.Sprintf("%s: пуст, использован встроенный: %v", n, err))
				continue
			}
			return nil, nil, err
		}
		t.File = file
		t.Project = project
		out = append(out, t)
		warns = append(warns, t.Warns...)
	}
	return out, warns, nil
}

// Load склеивает слои по имени файла: проектный шаблон замещает встроенный
// целиком, а шаблон, которого во встроенном слое нет, добавляется к набору.
func Load(kitDir, projectDir string) (*Set, error) {
	s := &Set{Templates: map[string]*Template{}}
	for _, layer := range []struct {
		dir     string
		project bool
	}{{kitDir, false}, {projectDir, true}} {
		if layer.dir == "" {
			continue
		}
		list, warns, err := ReadDir(layer.dir, layer.project)
		if err != nil {
			return nil, err
		}
		s.Warns = append(s.Warns, warns...)
		for _, t := range list {
			if _, ok := s.Templates[t.Name]; !ok {
				s.Names = append(s.Names, t.Name)
			}
			s.Templates[t.Name] = t
		}
	}
	sort.Strings(s.Names)
	return s, nil
}

// KitDir ищет встроенный слой тем же порядком, что профили харнесов: явная
// переменная, симлинк .devkit/devkit проекта (DK-193), корень проекта, соседний
// клон и путь из README.
func KitDir(root, home string) string {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, filepath.Join(v, "kit", DirName))
	}
	if root != "" {
		cands = append(cands, filepath.Join(root, ".devkit", "devkit", "kit", DirName),
			filepath.Join(root, "kit", DirName),
			filepath.Join(filepath.Dir(root), "devkit", "kit", DirName))
	}
	if home != "" {
		cands = append(cands, filepath.Join(home, "projects", "devkit", "kit", DirName))
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

// ProjectDir это проектный слой: коммитимый каталог рядом с review.conf.
func ProjectDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".devkit", DirName)
}

// Findings это находки доктора по решению 9: проектная копия встроенного
// шаблона обязана нести каждый встроенный этап либо секцией, либо записью
// dropped с причиной. Причину проверяет разбор, а отставание копии видно только
// сверкой со встроенным файлом того же имени.
func Findings(kitDir, projectDir string) []string {
	var out []string
	kit, kitWarns, err := ReadDir(kitDir, false)
	if err != nil {
		return []string{fmt.Sprintf("встроенные шаблоны планов не прочитаны: %v", err)}
	}
	proj, projWarns, err := ReadDir(projectDir, true)
	if err != nil {
		return []string{fmt.Sprintf("шаблоны планов проекта не прочитаны: %v", err)}
	}
	for _, w := range append(kitWarns, projWarns...) {
		out = append(out, "шаблон плана: "+w)
	}
	byName := map[string]*Template{}
	for _, t := range kit {
		byName[t.Name] = t
	}
	for _, p := range proj {
		base, ok := byName[p.Name]
		if !ok {
			continue
		}
		for _, s := range base.Stages {
			if _, ok := p.Stage(s.Key); ok {
				continue
			}
			if _, ok := p.DroppedWhy(s.Key); ok {
				continue
			}
			out = append(out, fmt.Sprintf("шаблон плана %s: копия проекта отстала, этапа [%s] (%s) в ней нет ни секцией, ни в dropped; вписать этап либо снять с причиной: dropped = [\"%s: <причина>\"]",
				p.Name, s.Key, s.Title, s.Key))
		}
	}
	return out
}

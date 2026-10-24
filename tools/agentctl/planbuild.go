package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/accept"
	"github.com/dronrider/devkit/internal/plans"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/trace"
)

// Сборка плана из шаблона (LLD DK-972, решения 4, 5 и 8): три пути к шаблону,
// слот для своих пунктов агента и след, который спрашивает done. Формат файла
// и адресация плана лежат в plan.go, набор шаблонов печатает plantpl.go.

// planNoTemplate это шапка плана, собранного из одних своих пунктов: шаблон не
// выбран ни флагом, ни задачей, и слово в шапке честнее пустого, его же
// печатает дашборд рядом с кольцом.
const planNoTemplate = "без шаблона"

// planSufRe разбирает суффикс заголовка строки «[план: <имя>]»: он называет
// шаблон плана явно, минуя выбор по типу. Разбор тот же, что у planSufRe в
// taskctl dep.go, ставит и снимает суффикс taskctl plan.
var planSufRe = regexp.MustCompile(`\s*\[план: ([^\]]+)\]\s*$`)

// planTailRe снимает хвосты «[провал: ...]» и «[блок: ...]», стоящие в
// заголовке позже плана и не пускающие якорную регулярку к суффиксу.
var planTailRe = regexp.MustCompile(`\s*\[(?:провал|блок): [^|\[]*\]\s*$`)

// planSuffix читает имя шаблона из заголовка строки. Хвосты снимаются с конца
// тем же порядком, что и в splitTitle taskctl, чтобы суффикс, стоящий перед
// приёмкой и провалом, находился при любом наборе хвостов.
func planSuffix(title string) string {
	rest := title
	for {
		m := planTailRe.FindString(rest)
		if m == "" {
			break
		}
		rest = rest[:len(rest)-len(m)]
	}
	if m := accept.SufRe.FindString(rest); m != "" {
		rest = rest[:len(rest)-len(m)]
	}
	if m := planSufRe.FindStringSubmatch(rest); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// planTask называет задачу доски, к которой относится план: её несёт метка
// субагента (<ID>-<роль>) либо запись реестра чатов самой сессии (DK-430).
// Пустая строка значит сессию без задачи. Задачу из метки узнаёт строка доски:
// метка начинается с ID задачи, и перебор строк надёжнее разбора метки на
// части. Имени tmux-сессии реестр хозяина тоже знает: план по нему пишут
// сессии без собственного ID (DK-269).
func planTask(start, home string, a planAddr) string {
	if a.label != "" {
		if root, err := findRoot(start); err == nil {
			if rows, err := loadRows(root); err == nil {
				for _, r := range rows {
					if a.label == r.ID || strings.HasPrefix(a.label, r.ID+"-") {
						return r.ID
					}
				}
			}
		}
	}
	if a.sid == "" {
		return ""
	}
	binds := sessions.Load(home)
	if rec, ok := binds[a.sid]; ok {
		return rec.Task
	}
	if a.tmux {
		if sid := sessions.TmuxOwnerSince(sessions.LoadAll(home), a.sid, ""); sid != "" {
			return binds[sid].Task
		}
	}
	return ""
}

// planChoose решает, из какого шаблона собирать план, тремя путями, и первый
// подходящий побеждает (решение 4): флаг --template, затем суффикс строки
// задачи и её тип, затем «без шаблона». nil у шаблона значит «без шаблона».
func planChoose(start, tplName, task string) (string, *plans.Template, error) {
	if tplName != "" {
		set, err := planLoadSet(start)
		if err != nil {
			return "", nil, err
		}
		t, ok := set.Get(tplName)
		if !ok {
			return "", nil, fmt.Errorf("шаблона %q в наборе нет, есть %s", tplName, strings.Join(set.Names, ", "))
		}
		return tplName, t, nil
	}
	if task == "" {
		return planNoTemplate, nil, nil
	}
	root, err := findRoot(start)
	if err != nil {
		return planNoTemplate, nil, nil
	}
	rows, err := loadRows(root)
	if err != nil {
		return planNoTemplate, nil, nil
	}
	row := rowOf(rows, task)
	if row == nil {
		return planNoTemplate, nil, nil
	}
	set, err := planLoadSet(start)
	if err != nil {
		return "", nil, err
	}
	if name := planSuffix(row.Title); name != "" {
		t, ok := set.Get(name)
		if !ok {
			return "", nil, fmt.Errorf("у %s стоит суффикс [план: %s], а шаблона с таким именем в наборе нет (есть %s): снять суффикс командой taskctl plan %s --off либо добавить шаблон в набор", task, name, strings.Join(set.Names, ", "), task)
		}
		return name, t, nil
	}
	// Тип пишется в строке с большой буквы («LLD»), а набор называет его
	// строчными, поэтому сначала точное совпадение, затем строчное.
	if t, ok := set.ByType(row.Type); ok {
		return t.Name, t, nil
	}
	if t, ok := set.ByType(strings.ToLower(row.Type)); ok {
		return t.Name, t, nil
	}
	// Составной и незнакомый тип падает на task: обычный порядок работы у
	// задачи без особенностей честнее «без шаблона» без всяких этапов.
	if t, ok := set.Get("task"); ok {
		return t.Name, t, nil
	}
	return planNoTemplate, nil, nil
}

// planAssemble собирает план из шаблона (решение 5): этапы стоят порядком
// файла, свои пункты агента ложатся на место этапа-слота и наследуют его след.
// Строка, совпавшая с именем этапа, считается этим этапом и не удваивается:
// агент, печатающий план целиком, не плодит дубли. Этап с условием по путям
// стоит с хвостом «при правке <маска>» (решение 8). Пустой набор своих
// пунктов оставляет слот одним пунктом с именем этапа.
func planAssemble(t *plans.Template, own []planItem) []planItem {
	stageText := func(s plans.Stage) string {
		if len(s.Paths) > 0 {
			return s.Title + " при правке " + strings.Join(s.Paths, ", ")
		}
		return s.Title
	}
	dup := make([]bool, len(own))
	for i, o := range own {
		for _, s := range t.Stages {
			if planSame(o.Text, s.Title) || planSame(o.Text, stageText(s)) {
				dup[i] = true
				break
			}
		}
	}
	var out []planItem
	for _, s := range t.Stages {
		item := planItem{Text: stageText(s), State: "pending", Stage: s.Key, Trace: s.Trace}
		if !s.Slot {
			out = append(out, item)
			continue
		}
		placed := false
		for i := range own {
			if dup[i] {
				continue
			}
			placed = true
			out = append(out, planItem{Text: own[i].Text, State: "pending", Stage: s.Key, Trace: s.Trace})
		}
		if !placed {
			out = append(out, item)
		}
	}
	return out
}

// planDoneTrace закрывает пункт плана по следу: этап со следом проходит done
// только при следе, след, живущий в файле задачи, у сессии без задачи
// закрывается словом с печатью об этом, а этап без следа называется «на слове
// агента» (решения 4 и 6). Этап с условием по путям, которого дифф ветки не
// задел, закрывается словами «не потребовался» (решение 8): след спрашивать
// нечего, правки для этапа не случилось.
func planDoneTrace(plan *planFile, idx int, start, home string, a planAddr) (string, error) {
	it := plan.Items[idx]
	if it.Trace == "" {
		plan.Items[idx].State = "completed"
		return "", nil
	}
	c := planTraceCtx(start, home, a)
	note := ""
	if it.Stage != "" {
		task := planTask(start, home, a)
		if _, tpl, err := planChoose(start, "", task); err == nil && tpl != nil {
			if st, ok := tpl.Stage(it.Stage); ok && len(st.Paths) > 0 {
				if touched, known := trace.PathsTouched(c.Root, c.Main, c.Branch, st.Paths); known && !touched {
					note = fmt.Sprintf("%s: не потребовался, дифф ветки маску %s не задел", it.Text, strings.Join(st.Paths, ", "))
				}
			}
		}
	}
	if note == "" {
		switch v, why := trace.Check(it.Trace, c); v {
		case trace.Have:
		case trace.ByWord:
			note = it.Text + ": " + why
		default:
			return "", fmt.Errorf("этап «%s» не закрыт: %s", it.Text, why)
		}
	}
	plan.Items[idx].State = "completed"
	return note, nil
}

// planTraceCtx собирает контекст следов для done: дерево, где идёт работа,
// задача сессии, если она есть, и её строка доски. Ворота доски собирают тот
// же контекст своим кодом (DK-1144), плану хватает лёгкой сборки: без дерева
// и без git проверяющие читаются так же, как у ворот вне git, а не молчат.
func planTraceCtx(start, home string, a planAddr) trace.Ctx {
	c := trace.Ctx{Again: "этап"}
	root, err := findRoot(start)
	if err != nil {
		return c
	}
	c.Root, c.LogRoot = root, root
	id := planTask(start, home, a)
	if id == "" {
		return c
	}
	c.ID = id
	if data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md")); err == nil {
		c.Doc = string(data)
	}
	c.Main = trace.GitMainBranch(root)
	c.Branch, _ = trace.UnmergedTaskBranch(root, id)
	c.DocsBranch = planDocsOnly(root, c.Main, c.Branch)
	if rows, err := loadRows(root); err == nil {
		if r := rowOf(rows, id); r != nil {
			c.TaskType = r.Type
			c.Accept = accept.KindOf(r.Title)
		}
	}
	return c
}

// planDocsOnly говорит, трогает ли дифф ветки одни тексты docs/: бескодовая
// правка не просит тестов, и проверяющий tests это знает.
func planDocsOnly(root, main, branch string) bool {
	if main == "" || branch == "" {
		return false
	}
	cmd := exec.Command("git", "-C", root, "diff", "--name-only", main+"..."+branch)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f = strings.TrimSpace(f)
		if f != "" && !strings.HasPrefix(f, "docs/") {
			return false
		}
	}
	return true
}

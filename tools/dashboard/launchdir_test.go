package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Сторож рабочего каталога у запусков процессов (DK-1373). Диалоги доступа
// macOS вставали на имя дашборда, когда его дерево поднимало процессы с
// унаследованным каталогом: под launchd это корень файловой системы, и дитя,
// обходящее своё дерево, упиралось в защищённые папки всей машины. Каталог
// поэтому называется на каждом подъёме процесса, и сторож держит класс целиком:
// новая дорога запуска без каталога краснеет прогоном, а не диалогами на
// машине. Той же дорогой проверяется и оболочка цикла цели: её tmux-сессии
// поднимаются с «-c».
//
// Сторож разбирает исходники дашборда и agentctl: всякое создание процесса
// (exec.Command и exec.CommandContext) обязано ставить cmd.Dir, а запускам без
// каталога место только в dirlessAllow ниже, с причиной. Перечень сверяется
// счётом: новая дорога внутри разрешённой функции меняет счёт и краснеет тоже,
// как и строчка, оставшаяся от переименованной функции.
func TestLaunchSitesNameDir(t *testing.T) {
	sites, findings := launchDirScan(t)
	for key, lines := range sites {
		allow := dirlessAllow[key]
		if allow.n == len(lines) {
			continue
		}
		for _, ln := range lines {
			if allow.why != "" {
				findings = append(findings, fmt.Sprintf(
					"%s:%d: запуск процесса без каталога сверх разрешённого (%d мест, ждём %d, причина прежних: %s): "+
						"назвать каталог присвоением cmd.Dir либо вписать запуск с причиной в dirlessAllow",
					key, ln, len(lines), allow.n, allow.why))
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"%s:%d: запуск процесса без каталога: назвать каталог присвоением cmd.Dir "+
					"либо вписать запуск с причиной в dirlessAllow", key, ln))
		}
	}
	for key := range dirlessAllow {
		if _, ok := sites[key]; !ok {
			findings = append(findings, fmt.Sprintf(
				"перечень dirlessAllow разошёлся с кодом: %s больше не запускает процессов без каталога, строчку убрать", key))
		}
	}
	if len(findings) == 0 {
		return
	}
	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
}

// dirlessAllow перечисляет запуски без каталога с числом мест и причиной.
// Контроль и статус-вызовы: они не обходят деревьев и не поднимают клиентов,
// а их утилиты работают по абсолютным путям и своим ключам каталога.
var dirlessAllow = map[string]launchAllow{
	"agentctl/corp.go:corpGit":             {1, "git с «-C» по абсолютному пути, дерево не обходит"},
	"agentctl/run.go:refreshQuotaDetached": {1, "отпочкование quota refresh: съёмщики панелей называют свои каталоги сами (panelDir)"},
	"agentctl/usage.go:tmuxRun":            {1, "управляющие вызовы tmux, сессии поднимаются с «-c»"},
	"agentctl/usage.go:ownerAlive":         {1, "статус-вызов ps, дерева не касается"},
	"agentctl/wait.go:waitCheck":           {1, "git status с «-C» по пути цели"},
	"agentctl/wait.go:procAlive":           {1, "статус-вызов ps, дерева не касается"},
}

// launchAllow это ожидаемое число запусков без каталога и причина разрешения.
type launchAllow struct {
	n   int
	why string
}

// launchDirScan обходит исходники двух пакетов и собирает запуски без каталога
// по функциям, а к ним находки по оболочке цикла цели.
func launchDirScan(t *testing.T) (map[string][]int, []string) {
	t.Helper()
	sites := map[string][]int{}
	var findings []string
	for _, pkg := range []struct{ dir, name string }{
		{".", "dashboard"},
		{"../agentctl", "agentctl"},
	} {
		files, err := filepath.Glob(filepath.Join(pkg.dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("исходники %s не найдены: %v", pkg.name, err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			launchDirFile(t, path, pkg.name, sites)
		}
	}
	// Оболочка цикла цели живёт на python и в AST-разбор не попадает, а сессия
	// без «-c» это та же дорога: цикл из-под launchd получил бы корень
	// файловой системы. Вызов new-session может стоять на нескольких строках,
	// поэтому «-c» ищется во всём вызове до закрывающих скобок, а не в одной
	// его строке.
	gr := filepath.Join("..", "..", "kit", "skills", "goal-loop", "goal-run.py")
	body, err := os.ReadFile(gr)
	if err != nil {
		t.Fatalf("оболочка цикла цели не прочитана: %v", err)
	}
	call, start := "", 0
	for i, ln := range strings.Split(string(body), "\n") {
		if call == "" {
			if !strings.Contains(ln, "new-session") {
				continue
			}
			call, start = ln, i+1
		} else {
			call += "\n" + ln
		}
		if strings.Count(call, "(") != strings.Count(call, ")") ||
			strings.Count(call, "[") != strings.Count(call, "]") {
			continue
		}
		if !strings.Contains(call, `"-c"`) {
			findings = append(findings, fmt.Sprintf(
				"%s:%d: tmux-сессия поднимается без «-c», каталог сессии не назван", gr, start))
		}
		call = ""
	}
	return sites, findings
}

// launchDirFile разбирает один файл и заносит в sites запуски процессов без
// каталога по функциям: ключ это «пакет/файл.go:функция», значение это строки.
func launchDirFile(t *testing.T, path, pkg string, sites map[string][]int) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s не разобран: %v", path, err)
	}
	key := pkg + "/" + filepath.Base(path)
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bound, dirSet := launchDirBinds(fn.Body)
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			c, ok := m.(*ast.CallExpr)
			if !ok || !launchDirIsCommand(c) {
				return true
			}
			if v := bound[c.Pos()]; v != "" && dirSet[v] {
				return true
			}
			sites[key+":"+fn.Name.Name] = append(sites[key+":"+fn.Name.Name], fset.Position(c.Pos()).Line)
			return true
		})
	}
}

// launchDirBinds собирает привязки вызовов к переменным и каталоги, которые
// переменные получают: присвоение exec.Command в переменную и присвоение
// cmd.Dir той же переменной в теле одной функции.
func launchDirBinds(body *ast.BlockStmt) (map[token.Pos]string, map[string]bool) {
	bound := map[token.Pos]string{}
	dirSet := map[string]bool{}
	ast.Inspect(body, func(m ast.Node) bool {
		a, ok := m.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(a.Rhs) == 1 && len(a.Lhs) == 1 {
			if c, ok := a.Rhs[0].(*ast.CallExpr); ok && launchDirIsCommand(c) {
				if id, ok := a.Lhs[0].(*ast.Ident); ok {
					bound[c.Pos()] = id.Name
				}
			}
		}
		for _, l := range a.Lhs {
			if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "Dir" {
				if id, ok := sel.X.(*ast.Ident); ok {
					dirSet[id.Name] = true
				}
			}
		}
		return true
	})
	return bound, dirSet
}

func launchDirIsCommand(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext")
}

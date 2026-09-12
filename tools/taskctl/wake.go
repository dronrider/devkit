package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/merged"
	"github.com/dronrider/devkit/internal/taskhead"
)

// Обход ждущих (DK-932). Строка ждёт соседа причиной Blocked с машинным
// разрядом «слияние: <ID>» либо «закрытие: <ID>». Событие, которое снимает
// ожидание, случается в `shipctl merge` и `taskctl close`, и обе команды в конце
// зовут этот хвост. Тик сторожка зовёт его же командой `taskctl wake` и добирает
// то, что мимо них проехало: слияние и закрытие руками, упавший хвост, событие
// до выката утилиты. Правило совпадения одно на троих, это condMet.
//
// Совпавшая строка выходит в In progress и поднимается лестницей `taskctl run`
// (DK-931). Порядок ходов тот же, что был у пробуждения вопросом в дашборде:
// всё, чем подъём может отказать надолго, спрашивается предполётом до снятия
// парковки. Отказ предполёта оставляет строку в Blocked, и следующий обход
// повторит заход.

const (
	classMerge = "слияние"
	classClose = "закрытие"
	classAsk   = "вопрос"
)

// waitClasses это разряды ожидания соседа. Причина пишется «<разряд>: <ID> ...»,
// дальше ID может идти проза.
var waitClasses = []string{classMerge, classClose}

// waitCond это ожидание соседа, разобранное из причины парковки.
type waitCond struct{ Class, Dep string }

// parseWaitCond разбирает причину парковки. Второй возврат говорит, что причина
// начата разрядом ожидания соседа. Ошибка значит, что разряд назван, а ID
// предпосылки первым словом за ним нет.
func parseWaitCond(reason string) (waitCond, bool, error) {
	for _, c := range waitClasses {
		rest, ok := strings.CutPrefix(reason, c+":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		dep := ""
		if len(f) > 0 {
			dep = strings.TrimRight(f[0], ",.;")
		}
		if !touchIDRe.MatchString(dep) {
			return waitCond{}, true, fmt.Errorf("разряд «%s:» ждёт ID предпосылки первым словом («%s: DK-123 ...»): по нему обход ждущих узнаёт событие", c, c)
		}
		return waitCond{Class: c, Dep: strings.ToUpper(dep)}, true, nil
	}
	return waitCond{}, false, nil
}

// condMet это правило совпадения, одно на close, merge и тик. Закрытие это
// строка предпосылки в архиве доски. Слияние это признак «слита» из
// internal/merged (DK-930): работа задачи лежит в main и не откачена, тот же
// ответ, что у `agentctl wait`. Статус строки предпосылки тут не спрашивается:
// поездное слияние оставляет её в In progress, а код уже в main. Второй возврат
// это слова события для отчёта и журнала.
func condMet(root string, arch *Archive, c waitCond) (bool, string, error) {
	switch c.Class {
	case classClose:
		if arch != nil && arch.has(c.Dep) {
			return true, c.Dep + " закрыта", nil
		}
		main, _ := merged.Main(root)
		ok, err := merged.Closed(root, main, c.Dep)
		if err != nil {
			return false, "", err
		}
		if ok {
			return true, c.Dep + " закрыта", nil
		}
		return false, c.Dep + " не закрыта", nil
	case classMerge:
		main, err := merged.Main(root)
		if err != nil {
			return false, "", err
		}
		v, err := merged.Task(root, main, c.Dep)
		if err != nil {
			return false, "", err
		}
		return v.Merged, v.Said(c.Dep), nil
	}
	return false, "", fmt.Errorf("разряд %q обходу не знаком", c.Class)
}

// checkWaitReason держит разряды ожидания соседа на парковке: ID предпосылки
// назван, это не сама строка, он есть на доске или в архиве, и событие ещё не
// случилось. Иначе строка спала бы в Blocked, дожидаясь того, что не придёт
// либо уже прошло.
func checkWaitReason(b *Board, root, id, reason string) error {
	c, ok, err := parseWaitCond(reason)
	if !ok || err != nil {
		return err
	}
	if strings.EqualFold(c.Dep, id) {
		return fmt.Errorf("%s ждёт сама себя: разряд «%s:» называет предпосылку, соседнюю задачу", id, c.Class)
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return err
	}
	if b.find(c.Dep) == nil && !arch.has(c.Dep) {
		return fmt.Errorf("%s нет ни на доске, ни в архиве: событие «%s: %s» не случится", c.Dep, c.Class, c.Dep)
	}
	// Спросить признак не вышло (нет git, нет main): парковку это не держит,
	// тот же вопрос задаст обход.
	if met, said, err := condMet(root, arch, c); err == nil && met {
		return fmt.Errorf("событие уже случилось (%s): ждать нечего, работа продолжается без парковки", said)
	}
	return nil
}

// waiter это строка, которую обход ведёт к подъёму. Met говорит, что событие
// случилось, Said это его слова для отчёта и журнала.
type waiter struct {
	ID, Class, Dep, Said string
	Met                  bool
}

// waiterSource отдаёт ждущих своего вида и строки отчёта про тех, чьё событие
// спросить не вышло.
type waiterSource func(root string, b *Board, arch *Archive) ([]waiter, []string)

// waiterSources это перечень источников обхода. Их два: припаркованная строка,
// ждущая соседа, и взведённая строка Backlog (DK-934). Close, merge и тик зовут
// обход одинаково и получают оба источника без правки своих вызовов.
var waiterSources = []waiterSource{parkedWaiters, armedWaiters}

// parkedWaiters это строки Blocked с разрядом ожидания соседа.
func parkedWaiters(root string, b *Board, arch *Archive) ([]waiter, []string) {
	var out []waiter
	var notes []string
	for _, r := range b.Rows {
		if r.Sect != SectBlocked {
			continue
		}
		_, _, _, _, _, blockSuf := splitTitle(r.Title)
		c, ok, err := parseWaitCond(blockReason(blockSuf))
		if !ok {
			continue
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", r.ID, err))
			continue
		}
		met, said, err := condMet(root, arch, c)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s ждёт «%s: %s», а спросить событие не вышло: %v", r.ID, c.Class, c.Dep, err))
			continue
		}
		out = append(out, waiter{ID: r.ID, Class: c.Class, Dep: c.Dep, Said: said, Met: met})
	}
	return out, notes
}

// wakeOpts это режим обхода. commit и push про коммит перевода строки, hidden
// метит голову, поднятую без человека (DK-847), quiet гасит отчёт, когда будить
// некого.
type wakeOpts struct {
	commit, push, hidden, quiet bool
}

// raiseHead поднимает голову строки лестницей `taskctl run`. Тесты подменяют её
// вместе с wakePreflight: настоящая лестница дотянулась бы до tmux и клиента
// машины.
var raiseHead = func(root, id string, o runOpts) (string, int, error) {
	return cmdRun(root, id, o)
}

// wakePreflight отвечает, есть ли чем поднять голову, до снятия парковки.
var wakePreflight = func(root, id string, o runOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	q, err := runRequest(root, home, id, o)
	if err != nil {
		return err
	}
	return taskhead.Preflight(q)
}

// cause это событие словами: разряд, предпосылка и ответ признака. У взвода
// предпосылка не одна, и слова приходят готовыми от источника.
func (w waiter) cause() string {
	if w.Class == classArm {
		return "взведена, " + w.Said
	}
	if w.Dep != "" {
		return fmt.Sprintf("ждала «%s: %s», %s", w.Class, w.Dep, w.Said)
	}
	return w.Said
}

// wakeFrom говорит, откуда обход поднимает строку. Ждущую соседа он берёт из
// Blocked и In progress, взведённую из Backlog: согласие человека на старт
// живёт только там, а начатой строке взвод уже не нужен.
func wakeFrom(class, sect string) bool {
	if class == classArm {
		return sect == SectBacklog
	}
	return sect == SectBlocked || sect == SectInProgress
}

// wakeWords это хвост сообщения коммита доски: взведённая строка не будится, а
// стартует, и в истории доски это видно словами.
func wakeWords(class string) string {
	if class == classArm {
		return "стартует по взводу"
	}
	return "разбужена обходом ждущих"
}

// wakeRow ведёт одну строку: предполёт, снятие признака вопроса, перевод в In
// progress, подъём и строка журнала. Второй возврат ложный, когда строка
// осталась стоять там, где стояла.
func wakeRow(root string, w waiter, o wakeOpts) (string, bool) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return fmt.Sprintf("%s: доска не прочиталась: %v", w.ID, err), false
	}
	row := b.find(w.ID)
	if row == nil {
		return w.ID + ": строки нет на доске", false
	}
	if !wakeFrom(w.Class, row.Sect) {
		return fmt.Sprintf("%s: подъём не нужен, строка в %s", w.ID, row.Sect), true
	}
	ro := runOpts{hidden: o.hidden}
	// У цели своя оболочка, и ожидание она переживает сама: парковку снять
	// надо, а голову задачи поверх цикла поднимать нельзя.
	goal := goalRow(row.Title)
	if !goal {
		if err := wakePreflight(root, w.ID, ro); err != nil {
			return fmt.Sprintf("%s: %s, а голову поднять нечем, строка стоит в %s: %v", w.ID, w.cause(), row.Sect, err), false
		}
	}
	if w.Class == classAsk {
		dropTaskAsks(root, w.ID)
	}
	if row.Sect != SectInProgress {
		var c CommitOpts
		if o.commit {
			c = CommitOpts{Msg: fmt.Sprintf("docs(tasks): %s %s", w.ID, wakeWords(w.Class)), Push: o.push}
		}
		if _, err := cmdMove(root, w.ID, SectInProgress, "", c); err != nil {
			return fmt.Sprintf("%s: %s, а строка из %s не вышла: %v", w.ID, w.cause(), row.Sect, err), false
		}
	}
	if goal {
		logWake(root, w, 0)
		return fmt.Sprintf("%s: %s, строка в In progress; голову не поднимаю, это цель: её цикл переживает ожидание сам", w.ID, w.cause()), true
	}
	out, code, err := raiseHead(root, w.ID, ro)
	logWake(root, w, code)
	said := strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(out), "\n", "; ")), " ")
	if err != nil {
		return fmt.Sprintf("%s: %s, строка в In progress, а подъём отказал: %v", w.ID, w.cause(), err), false
	}
	return fmt.Sprintf("%s: %s, строка в In progress; %s", w.ID, w.cause(), said), true
}

// logWake пишет подъём в журнал .devkit/log проекта: разряд и ID предпосылки
// стоят в ячейке команды, код это код лестницы подъёма.
func logWake(root string, w waiter, code int) {
	what := "wake " + w.ID + " " + w.Class
	if w.Dep != "" {
		what += ": " + w.Dep
	}
	logLine(root, what, code)
}

// dropTaskAsks снимает признак ожидания вопроса в чекауте и в дереве задачи
// рядом с ним: спрашивают чаще из дерева задачи, а отвечают в основном чекауте,
// и признак, переживший ответ, рисовал бы в панели вопрос уже не ждущей строке.
func dropTaskAsks(root, id string) {
	top := filepath.Clean(root)
	tree := filepath.Join(filepath.Dir(top), filepath.Base(top)+"-"+strings.ToLower(id))
	for _, dir := range []string{top, tree} {
		chat.DropAsk(dir, chat.TaskName(id))
	}
}

// namedWaiter собирает подъём по названному ID: строка в Blocked с любой
// причиной либо уже в In progress. Разряд берётся из причины для журнала.
func namedWaiter(b *Board, id string) waiter {
	id = strings.ToUpper(strings.TrimSpace(id))
	w := waiter{ID: id, Class: "назван", Said: "подъём по названному ID", Met: true}
	row := b.find(id)
	if row == nil {
		return w
	}
	if row.Sect == SectBacklog && armed(row.Title) {
		w.Class, w.Said = classArm, "подъём по названному ID"
		return w
	}
	_, _, _, _, _, blockSuf := splitTitle(row.Title)
	reason := blockReason(blockSuf)
	if c, ok, err := parseWaitCond(reason); ok && err == nil {
		w.Class, w.Dep = c.Class, c.Dep
		return w
	}
	for _, p := range parkPrefixes {
		if strings.HasPrefix(reason, p+":") {
			w.Class = p
		}
	}
	if w.Class == classAsk {
		w.Said = "ответ человека лежит во входе"
	}
	return w
}

// cmdWake это обход ждущих. Без ID идут источники обхода, и поднимаются только
// строки, чьё событие случилось. С ID поднимаются названные строки: так тик
// будит вопрос, ответ на который лежит во входе (правило ответа живёт в
// сторожке, он читает вход разговора). Третий возврат это признак, что
// какая-то строка осталась стоять.
func cmdWake(root string, ids []string, o wakeOpts) (string, bool, error) {
	if o.push && !o.commit {
		return "", false, fmt.Errorf("--push без коммита не имеет смысла")
	}
	if err := boardGuard(root, "wake"); err != nil {
		return "", false, err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", false, err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", false, err
	}
	var todo []waiter
	var lines []string
	waiting := 0
	if len(ids) == 0 {
		// Событие соседа будит строку без человека в окне.
		o.hidden = true
		for _, src := range waiterSources {
			got, notes := src(root, b, arch)
			lines = append(lines, notes...)
			for _, w := range got {
				waiting++
				if w.Met {
					todo = append(todo, w)
				}
			}
		}
	} else {
		for _, id := range ids {
			todo = append(todo, namedWaiter(b, id))
		}
	}
	failed, raised := false, 0
	for _, w := range todo {
		line, ok := wakeRow(root, w, o)
		lines = append(lines, line)
		if ok {
			raised++
		} else {
			failed = true
		}
	}
	if len(ids) == 0 {
		if len(todo) == 0 && len(lines) == 0 {
			if o.quiet {
				return "", false, nil
			}
			if waiting == 0 {
				return "ждущих событием нет", false, nil
			}
		}
		lines = append(lines, fmt.Sprintf("обход ждущих: ждут событием %d, событие случилось у %d, разбужено %d", waiting, len(todo), raised))
	}
	return strings.Join(lines, "\n"), failed, nil
}

// wakeNote это хвост обхода ждущих в отчёте close: пусто, когда будить некого.
// Доска к этому моменту записана и запушена, и провал обхода закрытия не
// роняет, он дописывается предупреждением, как у разлива.
func wakeNote(root string, c CommitOpts) string {
	msg, _, err := cmdWake(root, nil, wakeOpts{commit: c.Msg != "", push: c.Push, quiet: true})
	if err != nil {
		return "\nпредупреждение: обход ждущих не прошёл: " + err.Error()
	}
	if msg == "" {
		return ""
	}
	return "\n" + msg
}

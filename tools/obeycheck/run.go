package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Params struct {
	Scenarios []Scenario
	Layouts   []string // ровно две раскладки: сравниваются они между собой
	Repeats   int      // k повторов на раскладку
	Agent     []string // команда прогона, промпт уходит ей на stdin
	End       string   // конец прогона: сессия или субагент
	AgentDef  string   // определение исполнителя для субагентского конца
	Devkit    string   // чекаут devkit
	HomeSeed  string   // что положить во временный HOME до раскладки
	UserHome  string   // дом пользователя, откуда берётся связка ключей; пусто это дом машины
	Work      string   // куда класть прогоны, пусто это временная директория
	Keep      bool     // не убирать директории прогонов
	Preflight bool     // пробный прогон перед полным проходом
	Timeout   time.Duration
	Progress  io.Writer // построчный ход прогона, обычно stderr
}

// Обёртка субагентского конца. Резидент у исполнителя тот же, а поведение
// другое, и разрез, безопасный для диспетчера, может оказаться опасным для
// него. Текст обёртки нарочно короткий: она передаёт работу, а не учит, как её
// делать.
const subagentWrap = `Работу ниже сам не делай. Спавни субагента с определением %s и передай ему текст задачи дословно, дальше дождись его отчёта.

Текст задачи:

%s`

type attempt struct {
	Green   bool
	Suspect bool   // проверка зелёная, а команда прогона вышла с ошибкой
	Note    string // чем кончился прогон, если не просто зелено
	Repeat  int
}

type cell struct {
	Attempts []attempt
}

func (c cell) green() int {
	n := 0
	for _, a := range c.Attempts {
		if a.Green {
			n++
		}
	}
	return n
}

type row struct {
	Scenario Scenario
	Cells    []cell
	Skipped  bool
	Verdict  string
}

func trimNote(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.TrimSpace(strings.Join(lines, "; "))
}

// runOnce гоняет один сценарий на одной раскладке один раз: собирает окружение,
// отдаёт промпт команде прогона и спрашивает у проверки код возврата. Зелено
// или нет, решает только проверка: агент мог выйти с ошибкой и всё равно
// сделать работу, а мог отчитаться бодро и не сделать ничего.
func (p Params) runOnce(s Scenario, layout string, repeat int, dir string) (attempt, error) {
	a := attempt{Repeat: repeat}
	e, err := makeEnv(dir, p.Devkit, layout, p.HomeSeed, p.UserHome)
	if err != nil {
		return a, err
	}
	env := e.environ(p.Devkit, filepath.Base(layout), s.ID, repeat)
	if s.Setup != "" {
		out, err := shOut(e.Project, env, s.Setup, p.Timeout)
		if err != nil {
			return a, fmt.Errorf("подготовка сценария %s не прошла: %v (%s)", s.ID, err, trimNote(out))
		}
	}
	prompt := s.Prompt
	if p.End == endSub {
		prompt = fmt.Sprintf(subagentWrap, p.AgentDef, prompt)
	}
	tr, err := os.Create(e.Transcript)
	if err != nil {
		return a, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Agent[0], p.Agent[1:]...)
	cmd.Dir = e.Project
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = tr
	cmd.Stderr = tr
	runErr := cmd.Run()
	tr.Close()
	if runErr != nil && ctx.Err() != nil {
		a.Note = fmt.Sprintf("прогон не уложился в %s", p.Timeout)
	}
	if runErr != nil && cmd.ProcessState == nil {
		return a, fmt.Errorf("команда прогона %q не запустилась: %v", strings.Join(p.Agent, " "), runErr)
	}
	out, err := shOut(e.Project, e.checkEnviron(env), s.Check, p.Timeout)
	if err == nil {
		a.Green = true
		// Зелёная проверка при упавшей команде прогона это повод посмотреть
		// глазами: чаще всего так выглядит проверка, которая зеленеет и на
		// сессии, которой не было (например, неавторизованный HOME).
		if runErr != nil {
			a.Suspect = true
			a.Note = fmt.Sprintf("проверка зелёная, а команда прогона вышла с ошибкой: %v", runErr)
		}
		return a, nil
	}
	if a.Note == "" {
		a.Note = fmt.Sprintf("проверка: %v", err)
	}
	if n := trimNote(out); n != "" {
		a.Note += " (" + n + ")"
	}
	return a, nil
}

// preflight гоняет одну пробную сессию до полного прохода. Стоит она копейки, а
// ловит случай, ради которого прогон не стоит начинать: команда прогона
// отвечает, но сессии не поднимает (чаще всего временный HOME не авторизован).
// Без пробы это выглядит как ровная таблица нулей или, хуже, как зелёные
// проверки на отрицание, и двести сессий уходят впустую.
func (p Params) preflight(work string) error {
	dir := filepath.Join(work, "preflight")
	e, err := makeEnv(dir, p.Devkit, p.Layouts[0], p.HomeSeed, p.UserHome)
	if err != nil {
		return err
	}
	if !p.Keep {
		defer os.RemoveAll(dir)
	}
	tr, err := os.Create(e.Transcript)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Agent[0], p.Agent[1:]...)
	cmd.Dir = e.Project
	cmd.Env = e.environ(p.Devkit, filepath.Base(p.Layouts[0]), "preflight", 0)
	cmd.Stdin = strings.NewReader("Ответь одним словом: ок.")
	cmd.Stdout = tr
	cmd.Stderr = tr
	runErr := cmd.Run()
	tr.Close()
	size := int64(0)
	if fi, err := os.Stat(e.Transcript); err == nil {
		size = fi.Size()
	}
	if runErr == nil && size > 0 {
		p.say("проба: команда прогона отвечает, идём полным проходом")
		return nil
	}
	why := "транскрипт пуст"
	if runErr != nil {
		why = runErr.Error()
	}
	return fmt.Errorf("пробная сессия командой %q не отработала (%s); вслепую стенд полный проход не гонит. "+
		"Частая причина это неавторизованный временный HOME, разбор в tools/obeycheck/README.md; "+
		"пропустить пробу: --no-preflight", strings.Join(p.Agent, " "), why)
}

func shOut(dir string, env []string, script string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (p Params) validate() error {
	if len(p.Layouts) != 2 {
		return fmt.Errorf("жду ровно две раскладки, получил %d: стенд сравнивает одну с другой", len(p.Layouts))
	}
	for _, l := range p.Layouts {
		if !dirExists(l) {
			return fmt.Errorf("раскладки %s нет или это не директория", l)
		}
	}
	if p.Repeats < 1 {
		return fmt.Errorf("повторов должно быть хотя бы один")
	}
	if len(p.Agent) == 0 {
		return fmt.Errorf("нужна команда прогона")
	}
	if len(p.Scenarios) == 0 {
		return fmt.Errorf("нужен хотя бы один сценарий")
	}
	if p.End != endSession && p.End != endSub {
		return fmt.Errorf("конец прогона это %s или %s, получил %q", endSession, endSub, p.End)
	}
	return nil
}

// Run гоняет все сценарии на обеих раскладках и возвращает таблицу и признак
// регрессии. Регрессия это код возврата 1 у команды: по нему стенд встраивается
// в чужой конвейер, а не читается глазами.
func Run(p Params) (string, bool, error) {
	if err := p.validate(); err != nil {
		return "", false, err
	}
	userHome, err := userHomeDir(p.UserHome)
	if err != nil {
		return "", false, err
	}
	p.UserHome = userHome
	if err := checkAuth(userHome, p.HomeSeed); err != nil {
		return "", false, err
	}
	work := p.Work
	if work == "" {
		tmp, err := os.MkdirTemp("", "obeycheck-")
		if err != nil {
			return "", false, err
		}
		work = tmp
		if !p.Keep {
			defer os.RemoveAll(tmp)
		}
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", false, err
	}
	// Симлинки в пути прогона разрешаются сразу: на macOS временная директория
	// лежит за /var -> /private/var, и тогда pwd проекта не сходится с тем, что
	// печатает git, а проверки сценариев сравнивают именно пути.
	if resolved, err := filepath.EvalSymlinks(work); err == nil {
		work = resolved
	}

	var rows []row
	var live []Scenario
	for _, s := range p.Scenarios {
		if !s.runsOn(p.End) {
			rows = append(rows, row{Scenario: s, Skipped: true, Verdict: "пропущен, конец " + s.End})
			continue
		}
		live = append(live, s)
	}
	total := len(live) * len(p.Layouts) * p.Repeats
	p.say("прогон: сценариев %d, раскладки %d, повторов %d, всего %d сессий (конец: %s)",
		len(live), len(p.Layouts), p.Repeats, total, p.End)
	if p.Preflight {
		if err := p.preflight(work); err != nil {
			return "", false, err
		}
	}

	done := 0
	for _, s := range live {
		r := row{Scenario: s, Cells: make([]cell, len(p.Layouts))}
		for li, layout := range p.Layouts {
			for i := 1; i <= p.Repeats; i++ {
				dir := filepath.Join(work, fmt.Sprintf("%s-%s-%d", s.ID, filepath.Base(layout), i))
				a, err := p.runOnce(s, layout, i, dir)
				if err != nil {
					return "", false, err
				}
				done++
				word := "зелено"
				if a.Suspect {
					word = "зелено под вопросом: " + a.Note
				}
				if !a.Green {
					word = "красно: " + a.Note
				}
				p.say("[%d/%d] %s / %s / повтор %d: %s", done, total, s.ID, filepath.Base(layout), i, word)
				r.Cells[li].Attempts = append(r.Cells[li].Attempts, a)
				if !p.Keep {
					os.RemoveAll(dir)
				}
			}
		}
		r.Verdict = verdictOf(r.Cells[0], r.Cells[1])
		rows = append(rows, r)
	}
	regression := false
	for _, r := range rows {
		if r.Verdict == verdictRegress {
			regression = true
		}
	}
	return render(rows, p.Layouts, p.Repeats), regression, nil
}

func (p Params) say(format string, a ...any) {
	if p.Progress == nil {
		return
	}
	fmt.Fprintf(p.Progress, format+"\n", a...)
}

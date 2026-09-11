package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/accept"
	"github.com/dronrider/devkit/internal/checkrun"
	"github.com/dronrider/devkit/internal/taskhead"
)

// Подъём прогона сценария после выката (DK-718, DK-947). Выкат при autonomous
// = true доводит задачу до прода сам, а дальше конвейер упирался в правило
// независимости: агентскую часть сценария гоняет не автор правки, и позвать
// этого проверяющего было некому. Строка стояла в Check, держала очередь
// непроверенного выката и ждала, пока человек поднимет сессию руками.
//
// Зовёт подъём тот, кто увёл строку в Check без человека в окне, то есть сам
// выкат: merge и ship при autonomous = true. Признак тут не флаг вызова, а
// доверенный агенту конвейер: команду выката катит агент, и адресата у события
// «строка в Check» нет ни одного. Явный `--deploy` в это число не входит, он
// указание человека прямо сейчас, и человек в окне у него есть. Тем же кодом
// закрыт разлив поезда тиком сторожка: тик зовёт `ship --drain`, и подъём едет
// внутри той же команды, а строки, мимо которых выкат проехал, тик добирает
// командой `shipctl check-run`.
//
// Кого поднимать и какой моделью, решает общий отбор internal/checkrun, тот же
// у дашборда. Носитель тут `taskctl run <ID> --model <модель>`: лестница
// internal/taskhead и замок головы задачи, как у подъёма упавшего хода. До
// DK-947 подъём шёл бинарём дашборда, и без него строка стояла до человека.

// checkRunTimeout это предел подъёма одной головы: команда отдаёт окно tmux, и
// висеть ей не на чем. Предел нужен на случай подвисшего подпроцесса: слияние
// к этому моменту необратимо, и держать отчёт заложником подъёма нельзя.
const checkRunTimeout = 3 * time.Minute

// errCheckRunFailed это код возврата `check-run`: подъём был нужен и не вышел.
var errCheckRunFailed = errors.New("подъём прогона не вышел")

// shipCarrier это носитель подъёма без дашборда: голову поднимает taskctl run.
type shipCarrier struct {
	root, home, devkit string
	harness            string
	ladder             []checkrun.Step
	note               string
	asked              bool
}

func newShipCarrier(root string) *shipCarrier {
	home, _ := os.UserHomeDir()
	return &shipCarrier{root: root, home: home, devkit: taskhead.Devkit(root, home)}
}

func (c *shipCarrier) Ready(id string) (string, bool) {
	if _, err := exec.LookPath("taskctl"); err != nil {
		return checkrun.NotRaised + ", taskctl не нашёлся в PATH: поднимать голову нечем; прогнать" +
			" агентскую часть сценария и отметить `shipctl smoke " + id + "` руками", true
	}
	if c.devkit == "" {
		return checkrun.NotRaised + ", дерево devkit с " + taskhead.TaskRunRel + " не нашлось ни в" +
			" DEVKIT_HOME, ни в .devkit/devkit проекта, ни рядом с ним: голове негде жить", true
	}
	if why := checkrun.Perms(c.devkit, c.home); why != "" {
		return checkrun.NotRaised + ", права машинного контура на машине не разложены: " + why, true
	}
	return "", false
}

func (c *shipCarrier) Tier(id string) (string, string) { return checkrun.AskTier(c.root, id) }

func (c *shipCarrier) Ladder() []checkrun.Step {
	if !c.asked {
		c.asked = true
		c.harness, c.ladder, c.note = checkrun.AskLadder()
	}
	return c.ladder
}

// Raise поднимает голову проверки командой taskctl run. Модель называется
// всегда: без --model команда спросила бы вердикт исполнителя с записью этапа,
// и проверяющий лёг бы в «Ход работы» разработчиком.
func (c *shipCarrier) Raise(p checkrun.Plan) checkrun.Report {
	id := p.Row.ID
	if p.Choice.Model == "" {
		why := c.note
		if why == "" {
			why = "яруса " + p.Choice.Tier + " в лестнице подписки нет"
		}
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, модель проверяющего не названа (%s):"+
			" без неё taskctl run спросил бы вердикт исполнителя и записал бы проверяющего разработчиком",
			id, checkrun.NotRaised, why)}
	}
	args := []string{"run", id, "-C", c.root, "--model", p.Choice.Model,
		"--order", p.Order, "--again", p.Again, "--hidden"}
	if c.harness != "" {
		args = append(args, "--harness", c.harness)
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskctl", args...)
	cmd.Dir = c.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	said := strings.Join(strings.Fields(stdout.String()+" "+stderr.String()), " ")
	if ctx.Err() != nil {
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, taskctl run сорвал срок в %s: прогнать"+
			" сценарий и отметить `shipctl smoke %s` руками", id, checkrun.NotRaised, checkRunTimeout, id)}
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, taskctl run не запустился: %v", id, checkrun.NotRaised, err)}
		}
		code = ee.ExitCode()
	}
	switch code {
	case taskhead.CodeRaised:
		return checkrun.Report{Raised: true, Line: p.Raised(rungWords(stdout.String()))}
	case taskhead.CodeBusy:
		// Замок держит живая голова задачи, чаще всего та, что сама и катила
		// выкат. Поломкой это не считается: не автор прогонит сценарий сам, а
		// строку, которую голова сдала, поднимет тик после её выхода.
		return checkrun.Report{Line: fmt.Sprintf("%s: %s, работа уже идёт (%s): голова задачи прогонит"+
			" сценарий сама, если разработку вела не она, иначе проверяющего поднимет тик после её выхода",
			id, checkrun.NotRaised, said)}
	case taskhead.CodeCalled:
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, поднять голову нечем, позван человек: %s",
			id, checkrun.NotRaised, said)}
	}
	return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, taskctl run отказал с кодом %d: %s",
		id, checkrun.NotRaised, code, said)}
}

// rungWords называет ступень, на которой встала голова, по последней строке
// вывода taskctl run («ступень: новое окно, код 0»).
func rungWords(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "ступень: "); ok {
			rung, _, _ := strings.Cut(rest, ",")
			return "командой taskctl run, ступень " + strings.TrimSpace(rung)
		}
	}
	return "командой taskctl run"
}

// boardCheckRows переводит доску в строки отбора: вид приёмки из суффикса
// заголовка, провал из пометки taskctl fail.
func boardCheckRows(b *board) map[string]checkrun.Row {
	titles := map[string]string{}
	for _, sp := range sectByPrefix {
		titles[sp.key] = strings.TrimPrefix(sp.prefix, "## ")
	}
	rows := map[string]checkrun.Row{}
	for sect, rs := range b.sects {
		for _, r := range rs {
			rows[r.ID] = checkrun.Row{ID: r.ID, Title: r.Title, Sect: sect, Section: titles[sect],
				Accept: accept.KindOf(r.Title), Fail: failedOf(b, r.ID)}
		}
	}
	return rows
}

// checkRuns поднимает прогон по названным строкам, а без имён по всей секции
// Check. moved значит, что названные строки только что увёл в Check сам
// выкат: перевод сделала эта же команда, и секция берётся с её слов, а не
// перечиткой доски.
func checkRuns(root string, ids []string, moved bool) ([]checkrun.Report, error) {
	b, err := loadBoard(root)
	if err != nil {
		return nil, fmt.Errorf("доска не прочиталась: %v", err)
	}
	rows := boardCheckRows(b)
	if moved {
		for _, id := range ids {
			r := rows[id]
			r.ID, r.Sect, r.Section = id, "check", "Check"
			rows[id] = r
		}
	}
	return checkrun.Run(newShipCarrier(root), root, rows, ids), nil
}

// checkRunNote поднимает прогон сценария по задачам, ушедшим в Check, и
// возвращает строки для отчёта команды, тем же приёмом, что notify():
// молчащего исхода у подъёма нет ни одного, а провал подъёма не должен прятать
// то, что выкат прошёл. Пустой ответ бывает только там, где поднимать нечего
// вовсе.
func checkRunNote(root string, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	reps, err := checkRuns(root, ids, true)
	if err != nil {
		return fmt.Sprintf("прогон сценария не поднят: %v; прогнать агентскую часть сценария (%s)"+
			" и отметить `shipctl smoke %s` руками", err, strings.Join(ids, ", "), ids[0])
	}
	var lines []string
	for _, r := range reps {
		lines = append(lines, r.Line)
	}
	return strings.Join(lines, "\n")
}

// cmdCheckRun это вход `shipctl check-run`: страховка подъёма, которой тик
// сторожка добирает строки Check, проехавшие мимо выката. Машинный вид --json
// отдаёт исход по строкам: тик по нему помнит повтор отказа и зовёт человека
// один раз, а не пишет одно и то же в журнал каждые пять минут.
func cmdCheckRun(root string, ids []string, asJSON bool) (string, bool, error) {
	reps, err := checkRuns(root, ids, false)
	if err != nil {
		return "", true, err
	}
	failed := checkrun.Failed(reps)
	if asJSON {
		data, err := json.Marshal(reps)
		if err != nil {
			return "", true, err
		}
		return string(data), failed, nil
	}
	var lines []string
	for _, r := range reps {
		lines = append(lines, r.Line)
	}
	return strings.Join(lines, "\n"), failed, nil
}

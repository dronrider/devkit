package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/deployconf"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// Подъём прогона сценария после выката (DK-718). Выкат без человека в окне
// доводит задачу до прода и оставляет её в Check: агентскую часть сценария
// гоняет не автор правки, а поднять этого проверяющего было некому, и строка
// стояла до тех пор, пока человек не поднимал сессию руками.
//
// Механика подъёма тут та же, что у кнопки запуска на экране: tmux-сессия
// task-<ID> с оболочкой проходов и клиентом (runs.go). Своей у команды только
// два решения, кого поднимать и кому прогон не отдавать, и оба живут в одном
// месте, потому что зовущих у подъёма двое: shipctl в точке выката и тик
// сторожка страховкой по всем строкам Check.

// acceptMixed это смешанный вид приёмки: агентская половина сценария есть, а
// последний шаг за человеком, и закрывать такую строку подъём не заказывает.
const acceptMixed = "mixed"

// roleReview это роль вердикта, которой назначается проверяющий.
const roleReview = "review"

// errCheckRunFailed это код возврата команды: подъём был нужен и не вышел.
// Слова про причину уже напечатаны построчно, и повторять их ошибкой незачем.
var errCheckRunFailed = errors.New("подъём прогона не вышел")

// checkRunOrder это заказ поднятой сессии: прогнать агентскую часть сценария на
// выкаченном коде и довести строку до Done. Слова тут дословные, как у заказов
// экрана (runPrompt): по ним скиллы доски разводят работу, и пересказывать
// конвейер сессии не приходится. Имя исполнителя разработки едет в заказе
// потому, что ворота закрытия сверяют его с прогонявшим и на совпадении
// отказывают: сказать это до прогона дешевле, чем получить отказ после.
func checkRunOrder(id, dev, accept string) string {
	out := "Прогони агентскую часть сценария проверки " + id +
		" на выкаченном коде: вывод прогона в раздел «Проверка» файла задачи," +
		" прогонявшего отметь `agentctl stage " + id + " проверка --by <модель>`," +
		" выкат отметь `shipctl smoke " + id + "`."
	if accept == acceptUser || accept == acceptMixed {
		out += " Вид приёмки " + accept + ": твоя половина агентская, шаг человека остаётся ему," +
			" строку из Check не закрывай."
	} else {
		out += " Дальше закрой строку (`taskctl close " + id + "`)."
	}
	if dev != "" {
		out += " Разработку вёл " + dev + ", ему прогон не отдавай: ворота закрытия сверяют имена."
	}
	out += " Сценарий провалился, значит прод сломан: `taskctl fail " + id +
		" --reason \"чем сломан прод\"` и разбор по скиллу board-ship, а не закрытие."
	return out
}

// checkRunReport это исход подъёма по одной строке: слова для журнала зовущего
// и признак поломки. Поломкой считается только то, где подъём был нужен и не
// вышел: строка без нужды в прогоне (отметка стоит, приёмка за человеком, идёт
// своя сессия) это штатное «поднимать нечего».
type checkRunReport struct {
	Line   string
	Failed bool
	Raised bool
}

// needCheckRun решает, нужен ли строке прогон. Первым спрашивается проект:
// подъём заводит только конвейер, доверенный агенту целиком (`autonomous = true`
// в обвязке выката). Проект с выкатом за пользователем проверяющего не
// поднимает вовсе. Там человек в окне, до Check строка доходит с его рук, и
// сессия поверх его работы встала бы каждым тиком сторожка. Дальше идут те же
// признаки, по которым taskctl отбирает строки Check в закрытие автоматикой:
// секция, вид приёмки, непогашенный провал и отметка smoke на последний выкат.
// Пустой ответ значит «прогон нужен», непустой это причина, по которой
// поднимать нечего.
func needCheckRun(root string, row boardRow) string {
	if !deployconf.Autonomous(root) {
		return "выкат за пользователем (autonomous = false в " + deployconf.Rel +
			"): проверяющего поднимает человек"
	}
	if row.Sect != "check" {
		return "строка не в Check (" + row.Section + ")"
	}
	if row.Accept == acceptUser {
		return "приёмка за человеком (вид user): агентской половины у сценария нет"
	}
	if row.Fail != "" {
		return "непогашенный провал проверки: сначала чинится прод"
	}
	doc, err := os.ReadFile(taskDocPath(root, row.ID))
	if err != nil {
		return "файла задачи нет: сценарий прогонять не по чему"
	}
	if taskform.SmokeCovers(string(doc)) {
		return "отметка «" + taskform.SmokeNote + "» стоит: сценарий после выката прогнан"
	}
	return ""
}

// taskDocPath это файл задачи в корне проекта.
func taskDocPath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// devExecutor называет модель, которая вела разработку задачи: незакрытый пакет
// этапов и раздел «Ход работы» файла задачи, оба разбирает internal/stage, тот
// же код, которым ворота закрытия ловят прогон под именем автора правки.
func devExecutor(root, id string) (string, bool) {
	var lines []string
	if doc, err := os.ReadFile(taskDocPath(root, id)); err == nil {
		lines = taskform.SectionLines(string(doc), taskform.Stages)
	}
	var pending []stage.Stage
	if rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id)); err == nil {
		pending = rec.Stages
	}
	return stage.LastExecutor(lines, pending)
}

// checkRun поднимает прогон по одной строке Check. Возврат это строка отчёта:
// молчащего исхода тут нет ни одного, зовущий уносит слова в свой журнал.
func (s *server) checkRun(proj *Project, id string, rows map[string]boardRow) checkRunReport {
	row, ok := rows[id]
	if !ok {
		return checkRunReport{Line: id + ": строки нет на доске проекта " + proj.Name, Failed: true}
	}
	if why := needCheckRun(proj.Path, row); why != "" {
		return checkRunReport{Line: id + ": подъём не нужен, " + why}
	}
	if m := tmuxMissingCheck(); m != "" {
		return checkRunReport{Line: id + ": прогон не поднят, " + m, Failed: true}
	}
	sess := "task-" + id
	talk := s.tmuxTalk(proj.Path)
	for _, name := range tmuxSessions() {
		if name == sess && !talk[name] {
			return checkRunReport{Line: id + ": подъём не нужен, работа уже идёт в tmux-сессии " + sess}
		}
	}
	// Подписка тут не выбирается: подъём идёт клиентом по умолчанию, как шла
	// бы кнопка запуска без выбора руки. Раскладка спрашивается только про
	// лестницу ярусов, ею ярус проверяющего разворачивается в модель.
	own := s.harnesses().byDefault()
	if m := claudeMissing(); m != "" {
		return checkRunReport{Line: id + ": прогон не поднят, " + m, Failed: true}
	}
	// Ярус проверяющего берётся ролью ревью, а не исполнительским вердиктом:
	// прогон это второй взгляд на ту же работу, независимость у него та же, что
	// у ревью, и ярусом он ниже разработки.
	tier, tierWhy := s.pickTier(proj.Path, id, roleReview)
	model := ""
	if own != nil && own.Default {
		model = own.tierModel(tier)
	}
	dev, known := devExecutor(proj.Path, id)
	if known && model != "" && strings.EqualFold(model, dev) {
		return checkRunReport{Failed: true, Line: fmt.Sprintf(
			"%s: прогон не поднят, ярусом %s он достался бы модели %s, а она вела разработку;"+
				" сценарий прогоняет не автор правки, поднять другой моделью руками либо развести ярусы в раскладке машины",
			id, tier, model)}
	}
	if err := s.startTaskSession(proj, id, sess, nil, model,
		checkRunOrder(id, dev, row.Accept), runPrompt("in-progress", id)); err != nil {
		return checkRunReport{Line: id + ": прогон не поднят, " + err.Error(), Failed: true}
	}
	line := fmt.Sprintf("%s: прогон сценария поднят в tmux-сессии %s, %s", id, sess, tierWhy)
	switch {
	case known && model != "":
		line += fmt.Sprintf(", модель %s, разработку вёл %s", model, dev)
	case known:
		line += fmt.Sprintf(", разработку вёл %s (модель яруса раскладка не назвала, независимость сторожат ворота закрытия)", dev)
	default:
		line += ", исполнителя разработки записи не назвали, независимость сторожат ворота закрытия"
	}
	return checkRunReport{Line: line, Raised: true}
}

// checkRuns проходит по названным строкам, а без имён по всей секции Check.
// Пустых исходов не бывает: на каждую строку приходится своя строка отчёта, а
// на пустую секцию одна общая.
func (s *server) checkRuns(proj *Project, ids []string) (lines []string, raised int, failed bool) {
	raw, err := s.projectBoard(proj.Path)
	if err != nil {
		return []string{"доска проекта " + proj.Name + " не прочиталась: " + err.Error()}, 0, true
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return []string{"доска проекта " + proj.Name + " не разобралась: " + err.Error()}, 0, true
	}
	if len(ids) == 0 {
		for id, row := range rows {
			if row.Sect == "check" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return []string{"в Check пусто: поднимать нечего"}, 0, false
		}
	}
	for _, id := range ids {
		rep := s.checkRun(proj, id, rows)
		lines = append(lines, rep.Line)
		if rep.Raised {
			raised++
		}
		if rep.Failed {
			failed = true
		}
	}
	return lines, raised, failed
}

// cmdCheck это вход команды `dashboard check`: подъём прогона сценария по
// строкам Check названного проекта. Зовут её без экрана и без демона (shipctl
// после выката, тик сторожка страховкой), поэтому сервер тут собирается на
// месте, из того же конфига, и в сеть не выходит.
func cmdCheck(home, root string, ids []string, out io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !hasBoard(abs) {
		return fmt.Errorf("доски %s в %s нет: прогон поднимается по строке доски", boardRel, abs)
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	proj := &Project{Name: filepath.Base(abs), Path: abs}
	lines, _, failed := s.checkRuns(proj, ids)
	for _, ln := range lines {
		fmt.Fprintln(out, ln)
	}
	if failed {
		return errCheckRunFailed
	}
	return nil
}

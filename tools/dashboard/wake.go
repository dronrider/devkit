package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
)

// Подъём сессии задачи, разбуженной ответом человека (DK-922). Строка,
// припаркованная вопросом, после ответа возвращалась в In progress и стояла:
// оболочка конвейера к тому времени вышла по стопу wait_human и снесла окно, а
// поднять новое было некому. Человек отвечал и ждал, пока сам не заметит и не
// нажмёт «Запуск» заново.
//
// Зовущих у подъёма двое, и дорога у обоих одна. Панель поднимает сессию сразу,
// как только ответ лёг во вход задачи, а тик сторожка добирает пропущенное
// (реплику из терминала, ответ, написанный при мёртвом дашборде, строку,
// разбуженную прежним тиком). Тот же порядок держат подъём прогона сценария
// (checkrun.go) и второй круг чужого ревью (round.go): решения о том, кого
// поднимать и кому подъём не отдавать, живут в одном месте, потому что вторая
// копия этих правил разошлась бы с первой на первой же правке.

// taskWake поднимает сессию по одной разбуженной строке. Возврат тот же, что у
// подъёма прогона: слова отчёта и два признака, чтобы зовущий знал, поднялось
// ли что-нибудь и была ли поломка.
func (s *server) taskWake(proj *Project, id string, rows map[string]boardRow) checkRunReport {
	row, ok := rows[id]
	if !ok {
		return checkRunReport{Line: id + ": строки нет на доске проекта " + proj.Name, Failed: true}
	}
	// У цели своя оболочка, и на вопросе она не умирает: цикл сторожит признак
	// сам (goal-run.py). Поднимать ей сессию задачи нельзя вовсе.
	if isGoalTitle(row.Title) {
		return checkRunReport{Line: id + ": подъём не нужен, это цель: её цикл переживает вопрос сам"}
	}
	switch {
	case parkedByAsk(row):
		// Панель зовёт подъём сразу по записи ответа, и строка тут ещё стоит в
		// Blocked. Поднимать поверх парковки нечего: оболочка конвейера
		// спрашивает доску предполётом и вышла бы тем же стопом wait_human, с
		// которого всё и началось. Парковка снимается тем же ходом и тем же
		// вызовом taskctl, каким её снимает ответ клавишами (settleAsk) и тик
		// сторожка (wake в tools/devkitctl/watch.py).
		if err := s.unparkAsk(proj.Path, id); err != nil {
			return checkRunReport{Failed: true, Line: id +
				": сессия не поднята, парковка вопросом не снялась: " + err.Error()}
		}
	case row.Sect != "in-progress":
		return checkRunReport{Line: id + ": подъём не нужен, строка в " + row.Section}
	}
	if m := tmuxMissingCheck(); m != "" {
		return checkRunReport{Line: id + ": сессия не поднята, " + m, Failed: true}
	}
	sess := "task-" + id
	talk := s.tmuxTalk(proj.Path)
	for _, name := range tmuxSessions() {
		if name != sess {
			continue
		}
		if talk[name] {
			// Под тем же именем идёт разговор человека. Ответ он прочитает сам
			// первым же ходом, а сессия поверх его окна встала бы вторым
			// собеседником в том же чате.
			return checkRunReport{Line: id + ": подъём не нужен, в сессии " + sess + " идёт разговор"}
		}
		return checkRunReport{Line: id + ": подъём не нужен, работа уже идёт в tmux-сессии " + sess}
	}
	own := s.harnesses().byDefault()
	if m := claudeMissing(); m != "" {
		return checkRunReport{Line: id + ": сессия не поднята, " + m, Failed: true}
	}
	// Права машинного контура спрашиваются до подъёма по той же причине, что у
	// прогона сценария: окно поднимается без человека, одобрить запрос
	// разрешения в нём некому, и сессия без прав встала бы молча.
	if why := s.permsRefusal(); why != "" {
		return checkRunReport{Failed: true, Line: id +
			": сессия не поднята, права машинного контура на машине не разложены: " + why}
	}
	// Ярус берётся исполнительским вердиктом, без роли ревью: это продолжение
	// той же разработки, а не второй взгляд на неё.
	tier, tierWhy := s.pickTier(proj.Path, id, "")
	model := ""
	if own != nil && own.Default {
		model = own.tierModel(tier)
	}
	// Признак hidden тут не ставится, в отличие от прогона сценария и второго
	// круга ревью (DK-847). Те поднимает тик сам по себе, а этот подъём начал
	// человек своим ответом: он ждёт продолжения разговора и обязан найти его
	// в списке панели, а не гадать, куда делся чат.
	order := runPrompt("in-progress", id)
	if err := s.startTaskSession(proj, id, sess, nil, model, order, order, false); err != nil {
		return checkRunReport{Line: id + ": сессия не поднята, " + err.Error(), Failed: true}
	}
	return checkRunReport{Raised: true, Line: fmt.Sprintf(
		"%s: сессия задачи поднята в tmux-сессии %s ответом человека, %s", id, sess, tierWhy)}
}

// unparkAsk снимает парковку вопросом: признак ожидания и возврат строки в
// In progress. Признак снимается в обоих деревьях, в чекауте и в дереве задачи:
// спрашивают чаще всего из дерева задачи, а отвечают в панели основного
// чекаута, и признак, переживший ответ, рисовал бы в панели вопрос уже не
// ждущей строке.
func (s *server) unparkAsk(root, id string) error {
	name := chat.TaskName(id)
	for _, tree := range askTrees(root, id) {
		if err := chat.DropAsk(tree, name); err != nil {
			s.logf("признак ожидания задачи %s в %s не снялся: %v", id, tree, err)
		}
	}
	out, code, err := s.taskctlWrite(root, "move", id, "in-progress",
		"-m", fmt.Sprintf("docs(tasks): %s разбуждена ответом", id), "--push")
	if err != nil {
		return fmt.Errorf("taskctl move отказал (%d): %v", code, err)
	}
	s.logf("задача %s возвращена в In progress ответом человека: %s", id, strings.TrimSpace(out))
	return nil
}

// askTrees называет деревья, где может лежать признак ожидания задачи: чекаут
// проекта и дерево ветки задачи рядом с ним. Имя дерева собирается тем же
// правилом, что у сторожка (task_tree в tools/devkitctl/watch.py), и
// несуществующее сюда попадает наравне с живым: снятие признака там же и
// кончится отсутствием файла, а это не ошибка.
func askTrees(root, id string) []string {
	top := filepath.Clean(root)
	tree := filepath.Join(filepath.Dir(top), filepath.Base(top)+"-"+strings.ToLower(id))
	return []string{top, tree}
}

// cmdWake это вход команды `dashboard wake <ID>`: подъём сессии разбуженной
// строки. Зовёт её тик сторожка без экрана и без демона, поэтому сервер тут
// собирается на месте, как у подъёма прогона и второго круга.
func cmdWake(home, root string, ids []string, out io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !hasBoard(abs) {
		return fmt.Errorf("доски %s в %s нет: сессия поднимается по строке доски", boardRel, abs)
	}
	if len(ids) == 0 {
		return fmt.Errorf("жду ID разбуженной строки: dashboard wake -C <корень> <ID>")
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	proj := &Project{Name: filepath.Base(abs), Path: abs}
	raw, err := s.projectBoard(proj.Path)
	if err != nil {
		return fmt.Errorf("доска проекта %s не прочиталась: %v", proj.Name, err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return fmt.Errorf("доска проекта %s не разобралась: %v", proj.Name, err)
	}
	failed := false
	for _, id := range ids {
		rep := s.taskWake(proj, strings.TrimSpace(id), rows)
		fmt.Fprintln(out, rep.Line)
		if rep.Failed {
			failed = true
		}
	}
	if failed {
		return errCheckRunFailed
	}
	return nil
}

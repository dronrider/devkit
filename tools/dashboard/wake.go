package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/taskhead"
)

// taskWake обрабатывает ответ из панели сразу после записи во вход задачи.
// Сторожок подхватывает оставшиеся ответы через taskctl wake. Новую голову
// обе дороги поднимают через taskhead.Raise (DK-922).
//
// Перед запуском снимается парковка, чтобы оболочка увидела задачу в работе.
// При занятом замке, закреплённом запуске Codex или сверке прежнего запуска
// ответ остаётся во входе, а строка в работе. При других отказах пытаемся
// вернуть снятую здесь парковку и сообщаем, удалось ли это.
func (s *server) taskWake(proj *Project, id string, rows map[string]boardRow) checkRunReport {
	row, ok := rows[id]
	if !ok {
		return checkRunReport{Line: id + ": строки нет на доске проекта " + proj.Name, Failed: true}
	}
	parked := parkedByAsk(row)
	if !parked && row.Sect != "in-progress" {
		return checkRunReport{Line: id + ": подъём не нужен, строка в " + row.Section}
	}
	// У цели своя оболочка, и на вопросе она не умирает: цикл сторожит признак
	// сам (goal-run.py). Сессию задачи ей поднимать нельзя вовсе, а парковку
	// снять надо: ответ лежит во входе, и цикл читает его своим ходом.
	if isGoalTitle(row.Title) {
		if err := s.unparkIf(proj.Path, id, parked); err != nil {
			return unparkFailed(id, err)
		}
		return checkRunReport{Line: id + ": подъём не нужен, это цель: её цикл переживает вопрос сам"}
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
		// Найденное окно удерживает второй подъём. Снимаем парковку,
		// а ответ оставляем во входе.
		if err := s.unparkIf(proj.Path, id, parked); err != nil {
			return unparkFailed(id, err)
		}
		if talk[name] {
			return checkRunReport{Line: id + ": подъём не нужен, в сессии " + sess + " идёт разговор"}
		}
		return checkRunReport{Line: id + ": подъём не нужен, работа уже идёт в tmux-сессии " + sess}
	}
	if rep := s.headPrecheck(proj, id, sess); rep != nil {
		return *rep
	}
	// Ярус берётся исполнительским вердиктом, без роли ревью: это продолжение
	// той же разработки, а не второй взгляд на неё.
	tier, tierWhy := s.pickTier(proj.Path, id, "")
	model := s.headModel(tier)
	// Парковка снимается последним ходом перед подъёмом: оболочка конвейера
	// спрашивает доску предполётом и поверх строки в Blocked вышла бы тем же
	// стопом wait_human, с которого всё и началось. Снимает её тот же вызов
	// taskctl, каким сводит строку ответ клавишами (settleAsk) и тик сторожка.
	if err := s.unparkIf(proj.Path, id, parked); err != nil {
		return unparkFailed(id, err)
	}
	// Ответ человека запускает видимую сессию, чтобы продолжение можно
	// было открыть из списка разговоров (DK-847).
	order := runPrompt("in-progress", id)
	const hidden, automaticContinuation = false, true
	res, err := s.startTaskSession(proj, id, sess, nil, model, order, order, "работа", hidden, "", automaticContinuation)
	if err != nil {
		// После снятия парковки ответ заберёт живая голова либо дашборд
		// при возобновлении рабочего чата.
		var busy *headBusy
		if errors.As(err, &busy) {
			return checkRunReport{Line: continuationWaitLine(id, busy.where, res.Rung)}
		}
		return checkRunReport{Failed: true, Line: id + ": сессия не поднята, " + err.Error() +
			s.repark(proj.Path, id, row.Block, parked)}
	}
	return checkRunReport{Raised: true, Line: fmt.Sprintf(
		"%s: сессия задачи поднята %s ответом человека, %s", id, headWhere(res, sess), tierWhy)}
}

// unparkIf снимает парковку вопросом у строки, которая ещё стоит в Blocked.
// Разбуженную кем-то раньше строку трогать нечем: move её уже был.
func (s *server) unparkIf(root, id string, parked bool) error {
	if !parked {
		return nil
	}
	// Признак тут ещё лежит, и ждущую сессию подъём читает из него сам.
	return s.unparkAsk(root, id, "")
}

// unparkFailed это отказ, случившийся на снятии парковки. Строка осталась в
// Blocked с тем же вопросом, и подъём повторит тот, кто позвал: панель следующим
// ответом, тик следующим заходом.
func unparkFailed(id string, err error) checkRunReport {
	return checkRunReport{Failed: true, Line: id +
		": сессия не поднята, парковка вопросом не снялась: " + err.Error()}
}

// repark возвращает строку в Blocked с тем же вопросом, когда парковка уже
// снята, а сессия так и не поднялась. Без этого строка стояла бы в работе без
// сессии и без признака ожидания: тик берёт будимых только из Blocked, и
// поднять такую было бы некому, кроме руки человека. Возврат это хвост к строке
// отчёта: молчаливой уборки тут быть не должно, человек читает одну строку.
//
// Заход сюда стоит доске второго коммита с пушем, и живёт возврат ровно на те
// отказы, которых не поймал предполёт: сервер tmux до снятия парковки уже
// раскачан, а на самом окне отказывают вещи разовые (снесённый каталог проекта,
// имя сессии, занятое между проверкой и подъёмом, упёршийся лимит процессов).
// Затяжного сбоя, где такой отказ повторялся бы тиком за тиком, за предполётом
// не остаётся, поэтому шума на доске от возврата не будет.
func (s *server) repark(root, id, block string, parked bool) string {
	if !parked {
		// Парковку снимал не подъём, а тот, кто двигал строку до него: своей
		// причины блока у неё нет, и выдумывать вопрос вместо человека нельзя.
		return "; строка стоит в In progress, и подъём по ней зовётся руками"
	}
	if _, code, err := s.taskctlWrite(root, "move", id, "blocked", "--reason", block,
		"-m", fmt.Sprintf("docs(tasks): %s возвращена в Blocked, сессия не поднялась", id),
		"--push"); err != nil {
		s.logf("задача %s: возврат в Blocked после неудачного подъёма не прошёл (%d): %v", id, code, err)
		return "; вернуть строку в Blocked не вышло: " + err.Error() +
			"; она стоит в In progress без сессии, и поднять её надо руками"
	}
	s.logf("задача %s возвращена в Blocked тем же вопросом: сессия не поднялась", id)
	return "; строка возвращена в Blocked тем же вопросом, подъём повторит следующий заход"
}

// unparkAsk снимает парковку вопросом: признак ожидания и возврат строки в
// In progress. Признак снимается в обоих деревьях, в чекауте и в дереве задачи:
// спрашивают чаще всего из дерева задачи, а отвечают в панели основного
// чекаута, и признак, переживший ответ, рисовал бы в панели вопрос уже не
// ждущей строке.
func (s *server) unparkAsk(root, id, who string) error {
	name := chat.TaskName(id)
	// Адрес ждущей сессии спрашивается до снятия признака: снятый признак его
	// больше не несёт, а именно он говорит, чья работа продолжается ответом.
	// Зовущий, который признак уже снял, называет адрес сам: клавишный ответ
	// убирает признак первым ходом и приходит сюда с пустым файлом.
	if who == "" {
		who = askSession(root, id)
	}
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
	s.workBack(root, id, who)
	return nil
}

// askSession называет сессию, которая ждёт ответа по строке id: поле «сессия»
// признака ожидания. Деревья те же, что у снятия признака, и первый названный
// адрес выигрывает: признак кладёт один ход, а в двух местах он лежит одной и
// той же записью.
func askSession(root, id string) string {
	name := chat.TaskName(id)
	for _, tree := range askTrees(root, id) {
		if a, ok := chat.ReadAsk(chat.AskPath(tree, name)); ok && a.Session != "" {
			return a.Session
		}
	}
	return ""
}

// workBack возвращает записью реестра работу по строке за сессией, которая
// ждала ответа. Возврат строки в In progress делает дашборд от своего имени, и
// записи о работе за этим ходом не остаётся ни одной: отказавшая команда реестр
// не трогает (DK-1003), а повторный move уже стоящей строки отказывает, и сессия
// своего следа не ставит. Без записи привязку, снятую парковкой (DK-716), не
// возвращает ничто: строка доски стоит без «Стопа» и с кнопкой запуска поверх
// идущей работы, а поднятый резюмом разговор не знает своей задачи и живёт
// свободным чатом (цепочка DK-920).
//
// Запись ложится на ждущую сессию, а не на ту, которую поднимет подъём ниже.
// Работу продолжает именно ждущая: реплика человека поднимает её резюмом, и
// задачу подъём берёт из этой записи. Сессия, поднятая заново вместо неё,
// пишет свой след сама, а умерший разговор со записью работы ни строку, ни
// список работ не занимает: те смотрят на живое окно и свежий транскрипт.
func (s *server) workBack(root, id, sid string) {
	if sid == "" {
		// Признак без сессии законен: инструмент ожидания оставляет поле пустым,
		// когда сессия не назвалась, и ждёт безадресные реплики. Возвращать
		// работу тогда некому, и молчать об этом нельзя: строка после ответа
		// стоит без «Стопа», а причина этому одна строка журнала.
		s.logf("задача %s: работа за сессией не возвращена, признак ожидания сессию не назвал", id)
		return
	}
	line := sessions.Line(s.now(), sid, sessions.Bind{Task: id, Source: sessions.BySrc,
		Project: filepath.Base(filepath.Clean(root))}, "возврат в работу ответом человека")
	if err := sessions.Append(sessions.Path(s.cfg.Home), line); err != nil {
		s.logf("задача %s: запись о работе сессии %s не легла: %v", id, sid, err)
		return
	}
	s.logf("задача %s: работа возвращена сессии %s ответом человека", id, sid)
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

// cmdWake поднимает сессию разбуженной строки по явному вызову
// dashboard wake. Сервер собирается из конфига на месте.
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

// headPrecheck это машинные условия подъёма головы: клиент харнеса, права
// машинного контура, оболочка конвейера с профилем и живой сервер tmux. Пустой
// возврат значит, что поднимать есть чем.
//
// Условия эти живут на машине долго и одинаковы у обеих дорог подъёма, ответа
// на вопрос и лежащей реплики. Спрашиваются они до всякой правки доски: без них
// startTaskSession откажет уже за снятой парковкой, и строку пришлось бы
// возвращать в Blocked вторым коммитом. Тот же предполёт стоит у обхода ждущих
// в taskctl (DK-932). Живую сессию под именем задачи тут не спрашивают: у
// ответа на вопрос ей полагается снятая парковка, а лежащей реплике отказ по
// ведущей сессии (taskHead), и реакции у них разные.
func (s *server) headPrecheck(proj *Project, id, sess string) *checkRunReport {
	if m := tmuxMissingCheck(); m != "" {
		return &checkRunReport{Line: id + ": сессия не поднята, " + m, Failed: true}
	}
	if m := claudeMissing(); m != "" {
		return &checkRunReport{Line: id + ": сессия не поднята, " + m, Failed: true}
	}
	// Права машинного контура спрашиваются до подъёма по той же причине, что у
	// прогона сценария: окно поднимается без человека, одобрить запрос
	// разрешения в нём некому, и сессия без прав встала бы молча.
	if why := s.permsRefusal(); why != "" {
		return &checkRunReport{Failed: true, Line: id +
			": сессия не поднята, права машинного контура на машине не разложены: " + why}
	}
	pre, err := s.headRequest(proj, id, sess, nil, "", "", "", "", false, "")
	if err == nil {
		err = taskhead.Preflight(pre)
	}
	if err != nil {
		return &checkRunReport{Failed: true, Line: id + ": сессия не поднята, " + err.Error()}
	}
	// Наличие бинаря в PATH ничего не говорит про живость сервера tmux:
	// сорванный сокет, упёршийся лимит или зависший демон отказывают уже на
	// new-session, и каждый заход тика стоил бы доске двух коммитов с пушем,
	// пока сбой не пройдёт. `start-server` поднимает сервер без единой сессии и
	// молчит, когда он уже стоит, поэтому спросить его дёшево, а затяжной сбой
	// он ловит той же ошибкой, какой встретил бы подъём окна.
	if _, err := runProc("tmux", "start-server"); err != nil {
		return &checkRunReport{Failed: true, Line: id +
			": сессия не поднята, сервер tmux не отвечает: " + procErr(err)}
	}
	return nil
}

// headModel это модель яруса у своего харнеса. Чужой харнес модели не получает:
// имена ярусов у каждого свои, и подставить сюда чужое значило бы поднять голову
// моделью, которой у него нет.
func (s *server) headModel(tier string) string {
	own := s.harnesses().byDefault()
	if own == nil || !own.Default {
		return ""
	}
	return own.tierModel(tier)
}

func continuationWaitLine(id, why, rung string) string {
	if rung == taskhead.RungAppChat {
		return id + ": " + why
	}
	return id + ": подъём не нужен, работа уже идёт (" + why + ")"
}

// taskReplyRaise поднимает голову задачи по реплике, легшей в её чат (DK-1194).
// Дорога та же, что у подъёма ответом: одна лестница носителей, один замок, один
// предполёт. Отличий два, и оба идут от повода.
//
// Первое: секция строки тут не спрашивается вовсе и доска не правится. Реплика
// это повод для хода головы, а не событие строки, и проверенная строка с
// приёмкой человека так и остаётся ждать приёмки. Стоп приёмки на такой ход не
// распространяется (оболочка конвейера, preflight в task-run.py), а после ответа
// голова засыпает тем же стопом.
//
// Реплика остаётся во входе. Общий сторожок повторяет подъём обычных задач,
// а восстановление рабочего запуска Codex ведёт дашборд.
//
// Второй возврат говорит, что человека позвала сама лестница последней ступенью:
// второй баннер о том же ему не нужен.
func (s *server) taskReplyRaise(proj *Project, id string) (checkRunReport, bool) {
	sess := "task-" + id
	if rep := s.headPrecheck(proj, id, sess); rep != nil {
		return *rep, false
	}
	tier, tierWhy := s.pickTier(proj.Path, id, "")
	order := taskhead.ReplyOrder(id)
	const hidden, automaticContinuation = false, true
	res, err := s.startTaskSession(proj, id, sess, nil, s.headModel(tier), order, order, "работа", hidden, "", automaticContinuation)
	if err != nil {
		var busy *headBusy
		if errors.As(err, &busy) {
			return checkRunReport{Line: continuationWaitLine(id, busy.where, res.Rung)}, false
		}
		return checkRunReport{Failed: true, Line: id + ": сессия не поднята, " + err.Error()},
			res.Code == taskhead.CodeCalled
	}
	return checkRunReport{Raised: true, Line: fmt.Sprintf(
		"%s: сессия задачи поднята %s лежащей репликой, %s", id, headWhere(res, sess), tierWhy)}, false
}

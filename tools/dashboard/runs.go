package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Запуск и стоп работ: POST /api/projects/{p}/runs и DELETE
// /api/projects/{p}/runs/{id}. Запуск зовёт тот же механизм, что и руками
// (LLD DK-112): цель поднимает оболочка goal-run.py в её tmux-сессии
// goal-<ID>, одиночная задача это tmux-сессия task-<ID> с оболочкой конвейера
// доски. Голову конвейера оболочка ведёт живой интерактивной сессией в том же
// окне и заказывает ей проходы репликами (DK-724); печатная череда `claude -p`
// осталась у неё запасным входом. Стоп это стоп сессии, ручки «пауза» нет:
// возобновление это новый запуск, читающий состояние с диска.

// goalRunRel это путь оболочки цикла цели внутри чекаута devkit. Сама
// оболочка не ставится в PATH: она python-скрипт при скилле goal-loop, и
// сервер ищет её по корням конфига, где среди проектов лежит и чекаут devkit.
const goalRunRel = "kit/skills/goal-loop/goal-run.py"

// goalRunMissing называет ненайденную оболочку: без неё цель поднимать нечем,
// и молча отвечать «не вышло» нельзя.
const goalRunMissing = "goal-run.py не нашёлся в корнях конфига (" + goalRunRel +
	"): поднимать цикл цели нечем, нужен чекаут devkit в одном из корней"

// inRoots ищет файл чекаута devkit по тем же адресам, где ищутся проекты: сам
// корень или его прямой подкаталог. Пусто, если чекаута devkit в корнях нет.
func inRoots(roots []string, rel string) string {
	for _, root := range roots {
		if p := filepath.Join(root, filepath.FromSlash(rel)); isFile(p) {
			return p
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(root, e.Name(), filepath.FromSlash(rel))
			if isFile(p) {
				return p
			}
		}
	}
	return ""
}

// devkitOwnTree это чекаут devkit, из которого поднят сам дашборд. Служба
// называет его переменной DEVKIT_HOME (тем же способом, каким ищет чекаут
// смок), и спрашивается он раньше корней конфига: агентская часть devkit едет
// вместе с кодом дашборда, ветка POC меняет их вместе, а в корнях рядом лежит
// и чекаут main, где свежих флагов ещё нет.
func devkitOwnTree() string {
	return strings.TrimSpace(os.Getenv("DEVKIT_HOME"))
}

// goalRunPath ищет оболочку цикла цели: сперва в своём дереве, потом по корням
// конфига. Порядок этот не косметика. Дашборд ветки POC звал оболочку из
// первого попавшегося корня, то есть из main, где флага --tier нет вовсе:
// оболочка печатала справку и выходила, а человек видел её целиком вместо
// поднятого цикла (живой случай на цели DK-446). Всякая правка агентской части
// расходилась бы с тем, что запускается на самом деле.
func goalRunPath(roots []string) string {
	if tree := devkitOwnTree(); tree != "" {
		if p := filepath.Join(tree, filepath.FromSlash(goalRunRel)); isFile(p) {
			return p
		}
	}
	return inRoots(roots, goalRunRel)
}

// goalRunUnknownFlag узнаёт ответ оболочки, которая не поняла переданного:
// argparse печатает справку и выходит кодом 2. Показывать эту портянку человеку
// незачем, из неё не видно ни причины, ни что делать (замечание пользователя).
func goalRunUnknownFlag(err error, said string) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		return true
	}
	return strings.Contains(said, "unrecognized arguments") ||
		strings.Contains(said, "usage: goal-run")
}

// goalRunFlagSaid это те самые короткие слова: путь найденной оболочки в них
// стоит нарочно, потому что чинится случай выбором чекаута, а не правкой цели.
func goalRunFlagSaid(path string) string {
	return "версия оболочки цикла не понимает переданных флагов (" + path +
		"): она из другого чекаута devkit, чем сам дашборд"
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// notifyRel это путь уведомителя внутри чекаута devkit. Ищется он там же, где
// оболочка цикла: своего канала наружу дашборд не заводит, отправку держит
// hooks/notify.py (LLD DK-112), и стоп из дашборда зовёт его тем же порядком,
// что taskctl move зовёт его на Check.
const notifierRel = "hooks/notify.py"

// notifierMissing называет ненайденный уведомитель: стоп от этого не
// отменяется, но и молчать про непосланное нельзя, иначе лента показывает
// работающий цикл там, где его уже сняли.
const notifierMissing = "hooks/notify.py не нашёлся в корнях конфига: стоп остался без строки в ленте уведомлений"

// notifierPath ищет уведомитель по тем же адресам, что и оболочку цикла.
func notifierPath(roots []string) string {
	return inRoots(roots, notifierRel)
}

// sayStop зовёт уведомитель поводом run_stop: снятая сессия это то же событие,
// что стоп цикла маркером, и в ленте оно обязано стоять рядом с ним. Пустая
// строка значит, что сказать не вышло, и это приписка к ответу, а не отказ:
// сессия уже снята, и отменять стоп из-за уведомления нельзя.
func (s *server) sayStop(root, project, id, kind string) string {
	np := notifierPath(s.cfg.Roots)
	if np == "" {
		return notifierMissing
	}
	what := "цикл цели"
	if kind != "goal" {
		what = "конвейер задачи"
	}
	title := fmt.Sprintf("%s: %s стоп из дашборда", project, id)
	body := fmt.Sprintf("%s снят из дашборда; возобновление это новый запуск, состояние он прочтёт с диска", what)
	// Задача и проект едут своими ключами: лента ведёт от события к строке
	// доски по полю, а не по разбору заголовка (DK-323), и «Поднять виток»
	// поднимает работу того проекта, где стоп случился.
	cmd := exec.Command("python3", np, "--reason", "run_stop",
		"--task", id, "--project", project, title, body)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("уведомление о стопе не отправлено: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return ""
}

// runPrompt это заказ сессии конвейера одиночной задачи: свежая
// сессия-диспетчер ведёт строку по обычным скиллам доски, как это делается
// руками, своих шагов конвейера у дашборда нет. Заказ зависит от статуса
// строки: из Backlog задачу выполняют, начатую продолжают, проверенную
// закрывают. Слова те же, какими эту работу заказывают в чате, по ним скиллы
// доски и разводят конвейер.
func runPrompt(sect, id string) string {
	switch sect {
	case "in-progress":
		return "Продолжай выполнение " + id
	case "check":
		return "Закрой " + id
	}
	return "Выполни " + id
}

// rowOrder называет заказ дословно, той же строкой, что унесёт runPrompt в
// сессию конвейера (DK-286). Подсказка кнопки на экране читает готовую строку,
// а не пересказывает её ветвление вторым разбором: второй разбор рано или
// поздно разошёлся бы с настоящим заказом. У строки цели нет заказа вовсе:
// его для каждого витка сочиняет goal-run.py, а не дашборд. Нет заказа и у
// проверенной строки с пользовательской приёмкой: она закрывается прямо с
// экрана командой taskctl (closeFromCheck), без сессии агента, и заказывать
// там нечего.
func rowOrder(sect, id, accept, title string) string {
	if isGoalTitle(title) {
		return ""
	}
	if sect == "check" && accept == acceptUser {
		return ""
	}
	return runPrompt(sect, id)
}

// shQuote квотит строку для shell: tmux склеивает хвостовые аргументы
// new-session пробелами и отдаёт их шеллу, без кавычек промпт рассыпался бы
// на отдельные слова, как это уже решено в goal-run.py через shlex.quote.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// findRow ищет строку доски в ответе taskctl: по ней решается, цель это или
// одиночная задача, ею же подписывается живая работа и по её ссылке ищется
// файл цели. Цель узнаётся заголовком от слова «Цель:», как везде на доске.
func findRow(raw json.RawMessage, id string) (boardRow, bool) {
	rows, err := parseBoardRows(raw)
	if err != nil {
		return boardRow{}, false
	}
	row, ok := rows[id]
	return row, ok
}

func isGoalTitle(title string) bool {
	return strings.HasPrefix(title, "Цель:")
}

// procErr разворачивает ошибку подпроцесса в слова: stderr процесса, если он
// что-то сказал, иначе сама ошибка.
func procErr(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return err.Error()
}

// defaultClient это клиент, которым конвейер поднимается без выбора подписки:
// ровно то, что дашборд звал до появления выбора.
const defaultClient = "claude"

// fallbackTier это ярус, которым работа идёт, когда назначить его некому: у
// груминга он тот же самый. Верхний ярус тут не годится, он самый дорогой, а
// нижний не тянет работу конвейера.
const fallbackTier = "pro"

// pickTier спрашивает у вердикта, каким ярусом закрывать задачу. Назначающий
// тут вердикт, а не дашборд: правило доски велит брать исполнителя и ярус у
// agentctl pick, а не выбирать глазом. Вторая строка это слова про то, откуда
// ярус взялся: молчащий вердикт откатывает на pro, и молчать об откате нельзя,
// иначе подмена яруса ничем не видна.
func (s *server) pickTier(dir, id, role string) (string, string) {
	args := []string{"pick", id}
	if role != "" {
		args = append(args, "--role", role)
	}
	out, err := runProcQuiet(dir, true, binPath(agentctlBin), args...)
	if err != nil {
		return fallbackTier, fmt.Sprintf("вердикт agentctl pick не ответил (%s): ярус %s",
			procErr(err), fallbackTier)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "tier:"); ok {
			if tier := strings.TrimSpace(rest); tier != "" {
				return tier, "ярус " + tier + " по вердикту agentctl pick"
			}
		}
	}
	return fallbackTier, fmt.Sprintf("вердикт agentctl pick яруса не назвал: ярус %s", fallbackTier)
}

// runTier сводит два голоса про ярус: выбор человека и вердикт. Человек
// сильнее, вердикт назначает, когда человек не выбирал. Имя, выбранное
// человеком, сверяется с раскладкой подписки той же проверкой, что и имя самой
// подписки: устаревший экран не должен поднимать работу неизвестно чем.
func (s *server) runTier(dir, id, want string, h *Harness, goal bool) (string, string, string) {
	if want != "" {
		if h != nil && len(h.tierNames()) > 0 && h.tierModel(want) == "" {
			return "", "", fmt.Sprintf("яруса %s в раскладке машины нет, ярусы подписки: %s",
				want, strings.Join(h.tierNames(), ", "))
		}
		return want, "ярус " + want + " выбран рукой", ""
	}
	// У цели вердикта на весь цикл нет: витки режет потолок из раздела
	// «Бюджет», а ярус первого витка это тот же pro, что у разбора.
	if goal {
		return fallbackTier, "ярус " + fallbackTier + " по умолчанию цикла цели", ""
	}
	tier, said := s.pickTier(dir, id, "")
	return tier, said, ""
}

// clientMissing называет ненайденный клиент до подъёма tmux-сессии: сессия с
// ненайденной командой умерла бы молча, и стоп был бы неотличим от запуска.
// Имя приезжает раскладкой подписок (`bin` харнеса), а не зашито: у второй
// подписки клиент бывает и другой.
func clientMissing(bin string) string {
	if _, err := exec.LookPath(bin); err != nil {
		return bin + " не нашёлся в PATH: сессию конвейера поднять нечем; " +
			"PATH launchd-агенту дописывает devkitctl doctor --fix"
	}
	return ""
}

func claudeMissing() string {
	return clientMissing(defaultClient)
}

// harnessTail это хвост строки журнала про выбранную подписку: по журналу
// разбирают, куда ушла квота, и запуск без имени от запуска с именем там обязан
// отличаться.
func harnessTail(h *Harness) string {
	if h == nil {
		return ""
	}
	return ", подписка " + h.Name
}

// Пары окружения конвейера те же, что у диалога: их собирает общая сборка
// launchEnv, и своего набора у конвейера нет. Задача и имя
// tmux-сессии для SessionStart-хука, настоящий HOME и заглушка опроса фокуса.
// HOME тут не мелочь: tmux-сервер, поднятый демоном, наследует его подложный
// дом, agentctl exec разворачивает в нём тильду раскладки харнеса, и
// CLAUDE_CONFIG_DIR второй подписки указывал в пустой каталог демона, а клиент
// отвечал «Not logged in» (живой случай, запуск DK-269 на второй подписке).
// Окружение приходит доводом, а не собирается тут: собирает его одно место на
// все дороги подъёма (launchEnv), и звать его в обход сервера некому.

// taskRunRel это путь оболочки конвейера задачи внутри чекаута devkit. Ищется
// она там же и тем же порядком, что оболочка цикла цели.
const taskRunRel = "kit/skills/board-task/task-run.py"

const taskRunMissing = "task-run.py не нашёлся в корнях конфига (" + taskRunRel +
	"): поднимать конвейер задачи нечем, нужен чекаут devkit в одном из корней"

func taskRunPath(roots []string) string {
	if tree := devkitOwnTree(); tree != "" {
		if p := filepath.Join(tree, filepath.FromSlash(taskRunRel)); isFile(p) {
			return p
		}
	}
	return inRoots(roots, taskRunRel)
}

// taskRunHead это голова команды конвейера: оболочка с задачей, корнем и
// заказами обоих сортов. Заказ первого прохода зависит от статуса строки, заказ
// следующих всегда «продолжай»: строку к тому времени уже двигали, и начинать
// её заново нельзя. Слова обоих заказов сочиняет дашборд и только он, оболочка
// их не пересказывает.
func taskRunHead(runner, id, root, order, again, project string) string {
	return "python3 " + shQuote(runner) + " " + shQuote(id) +
		" -C " + shQuote(root) +
		" --project " + shQuote(project) +
		" --order " + shQuote(order) +
		" --again " + shQuote(again) + " --"
}

// sessionCommand собирает команду tmux-сессии конвейера: оболочка, а за нею
// клиент без заказа. Заказ каждому проходу оболочка приставляет сама, теми
// словами, что пришли ей отсюда, и она же решает, вести голову живой сессией
// или печатной чередой. Прежде тут стоял голый `claude -p '<заказ>'`, и
// конвейер жил ровно один ход головы: ход кончался, процесс выходил, окно
// закрывалось, а задача оставалась в работе (DK-691).
func sessionCommand(agentctl, runner string, h *Harness, env, order, again, id, root, project, model string) string {
	return env + taskRunHead(runner, id, root, order, again, project) + " " +
		clientCommand(agentctl, h, model)
}

// clientCommand это клиент с флагами и без заказа. Без выбранной подписки это
// `claude`, с выбранной команда заворачивается в `agentctl exec`: пары окружения
// подписки кладёт он, и значения при этом никуда не уезжают, ни в веб-сервер, ни
// в панель сессии, которую дашборд показывает на экране (LLD DK-328, решение 3).
// Собирать пары тут самому нельзя по той же причине: токен и base URL второй
// подписки поселились бы в процессе, который эти панели и раздаёт.
func clientCommand(agentctl string, h *Harness, model string) string {
	if h == nil {
		// Режим разрешений ставится и своей подписке. Живая голова стоит в
		// окне без человека, а печатный проход идёт и вовсе без окна, и запрос
		// разрешения одобрить некому: клиент отвечает отказом, а работа встаёт
		// молча.
		// Своё умолчание оболочка проходов ставит сама, но строка клиента
		// приезжает к ней отсюда и умолчание перебивает, так что сказать это
		// надо тут (DK-739).
		client := defaultClient + " --permission-mode auto"
		// Ярус называется явно: без флага клиент берёт свой дефолт, и работа
		// уходила верхним ярусом, которого ей никто не назначал.
		if model != "" {
			client += " --model " + shQuote(model)
		}
		return client
	}
	// agentctl зовётся полным путём: сессия наследует PATH дашборда, а под
	// launchd он системный, и утилиты devkit в нём может не быть вовсе. Клиент
	// подписки остаётся именем: его ищет тот же PATH, каким дашборд находил
	// claude до этой задачи, и проверка «не нашёлся» идёт по нему же.
	// Имя, путь и клиент квотятся наравне с заказом: строка уходит шеллу сессии,
	// и пробел в пути рассыпал бы команду на слова.
	// Режим разрешений едет флагом по той же причине, что у чата (chatCmd):
	// свежий профиль второй подписки поднимает клиента в ручном режиме, и
	// конвейер вставал бы на вопросе разрешения, которого некому увидеть.
	tier := ""
	// Ярус называется и второй подписке: её клиент принимает имя из своей
	// лестницы той же дорогой, что и чат (DK-750), а без флага сессия шла бы
	// дефолтом профиля, которого никто не назначал.
	if model != "" {
		tier = " --model " + shQuote(model)
	}
	return shQuote(agentctl) + " exec --harness " + shQuote(h.Name) + " -- " +
		shQuote(h.Bin) + " --permission-mode auto" + tier
}

// startTaskSession поднимает tmux-сессию конвейера задачи: оболочка, клиент и
// заказы обоих сортов. Дорога сюда не одна: кнопка экрана и подъём
// прогона после выката (checkrun.go) поднимают одно и то же окно, и правила
// заказа (план, канал ответа) обоим приставляются тут, а не пересказываются
// каждым зовущим.
func (s *server) startTaskSession(proj *Project, id, sess string, h *Harness, model, order, again string) error {
	// Оболочка ищется до подъёма окна: без неё конвейер прожил бы один ход
	// головы, и отказать тут честнее, чем поднять работу, которая умрёт на
	// первом же ожидании.
	tr := taskRunPath(s.cfg.Roots)
	if tr == "" {
		return errors.New(taskRunMissing)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", proj.Path,
		sessionCommand(binPath(agentctlBin), tr, h, s.headlessEnv(id, sess),
			order+" "+orderRules(sess), again+" "+orderRules(sess),
			id, proj.Path, proj.Name, model)); err != nil {
		return fmt.Errorf("tmux не поднял сессию %s: %s", sess, procErr(err))
	}
	// Конвейер встаёт под того же сторожа, что и разговор (chatwatch.go): без
	// отметки его смерть не замечал никто, и оборванная работа стояла до тех
	// пор, пока человек сам не напишет задаче (DK-660).
	s.chatRaised(sess, "", id, proj.Name)
	return nil
}

func (s *server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("запуск отклонён: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "запуск")
	if found == nil {
		return
	}
	var body struct {
		ID string `json:"id"`
		// Harness это выбранная подписка. Пусто значит «как раньше»: список не
		// прочитан или экран старый, и работа идёт на подписке по умолчанию.
		Harness string `json:"harness"`
		// Tier это выбранный человеком ярус. Пусто значит «как назначено»: у
		// задачи ярус называет вердикт agentctl pick, у цели это pro.
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		s.logf("запуск отклонён: битое тело запроса 400")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\": \"DK-NNN\"}"})
		return
	}
	id := body.ID
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		s.logf("запуск %s в %s не удался: доска не прочиталась 502: %v", id, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	row, ok := findRow(raw, id)
	if !ok {
		s.logf("запуск %s в %s отклонён: нет строки на доске 404", id, found.Name)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": rowGone(found, id)})
		return
	}
	// Проверенная задача с пользовательской приёмкой закрывается тут же, своей
	// командой: кнопка «Закрыть» у неё это подтверждение принятого глазами, а не
	// заказ работы, и сессия агента ради taskctl close стоила бы минут ожидания
	// и квоты (DK-289). Вид приёмки приезжает суффиксом строки доски, читать его
	// больше неоткуда, и решается всё до проверок tmux: закрытие ими не связано.
	if row.Sect == "check" && row.Accept == acceptUser {
		if body.Harness != "" {
			// Подписка тут ни при чём: закрытие идёт командой taskctl, сессии
			// агента за ним нет, и квоты оно не тратит. В журнале это сказано,
			// чтобы выбор не выглядел потерянным.
			s.logf("закрытие %s в %s: выбранная подписка %s не понадобилась, сессии агента тут нет", id, found.Name, body.Harness)
		}
		s.closeFromCheck(w, found, id)
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		s.logf("запуск %s в %s отклонён: tmux не нашёлся 502", id, found.Name)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	kind := "task"
	if isGoalTitle(row.Title) {
		kind = "goal"
	}
	sess := kind + "-" + id
	talk := s.tmuxTalk(found.Path)
	for _, name := range tmuxSessions() {
		if name != "goal-"+id && name != "task-"+id {
			continue
		}
		if !talk[name] {
			s.logf("запуск %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): запускать поверх живой сессии нельзя, сначала стоп", id, name)})
			return
		}
		// Сессия без хода это досчитавший разговор (чаще всего груминг, который
		// кончился строкой и остался стоять на приглашении). Работой строка его
		// не считает, кнопку запуска показывает, и отказывать тут значило бы
		// обещать кнопкой то, чего ручка не делает: остаток снимается, а на его
		// месте поднимается заказанная работа.
		s.logf("запуск %s в %s: остаток разговора в tmux-сессии %s снят", id, found.Name, name)
		s.chatWatchOff(name)
		runProc("tmux", "kill-session", "-t", name)
	}
	// Выбранная подписка сверяется с раскладкой машины: имени, которого в ней
	// нет, верить нельзя, иначе экран, устаревший на смену конфига, поднимал бы
	// сессию неизвестно на чём. Проверка одна на цель и на задачу: витки цели
	// платятся той же квотой, которую человек выбрал (замечание пользователя).
	var harness *Harness
	if body.Harness != "" {
		h, why := s.harnesses().pick(body.Harness)
		if h == nil {
			s.logf("запуск %s в %s отклонён: %s", id, found.Name, why)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
			return
		}
		harness = h
	}
	// Ярус называется до подъёма: у задачи его назначает вердикт, у цели это
	// pro, и выбор человека сильнее обоих.
	own := harness
	if own == nil {
		own = s.harnesses().byDefault()
	}
	tier, tierWhy, tierBad := s.runTier(found.Path, id, body.Tier, own, kind == "goal")
	if tierBad != "" {
		s.logf("запуск %s в %s отклонён: %s", id, found.Name, tierBad)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tierBad})
		return
	}
	// У цели заказ один на все статусы: и «выполнить», и «продолжить» это
	// следующий виток, а промпт витка сочиняет не дашборд, а сама оболочка
	// цикла, и лезть в её слова отсюда нечем. Подписка едет ей флагом: витки
	// она заводит сама, и без флага платила бы подпиской по умолчанию.
	if kind == "goal" {
		gr := goalRunPath(s.cfg.Roots)
		if gr == "" {
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, goalRunMissing)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": goalRunMissing})
			return
		}
		args := []string{gr, id, "-C", found.Path}
		if harness != nil {
			args = append(args, "--harness", harness.Name)
		}
		// Ярус едет оболочке той же дорогой, что и подписка: разворачивать его
		// моделью она будет сама, раскладкой машины (agentctl harness --json).
		// Собирать модель тут значило бы решать за неё на каждом витке, а витков
		// у цикла много и подписка между ними меняется.
		if tier != "" {
			args = append(args, "--tier", tier)
		}
		out, err := runProc("python3", args...)
		if err != nil {
			text := procErr(err)
			code := http.StatusBadGateway
			var ee *exec.ExitError
			// Код 3 у оболочки это занятый замок или живая сессия: цикл уже
			// идёт, и это конфликт, а не поломка.
			if errors.As(err, &ee) && ee.ExitCode() == 3 {
				code = http.StatusConflict
			} else if goalRunUnknownFlag(err, text) {
				// Справку человеку не показываем: от неё ни причины, ни что
				// делать, а сказать надо ровно одно, и с путём.
				text = goalRunFlagSaid(gr)
			}
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, text)
			writeJSON(w, code, map[string]string{"error": text})
			return
		}
		s.logf("цель %s поднята в %s (tmux-сессия %s%s), %s", id, found.Name, sess, harnessTail(harness), tierWhy)
		goalOut := map[string]string{
			"id": id, "kind": kind, "session": sess, "tier": tier,
			"message": strings.TrimSpace(string(out)),
		}
		if harness != nil {
			goalOut["harness"] = harness.Name
			goalOut["message"] = fmt.Sprintf("цикл цели %s поднят на подписке %s (tmux-сессия %s)", id, harness.Name, sess)
		}
		goalOut["message"] = strings.TrimSpace(goalOut["message"]) + ", " + tierWhy
		writeJSON(w, http.StatusOK, goalOut)
		return
	}
	client := defaultClient
	if harness != nil {
		client = harness.Bin
	}
	if m := clientMissing(client); m != "" {
		s.logf("запуск задачи %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Модель конвейера чужой подписки пишется в память диалога: панель без
	// записи показывала бы в селекторе умолчание первой подписки поверх
	// второй (живой случай, запуск DK-269 показывал opus). Пишется модель того
	// же яруса, которым идёт работа, и он же уезжает в заказ клиенту.
	if harness != nil {
		if m := harness.tierModel(tier); m != "" {
			if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: m}); err != nil {
				s.logf("модель конвейера %s не записалась: %v", sess, err)
			}
		}
	}
	// Ярус называется флагом всякой подписке: клиент второй подписки принимает
	// имя из своей лестницы (DK-750), а без флага берёт свой дефолт, и он
	// бывает верхним ярусом, которого никто не назначал (находка этого
	// захода).
	model := ""
	if own != nil {
		model = own.tierModel(tier)
	}
	if err := s.startTaskSession(found, id, sess, harness, model,
		runPrompt(row.Sect, id), runPrompt("in-progress", id)); err != nil {
		s.logf("запуск задачи %s в %s не удался: %s", id, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// Строка результата называет подписку: подмена квоты иначе ничем не видна, а
	// сказано тут именно про сессию конвейера. Куда она отдаст исполнителя
	// дальше, решает вердикт pick по лестнице этой подписки, и обещать тут
	// большее значило бы обещать чужую настройку.
	resp := map[string]string{"id": id, "kind": kind, "session": sess, "tier": tier,
		"message": fmt.Sprintf("конвейер задачи %s поднят в tmux-сессии %s", id, sess)}
	if harness != nil {
		resp["harness"] = harness.Name
		resp["message"] = fmt.Sprintf("конвейер задачи %s поднят на подписке %s (tmux-сессия %s)", id, harness.Name, sess)
	}
	if model != "" {
		resp["model"] = model
	}
	// Откуда взялся ярус, сказано словами и в ответе, и в журнале: молчащий
	// вердикт подменяет назначение, и человеку это видно.
	resp["message"] += ", " + tierWhy
	s.logf("задача %s поднята в %s (tmux-сессия %s%s), %s", id, found.Name, sess, harnessTail(harness), tierWhy)
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("стоп отклонён: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "стоп")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if m := tmuxMissingCheck(); m != "" {
		s.logf("стоп %s отклонён: tmux не нашёлся 502", id)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		s.logf("стоп %s не удался: доска не прочиталась 502: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	view, err := parseBoardView(raw)
	if err != nil {
		s.logf("стоп %s не удался: ответ taskctl не разобрался 502: %v", id, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return
	}
	// Стоп симметричен запуску: работа ищется среди живых работ этого
	// проекта, где tmux-сессии привязаны префиксом его доски (liveWorks,
	// правило DK-217), а не в общем списке сессий машины. Иначе стоп на
	// доске demo снимал бы чужую goal-DK-777 и заводил через --say журнал
	// в чужом корне.
	var work *Work
	works := s.liveWorks(found.Path, view.Prefix, raw)
	for i := range works {
		if works[i].ID == id {
			work = &works[i]
			break
		}
	}
	if work == nil {
		s.logf("стоп %s отклонён: работа не идёт 404", id)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("работа %s в проекте %s не идёт: нет ни tmux-сессии с префиксом его доски, ни записи в реестре целей", id, found.Name)})
		return
	}
	if work.Via == "session" {
		s.logf("стоп %s отклонён: интерактивная сессия 409", id)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("работа %s это интерактивная сессия: её ведёт человек в окне, снимать нечего", id)})
		return
	}
	if work.Via == "registry" {
		s.logf("стоп %s отклонён: цикл ведёт другая сессия 409", id)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("цикл цели %s ведёт другая сессия, tmux-сессии дашборда у него нет: "+
				"стоп отсюда недоступен, снимать там, где цикл поднят", id)})
		return
	}
	kind := work.Kind
	sess := kind + "-" + id
	resp := map[string]string{"id": id, "kind": kind, "session": sess, "state": "стоп"}
	if kind == "goal" {
		// Строка про стоп идёт в журнал цикла до убийства сессии: стоп виден
		// там же, где виден ход. Провал записи стоп не отменяет, но называется.
		if gr := goalRunPath(s.cfg.Roots); gr == "" {
			resp["note"] = goalRunMissing
		} else if _, err := runProc("python3", gr, id, "-C", found.Path, "--say", "стоп из дашборда"); err != nil {
			resp["note"] = fmt.Sprintf("строка про стоп в журнал цикла не записалась: %s", procErr(err))
		}
		if resp["note"] != "" {
			s.logf("стоп %s в %s: %s", id, found.Name, resp["note"])
		}
	}
	// Имя сессии задачи одно на конвейер и на разбор черновика, и разбор ходит
	// под присмотром: снятое кнопкой стопа смертью не считается.
	s.chatWatchOff(sess)
	if _, err := runProc("tmux", "kill-session", "-t", "="+sess); err != nil {
		text := fmt.Sprintf("tmux не снял сессию %s: %s", sess, procErr(err))
		s.logf("стоп %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	// Стоп говорит о себе тем же уведомителем, что виток и taskctl move:
	// снятая сессия иначе видна только тому, кто нажал кнопку, а лента для
	// того и заведена, чтобы стоп доехал до второго устройства.
	if note := s.sayStop(found.Path, found.Name, id, kind); note != "" {
		resp["note"] = strings.TrimPrefix(resp["note"]+"; "+note, "; ")
		s.logf("стоп %s в %s: %s", id, found.Name, note)
	}
	if kind == "goal" {
		resp["message"] = fmt.Sprintf("стоп: tmux-сессия %s снята, замок оболочки отпадёт сам по мёртвому pid; возобновление это новый запуск, виток прочтёт состояние с диска", sess)
	} else {
		resp["message"] = fmt.Sprintf("стоп: tmux-сессия %s снята; возобновление это новый запуск, конвейер прочтёт состояние с диска", sess)
	}
	s.logf("стоп %s в %s: tmux-сессия %s снята", id, found.Name, sess)
	writeJSON(w, http.StatusOK, resp)
}

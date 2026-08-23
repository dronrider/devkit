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
// goal-<ID>, одиночная задача это tmux-сессия task-<ID> с headless-сессией
// конвейера доски. Стоп это стоп сессии, ручки «пауза» нет: возобновление
// это новый запуск, читающий состояние с диска.

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

// goalRunPath ищет оболочку цикла цели.
func goalRunPath(roots []string) string {
	return inRoots(roots, goalRunRel)
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

// runPrompt это промпт headless-сессии конвейера одиночной задачи: свежая
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
// headless-сессию (DK-286). Подсказка кнопки на экране читает готовую строку,
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

// clientMissing называет ненайденный клиент до подъёма tmux-сессии: сессия с
// ненайденной командой умерла бы молча, и стоп был бы неотличим от запуска.
// Имя приезжает раскладкой подписок (`bin` харнеса), а не зашито: у второй
// подписки клиент бывает и другой.
func clientMissing(bin string) string {
	if _, err := exec.LookPath(bin); err != nil {
		return bin + " не нашёлся в PATH: headless-сессию конвейера поднять нечем; " +
			"PATH launchd-агенту дописывает devkitctl doctor --fix"
	}
	return ""
}

func claudeMissing() string {
	return clientMissing(defaultClient)
}

// sessionCommand собирает команду tmux-сессии конвейера. Без выбранной подписки
// это прежний `claude -p '<заказ>'`. С выбранной команда заворачивается в
// `agentctl exec`: пары окружения подписки кладёт он, и значения при этом
// никуда не уезжают, ни в веб-сервер, ни в панель сессии, которую дашборд
// показывает на экране (LLD DK-328, решение 3). Собирать пары тут самому нельзя
// по той же причине: токен и base URL второй подписки поселились бы в процессе,
// который эти панели и раздаёт.
// harnessTail это хвост строки журнала про выбранную подписку: по журналу
// разбирают, куда ушла квота, и запуск без имени от запуска с именем там обязан
// отличаться.
func harnessTail(h *Harness) string {
	if h == nil {
		return ""
	}
	return ", подписка " + h.Name
}

// Пары окружения конвейера те же, что у диалога (chatVars): задача и имя
// tmux-сессии для SessionStart-хука, настоящий HOME и заглушка опроса фокуса.
// HOME тут не мелочь: tmux-сервер, поднятый демоном, наследует его подложный
// дом, agentctl exec разворачивает в нём тильду раскладки харнеса, и
// CLAUDE_CONFIG_DIR второй подписки указывал в пустой каталог демона, а клиент
// отвечал «Not logged in» (живой случай, запуск DK-269 на второй подписке).
func sessionCommand(agentctl string, h *Harness, prompt, id, sess string) string {
	if h == nil {
		return chatVars(id, sess) + defaultClient + " -p " + shQuote(prompt)
	}
	// agentctl зовётся полным путём: сессия наследует PATH дашборда, а под
	// launchd он системный, и утилиты devkit в нём может не быть вовсе. Клиент
	// подписки остаётся именем: его ищет тот же PATH, каким дашборд находил
	// claude до этой задачи, и проверка «не нашёлся» идёт по нему же.
	// Имя, путь и клиент квотятся наравне с заказом: строка уходит шеллу сессии,
	// и пробел в пути рассыпал бы команду на слова.
	return chatVars(id, sess) + shQuote(agentctl) + " exec --harness " + shQuote(h.Name) + " -- " +
		shQuote(h.Bin) + " -p " + shQuote(prompt)
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
	// Цикл цели поднимает оболочка goal-run своей сессией, и подписку ей
	// передать нечем: виток она заводит сама, каждый раз заново. Отказ тут
	// честнее молчаливого запуска на подписке по умолчанию, потому что имя
	// человек уже выбрал.
	if kind == "goal" && body.Harness != "" {
		why := fmt.Sprintf("цель %s поднимает оболочка цикла goal-run своей сессией, и выбор подписки до неё не доезжает: "+
			"виток пойдёт на подписке по умолчанию, а выбрать её можно у одиночной задачи", id)
		s.logf("запуск цели %s в %s отклонён: выбор подписки цели не передаётся", id, found.Name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
		return
	}
	sess := kind + "-" + id
	for _, name := range tmuxSessions() {
		if name == "goal-"+id || name == "task-"+id {
			s.logf("запуск %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): запускать поверх живой сессии нельзя, сначала стоп", id, name)})
			return
		}
	}
	// У цели заказ один на все статусы: и «выполнить», и «продолжить» это
	// следующий виток, а промпт витка сочиняет не дашборд, а сама оболочка
	// цикла, и лезть в её слова отсюда нечем.
	if kind == "goal" {
		gr := goalRunPath(s.cfg.Roots)
		if gr == "" {
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, goalRunMissing)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": goalRunMissing})
			return
		}
		out, err := runProc("python3", gr, id, "-C", found.Path)
		if err != nil {
			text := procErr(err)
			code := http.StatusBadGateway
			var ee *exec.ExitError
			// Код 3 у оболочки это занятый замок или живая сессия: цикл уже
			// идёт, и это конфликт, а не поломка.
			if errors.As(err, &ee) && ee.ExitCode() == 3 {
				code = http.StatusConflict
			}
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, text)
			writeJSON(w, code, map[string]string{"error": text})
			return
		}
		s.logf("цель %s поднята в %s (tmux-сессия %s)", id, found.Name, sess)
		writeJSON(w, http.StatusOK, map[string]string{
			"id": id, "kind": kind, "session": sess,
			"message": strings.TrimSpace(string(out)),
		})
		return
	}
	// Выбранная подписка сверяется с раскладкой машины: имени, которого в ней
	// нет, верить нельзя, иначе экран, устаревший на смену конфига, поднимал бы
	// сессию неизвестно на чём.
	var harness *Harness
	if body.Harness != "" {
		h, why := s.harnesses().pick(body.Harness)
		if h == nil {
			s.logf("запуск задачи %s в %s отклонён: %s", id, found.Name, why)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
			return
		}
		harness = h
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
	// Модель конвейера чужой подписки пишется в память диалога: сам заказ её
	// не называет (клиент подписки берёт свою по умолчанию), а панель без
	// записи показывала бы в селекторе умолчание первой подписки поверх
	// второй (живой случай, запуск DK-269 показывал opus). Пишется ярус pro: им
	// подписка и поднимает клиента.
	if harness != nil {
		if m := harness.tierModel("pro"); m != "" {
			if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: m}); err != nil {
				s.logf("модель конвейера %s не записалась: %v", sess, err)
			}
		}
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		sessionCommand(binPath(agentctlBin), harness, runPrompt(row.Sect, id)+" "+planRuleFor(sess),
			id, sess)); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("запуск задачи %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	// Строка результата называет подписку: подмена квоты иначе ничем не видна, а
	// сказано тут именно про сессию конвейера. Куда она отдаст исполнителя
	// дальше, решает вердикт pick по лестнице этой подписки, и обещать тут
	// большее значило бы обещать чужую настройку.
	resp := map[string]string{"id": id, "kind": kind, "session": sess,
		"message": fmt.Sprintf("конвейер задачи %s поднят в tmux-сессии %s", id, sess)}
	if harness != nil {
		resp["harness"] = harness.Name
		resp["message"] = fmt.Sprintf("конвейер задачи %s поднят на подписке %s (tmux-сессия %s)", id, harness.Name, sess)
	}
	s.logf("задача %s поднята в %s (tmux-сессия %s%s)", id, found.Name, sess, harnessTail(harness))
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

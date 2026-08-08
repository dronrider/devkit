package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// agentctl run это точка входа делегирования: печатается тот же вердикт, что у
// pick, а дальше работу забирает режим [delegate] харнеса назначения ступени.
// Дизайн в docs/lld/DK-033-universal-kit.md (раздел «Делегирование: agentctl
// run») и в docs/lld/DK-090-heterogeneous-ladder.md (раздел «Делегирование: run
// отдаёт работу харнесу назначения»).

const (
	harnessEnv  = "DEVKIT_HARNESS"
	runDepthEnv = "DEVKIT_RUN_DEPTH"
	// Код «делегировать нечем». Ненулевой сознательно: скрипт поверх run не
	// должен принять отказ за сделанную работу. Тройка, а не единица, чтобы
	// отличаться от ошибок самого run (нет задачи, битый профиль, вложенный
	// запуск), после которых делегирование даже не начиналось.
	codeNothingToDelegate = 3
)

// runDepthRefusal это ограничитель вложенности. Подпроцесс режима cli это
// полноценная сессия агента, она читает те же правила, а правила велят
// делегировать через run: без ограничителя выходит воронка из сессий, то есть
// потраченная подписка. Флага обхода нет сознательно, сценария, где воронка
// легальна, не придумано. Нечисловое значение переменной считается взведённым
// ограничителем: молча пропустить его хуже, чем отказать.
func runDepthRefusal(value, id string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	if n, err := strconv.Atoi(v); err == nil && n < 1 {
		return nil
	}
	return fmt.Errorf("вложенное делегирование запрещено: эту сессию подняли через run (%s=%s), и делегировать дальше нельзя, иначе выйдет воронка сессий; задачу %s исполняй сам", runDepthEnv, v, id)
}

func roleWord(role string) string {
	if role == roleReview {
		return "ревьювер"
	}
	return "исполнитель"
}

// stripFrontmatter отрезает frontmatter определения агента: подпроцессу едет
// тело, а поля frontmatter для чужого инструмента ничего не значат. Модель и
// effort из них не теряются, они уходят в команду плейсхолдерами.
func stripFrontmatter(text string) string {
	const fence = "---\n"
	if !strings.HasPrefix(text, fence) {
		return text
	}
	rest := text[len(fence):]
	i := strings.Index(rest, "\n"+fence)
	if i < 0 {
		return text
	}
	return strings.TrimLeft(rest[i+len(fence)+1:], "\n")
}

// agentPrompt собирает промпт подпроцесса: тело нейтрального определения агента
// плюс блок задачи. Ровно то, что диспетчер передаёт субагенту на харнесе со
// своим спавном, только собранное в один текст.
func agentPrompt(kit, role, effort, id, workdir string) (string, error) {
	path := filepath.Join(kit, "agents", role+"-"+effort+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("определения агента %s нет (%v): маппинг такого effort не выдаёт, под override-строку определение заводится по месту, см. tools/agentctl/README.md, раздел «Определения субагентов»", path, err)
	}
	body := strings.TrimSpace(stripFrontmatter(string(data)))
	task := fmt.Sprintf("Задача %s, роль: %s. Рабочая директория: %s. Постановка в docs/tasks/%s.md, метаданные в строке доски.",
		id, roleWord(role), workdir, id)
	return body + "\n\n" + task, nil
}

// substituteCommand подставляет плейсхолдеры в шаблон команды. Подстановка идёт
// внутри элемента, а не по целому элементу: у CLI бывает и «--model={model}».
// Незнакомая фигурная скобка остаётся как есть: в аргументах чужого инструмента
// живут и JSON, и шаблоны его собственного языка, и отказывать из-за них run не
// вправе.
func substituteCommand(tmpl []string, vals map[string]string) []string {
	argv := make([]string, 0, len(tmpl))
	for _, a := range tmpl {
		for k, v := range vals {
			a = strings.ReplaceAll(a, "{"+k+"}", v)
		}
		argv = append(argv, a)
	}
	return argv
}

// refreshQuota освежает снимок квоты названного харнеса. Отдельной переменной
// ради тестов: настоящая работа поднимает клиента чужого инструмента, и в
// тестах ей делать нечего.
var refreshQuota = refreshQuotaDetached

// refreshExecutorQuota освежает снимок у харнеса-исполнителя, а не у активного:
// тратиться будет его подписка, и протухший снимок делает решение корректора
// слепым именно по ней. Освежать нечем (пустая [quota]) или снимок свежий,
// значит run молчит: про состояние снимка уже сказано вердиктом.
func refreshExecutorQuota(l *layers, harness string, now time.Time) {
	q := quotaSpecOf(l, harness)
	if q == nil {
		return
	}
	if s, err := q.read(); err == nil && !s.empty() && s.fresh(now) {
		return
	}
	refreshQuota(harness)
}

// Замок съёма остатка: тот же, что у SessionStart-хука (hooks/quota-refresh.sh).
// Держит он две вещи сразу. Первая: съём поднимает клиента, старт которого
// дёргает тот же хук, и без замка вышла бы воронка. Вторая: параллельные сессии
// не снимают панель наперегонки. Замок это директория, mkdir атомарен.
const refreshLockStale = 10 * time.Minute

func refreshLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".devkit", "quota-refresh.lock")
}

// takeRefreshLock берёт замок. Брошенный замок старше десяти минут снимается:
// весь цикл съёма укладывается в минуту, а сессия, умершая с замком в руке,
// иначе выключила бы освежение навсегда.
func takeRefreshLock(path string, now time.Time) bool {
	if fi, err := os.Stat(path); err == nil {
		if now.Sub(fi.ModTime()) < refreshLockStale {
			return false
		}
		os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	return os.Mkdir(path, 0o755) == nil
}

// refreshQuotaDetached запускает отцепленный `agentctl quota refresh --if-stale`
// от имени названного харнеса и сразу возвращается: делегирование ждать съёма
// не должно, вердикт уже посчитан и напечатан, а свежий снимок нужен сессии
// подпроцесса и следующему запуску. Вывод остаётся в журнале рядом с замком,
// иначе про фоновую работу нельзя узнать вообще ничего.
func refreshQuotaDetached(harness string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	lock := refreshLockPath()
	if !takeRefreshLock(lock, timeNow()) {
		return
	}
	log, err := os.Create(filepath.Join(filepath.Dir(lock), "quota-refresh.log"))
	if err != nil {
		os.Remove(lock)
		return
	}
	cmd := exec.Command(exe, "quota", "refresh", "--if-stale")
	cmd.Env = append(os.Environ(), harnessEnv+"="+harness)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		os.Remove(lock)
		return
	}
	go func() {
		cmd.Wait()
		log.Close()
		os.Remove(lock)
	}()
}

// cmdRun печатает вердикт и отдаёт задачу тому, кого назвала строка via.
// Возвращает код выхода: ноль это сделанная работа (или инструкция диспетчеру
// на харнесе со своим спавном), три это «делегировать нечем», код подпроцесса
// проезжает наружу как есть. Ошибка это отказ самого run, её печатает main
// единицей.
func cmdRun(root, id string, record bool, role, goal, workdir string, out, errw io.Writer) (int, error) {
	if err := runDepthRefusal(os.Getenv(runDepthEnv), id); err != nil {
		return 0, err
	}
	if workdir == "" {
		workdir = root
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return 0, err
	}
	workdir = abs
	if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("рабочей директории %s нет: дерево задачи заводит shipctl start, а путь к нему run принимает флагом --workdir", workdir)
	}
	p, err := pickVerdict(root, id, record, role, goal)
	if err != nil {
		return 0, err
	}
	fmt.Fprintln(out, p.Text)
	v, hc := p.V, p.HC
	who := v.Via
	if who == "" || who == unmappedModel {
		fmt.Fprintf(errw, "делегировать нечем: ярус %s разворачивать нечем, исполнителя вердикт не назвал; разобраться: agentctl harness\n", v.Tier)
		return codeNothingToDelegate, nil
	}
	var prof *profile
	if hc.L != nil {
		prof = hc.L.Profiles[who]
	}
	if prof == nil {
		fmt.Fprintf(errw, "делегировать нечем: ярус %s назначен в %s, а профиля такого харнеса нет, и чем его поднимать, сказать некому; разобраться: agentctl harness\n", v.Tier, who)
		return codeNothingToDelegate, nil
	}
	del := prof.section("delegate")
	mode := del.str("mode")
	switch mode {
	case "native":
		// Пара «уехавшая ступень плюс native» отказная по построению: native
		// значит, что спавн субагента есть только внутри самого инструмента, и
		// снаружи, из чужой сессии, дёрнуть его нечем.
		if hc.Models.away(v.Tier) {
			fmt.Fprintf(errw, "делегировать нечем: ступень %s уезжает в %s, а он делегирует режимом native, то есть спавном субагента внутри своей же сессии, и снаружи дёрнуть его нечем; либо сменить назначение яруса в %s, либо исполнять задачу сессией %s\n",
				v.Tier, who, machineConfigPath(), who)
			return codeNothingToDelegate, nil
		}
		fmt.Fprintf(out, "делегирование: native (харнес %s, профиль %s)\n", who, prof.Path)
		fmt.Fprintf(out, "спавнить субагента: модель %s, определение %s-%s, рабочая директория %s, постановка docs/tasks/%s.md\n",
			v.Model, role, v.Effort, workdir, id)
		return 0, nil
	case "none":
		fmt.Fprintf(errw, "делегировать нечем: харнес %s объявил delegate.mode = \"none\" (профиль %s), ни субагента, ни команды у него нет; задачу исполняет тот, кто это умеет\n", who, prof.Path)
		return codeNothingToDelegate, nil
	case "cli":
	default:
		return 0, fmt.Errorf("профиль %s: [delegate] mode = %s, run такого режима не знает", prof.Path, quoteTOML(mode))
	}
	tmpl := del.arr("command")
	if len(tmpl) == 0 {
		return 0, fmt.Errorf("профиль %s: [delegate] mode = \"cli\", а command пуст, поднимать нечего", prof.Path)
	}
	prompt, err := agentPrompt(filepath.Dir(hc.L.Dir), role, v.Effort, id, workdir)
	if err != nil {
		return 0, err
	}
	argv := substituteCommand(tmpl, map[string]string{
		"model": v.Model, "effort": v.Effort, "workdir": workdir, "prompt": prompt,
	})
	refreshExecutorQuota(hc.L, who, timeNow())
	// Пары из env харнеса назначения: ими подпроцесс получает свой каталог
	// конфигурации (CLAUDE_CONFIG_DIR у второй подписки), а через него base URL и
	// токен. Имена печатаются, значения нет: в env кладут и токены, а вывод run
	// уезжает в логи и в контекст диспетчера.
	var pairs []envPair
	if hc.L != nil {
		if s := hc.L.Setup[who]; s != nil {
			pairs = s.Env
		}
	}
	tail := ""
	if len(pairs) > 0 {
		tail = ", окружение: " + strings.Join(envNames(pairs), ", ")
	}
	fmt.Fprintf(out, "делегирование: cli (харнес %s, профиль %s), подпроцесс %s в %s%s\n", who, prof.Path, argv[0], workdir, tail)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	// Окружение складывается поверх унаследованного, последнее значение
	// побеждает: сначала пары харнеса назначения, потом своё. Ограничитель
	// вложенности и имя харнеса идут последними не для порядка, а чтобы их нельзя
	// было перебить (имена с приставкой DEVKIT_ в env запрещены чтением машинного
	// слоя, и вторым рубежом стоит эта очерёдность). Детект по переменным родителя
	// в гнезде сессий неоднозначен по построению, поэтому харнес ставится явно.
	cmd.Env = os.Environ()
	for _, p := range pairs {
		cmd.Env = append(cmd.Env, p.Name+"="+p.Value)
	}
	cmd.Env = append(cmd.Env, harnessEnv+"="+who, runDepthEnv+"=1")
	cmd.Stdout, cmd.Stderr = out, errw
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		code := ee.ExitCode()
		if code < 0 {
			// Убитый сигналом подпроцесс кода выхода не имеет, а отдать наружу
			// минус единицу нельзя: она обернулась бы в 255 и читалась бы как
			// чужой код возврата.
			fmt.Fprintf(errw, "подпроцесс кончился сигналом (%v), наружу идёт код 1\n", ee)
			code = 1
		}
		return code, nil
	default:
		return 0, fmt.Errorf("подпроцесс %s не поднялся (%v); команду объявляет [delegate] command профиля %s", argv[0], err, prof.Path)
	}
}

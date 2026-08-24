package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/works"
)

// Доска берётся подпроцессом taskctl list --json, а не своим разбором
// markdown: правда о формате одна и живёт в утилите (LLD DK-112, «Откуда
// сервер берёт данные»). Честная ошибка «taskctl не нашёлся» полезнее
// выживания с собственным разбором, который молча устареет.

// taskctlBin подменяется тестами.
var taskctlBin = "taskctl"

// exeDir отдаёт каталог собственного бинаря; подменяется тестами, потому что
// os.Executable у go test показывает на тестовый бинарь во временной сборке.
var exeDir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// binPath ищет утилиту devkit сначала рядом с собственным бинарём, потом по
// PATH. Под launchd PATH системный (EnvironmentVariables в plist нет, бинарь
// dashboard назван полным путём), а утилиты devkit лежат одним каталогом:
// сосед по каталогу это и есть бинарь той же раскладки. PATH остаётся
// откатом для запуска из исходников и тестов. Пусто, если не нашёлся нигде.
func binPath(name string) string {
	if dir := exeDir(); dir != "" {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func taskctlPath() string { return binPath(taskctlBin) }

// procTimeout ограничивает подпроцессы сроком: подвисший taskctl или tmux не
// должен держать горутину запроса вечно, тем более после ухода клиента.
// Переменная, чтобы тест изображал зависание без настоящего ожидания.
var procTimeout = 30 * time.Second

// runProc гоняет подпроцесс со сроком; по срыву срока процесс снимается, а
// ошибка называет срок, а не пересказывает сигнал убийства.
func runProc(name string, args ...string) ([]byte, error) {
	return runProcIn("", name, args...)
}

// runProcIn это тот же запуск с текстом на входе. Текст черновика едет так, а
// не аргументом: аргументом он проходит через разбор флагов и через стража
// подкоманд taskctl, и мысль вида «-p» или «fix» там теряется целиком.
func runProcIn(stdin, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), procTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// Без WaitDelay Output ждёт трубу, а её после убийства процесса может
	// держать открытой его выживший потомок: срок обязан вернуть управление,
	// а не переложить вечное ожидание с процесса на трубу.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s не ответил за %s и снят по сроку", name, procTimeout)
	}
	return out, err
}

// commitDocs коммитит и пушит правку доски или файла задачи: доска это общий
// источник правды, и отставший remote оборачивается конфликтами (ядро правил
// доски). Провал возвращается словами, а не кодом ошибки: правка уже на
// месте, и утилита с витком видят её и без коммита.
func commitDocs(dir, msg string, paths ...string) string {
	// В add идут только те пути, что есть на диске: файл закрытой задачи уехал
	// в архив руками git mv, переименование уже лежит в индексе, а pathspec по
	// исчезнувшему пути add уронил бы. В pathspec коммита нужны оба конца
	// переезда, иначе в коммит попала бы его половина.
	var add []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			add = append(add, p)
		}
	}
	steps := [][]string{
		append([]string{"commit", "-m", msg, "--"}, paths...),
		{"push"},
	}
	if len(add) > 0 {
		steps = append([][]string{append([]string{"add", "--"}, add...)}, steps...)
	}
	for _, args := range steps {
		if _, err := runProc("git", append([]string{"-C", dir}, args...)...); err != nil {
			return fmt.Sprintf("запись на месте, но git %s не прошёл: %s", args[0], procErr(err))
		}
	}
	return ""
}

func taskctlMissing() string {
	if taskctlPath() == "" {
		return "taskctl не нашёлся ни рядом с бинарём дашборда, ни в PATH: доски не читаются, " +
			"поставить бинари: devkitctl update"
	}
	return ""
}

// boardJSON отдаёт доску проекта как есть, байтами ответа taskctl. Память на
// этот ответ живёт уровнем выше, в методе сервера (cache.go).
func boardJSON(dir string) (json.RawMessage, error) {
	bin := taskctlPath()
	if bin == "" {
		return nil, errors.New(taskctlMissing())
	}
	out, err := runProc(bin, "list", "--json", "-C", dir)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("taskctl list --json в %s: %s", dir, strings.TrimSpace(string(ee.Stderr)))
		}
		if m := taskctlMissing(); m != "" {
			return nil, errors.New(m)
		}
		return nil, fmt.Errorf("taskctl list --json в %s: %v", dir, err)
	}
	return json.RawMessage(bytes.TrimSpace(out)), nil
}

// boardView это минимум, который сервер сам вычитывает из ответа taskctl:
// префикс для привязки tmux-сессий и счётчики секций для списка проектов.
type boardView struct {
	Prefix   string `json:"prefix"`
	Sections []struct {
		Key  string            `json:"key"`
		Rows []json.RawMessage `json:"rows"`
	} `json:"sections"`
}

func parseBoardView(raw json.RawMessage) (boardView, error) {
	var v boardView
	err := json.Unmarshal(raw, &v)
	return v, err
}

// Work это живая работа проекта: tmux-сессия goal-*/task-*, цель из реестра
// ~/.devkit/goals (цикл, который ведёт другая сессия, без tmux-сессии) либо
// интерактивная сессия человека в окне, узнанная по свежему транскрипту
// (DK-263). Session и Note заполняет только третий источник: у интерактивной
// работы есть транскрипт, а задача у неё бывает и не узнана.
type Work struct {
	ID string `json:"id"`
	// Kind говорит, о чём работа, а Via чем она видна. Вид session это работа
	// с неузнанной задачей: чем она занята, сказать нечем.
	Kind string `json:"kind"` // goal | task | session
	Via  string `json:"via"`  // tmux | registry | session
	// Title это заголовок со строки доски: им работа и подписана на экране,
	// имя сессии goal-DK-112 о занятии агента не говорит ничего (DK-255).
	// Пусто у работы, чьей строки на доске нет.
	Title   string `json:"title,omitempty"`
	Session string `json:"session,omitempty"`
	Note    string `json:"note,omitempty"`
	// Sect это ключ секции доски (in-progress, check, backlog, blocked): им
	// подписан статус работы на экране «Агенты». Пусто у работы, чьей строки на
	// доске нет.
	Sect string `json:"sect,omitempty"`
	// Started это момент начала работы в unix-секундах: экран «Агенты» считает
	// по нему, сколько она идёт. Знает его только tmux, у которого сессия
	// заведена; запись реестра и транскрипт помечены последним касанием, а не
	// первым, и время работы из них не выводится. Ноль значит «начала не
	// видно», и строка тогда остаётся без времени, а не с нулём минут.
	Started int64 `json:"started,omitempty"`
	// Own говорит, дашбордова ли это работа: её tmux-сессию поднял он сам и
	// знает её имя. Не своя это цикл цели, заведённый в другом месте, и окно
	// человека: распоряжаться ими дашборд не может, и в разделе «Агенты» они
	// стоят своим табом (решение пользователя). Тот же признак объясняет
	// подсказкой, почему у строки нет кнопки остановки.
	Own bool `json:"own,omitempty"`
	// Model это модель работы, как её знает дашборд: по ней фильтруется список
	// раздела. Пусто у работы, чью модель взять неоткуда.
	Model string `json:"model,omitempty"`
	// Talk отличает разговор о задаче от работы над ней: чат живой и номер
	// задачи у него свой, но строку он не присваивает, и признак работы с него
	// не берётся (leadsTask). На экране «Агенты» такая строка стоит наравне с
	// остальными, ей нужны те же две дороги.
	Talk bool `json:"talk,omitempty"`
	// Live это состояние работы словом: busy (ход идёт), waiting (агент
	// спросил и ждёт человека), idle (сессия жива, а хода нет дольше рубежа),
	// dead (сессии не видно). Прежде экран красил зелёным всякую живую сессию,
	// и три десятка окон, молчавших часами, выглядели работающими: по экрану
	// нельзя было сказать, чем занята машина (замечание пользователя по
	// снимку). Пусто у работы, о состоянии которой сказать нечего.
	Live string `json:"live,omitempty"`
	// Moved это время последнего хода в unix-секундах: по нему экран говорит
	// давность словами. Ноль значит «времени не видно», и экран тогда молчит, а
	// не показывает эпоху, как показывал её реестр без поля времени.
	Moved int64 `json:"moved,omitempty"`
}

// Состояния работы. Слова машинные, человек их не читает: на экран они
// переводятся подписью строки.
const (
	workBusy = "busy"
	workWait = "waiting"
	workIdle = "idle"
	workDead = "dead"
)

// workIdleAfter это рубеж простоя: работа, чей последний ход старше него,
// работой не считается, как бы жива ни была её сессия. Порог назван тут один
// раз на весь дашборд: разъехавшись, кружок строки и слова подсказки мерили бы
// простой по-разному.
const workIdleAfter = 20 * time.Minute

// peerFresh отвечает, свежа ли запись реестра клиента. Слову «busy» верят
// только у свежей записи: клиент, упавший посреди хода, оставляет своё «busy» в
// реестре навсегда, и по нему семичасовой разговор выходил активным (замечание
// пользователя). Время в записи лежит в миллисекундах, нулевое значит «времени
// нет», и такой записи не верят вовсе.
func peerFresh(p peer, now time.Time) bool {
	if p.Updated <= 0 {
		return false
	}
	return now.Sub(time.Unix(p.Updated/1000, 0)) <= workIdleAfter
}

// livePeers раскладывает реестр живых сессий клиента по двум ключам: по id
// сессии и по имени tmux. Имя в записи стоит полным адресом пары
// («task-DK-499:@896.%896»), и ключом берётся его первое звено.
func (s *server) livePeers() (bySid, byTmux map[string]peer) {
	bySid = s.peers()
	byTmux = map[string]peer{}
	for _, p := range bySid {
		if name := strings.SplitN(p.Tmux, ":", 2)[0]; name != "" {
			byTmux[name] = p
		}
	}
	return bySid, byTmux
}

// workState называет состояние работы и время её последнего хода. Источников
// три, и они ранжированы: признак ожидания во входе разговора (его кладёт
// taskctl ask, и он старше всего), транскрипт (по нему считается сам ход, и
// только он честен у окон vscode, где состояния в реестре нет вовсе) и запись
// реестра клиента с её состоянием и временем касания.
func (s *server) workState(projPath, id, sid, tmux string, bySid, byTmux map[string]peer) (string, int64) {
	now := s.now()
	p, known := peer{}, false
	if sid != "" {
		p, known = bySid[sid]
	}
	if !known && tmux != "" {
		p, known = byTmux[tmux]
		if known && sid == "" {
			sid = p.SessionID
		}
	}
	// Время реестра приходит в миллисекундах, и нулевое значит «времени нет»:
	// показанное как есть, оно превращалось в 1970 год.
	moved := int64(0)
	if known && p.Updated > 0 {
		moved = p.Updated / 1000
	}
	busy := false
	if sid != "" {
		if info, ok := findSession(s.transcriptRoots(), projPath, sid); ok {
			if at := info.mod.Unix(); at > moved {
				moved = at
			}
			busy = s.sessionBusy(info.path, now)
		}
	}
	if id != "" {
		if _, waiting := askWaiting(projPath, id, now); waiting {
			return workWait, moved
		}
	}
	switch {
	case busy:
		return workBusy, moved
	case known && p.Status == "busy" && peerFresh(p, now):
		return workBusy, moved
	case known && p.Status == "waiting":
		return workWait, moved
	case known || tmux != "":
		// Сессия на месте, а хода в ней нет: работой это не считается, даже
		// если реестр молчит о состоянии вовсе.
		return workIdle, moved
	}
	return workDead, moved
}

// liveWorks собирает работы проекта. tmux-сессии на машине общие, к проекту
// они привязываются префиксом ID с его доски; разбор имён и записи реестра
// целей живут в общем каркасе internal/works: тем же признаком занятости
// пользуется планировщик слота taskctl (LLD DK-400, решение 3), и вторая
// копия разбора у экрана расползлась бы с утилитой на первой правке.
// Третьим идут интерактивные сессии: окно агента у человека не заводит ни
// tmux-сессии, ни записи в реестре, и без них половина работы на машине была
// бы невидима.
func (s *server) liveWorks(projectPath, prefix string, board json.RawMessage) []Work {
	// Пустой список, а не null: клиент и smoke-сценарий различают «работ нет»
	// и «поля нет».
	list := []Work{}
	seen := map[string]bool{}
	busy := map[string]bool{}
	// Заголовки берутся с доски разом: работа подписывается им, а не именем
	// сессии goal-DK-112, которое о занятии агента не говорит ничего (DK-255).
	// Строки на доске может и не быть, и тогда работа остаётся при своём ID.
	rows, _ := parseBoardRows(board)
	// Сессии, где клиент жив, а хода нет: строке они работы не дают.
	talk := s.tmuxTalk(projectPath)
	// Реестр живых сессий читается разом на все работы: по нему считается их
	// состояние, и тридцать походов в каталог реестра тут ни к чему.
	bySid, byTmux := s.livePeers()
	// Обратная дорога от имени tmux к сессии: состояние работы считается по её
	// транскрипту, а у работы, взятой из списка tmux, id сессии своего нет.
	sidOf := map[string]string{}
	for sid, rec := range s.binds() {
		if name := strings.SplitN(rec.Tmux, ":", 2)[0]; name != "" {
			sidOf[name] = sid
		}
	}
	// Сессии спрашиваются со временем создания (tmuxList): по нему экран
	// «Агенты» говорит, сколько работа идёт.
	for _, sess := range tmuxList() {
		id, kind := works.SessionTask(sess.Name, prefix)
		if id == "" {
			continue
		}
		live, moved := s.workState(projectPath, id, sidOf[sess.Name], sess.Name, bySid, byTmux)
		list = append(list, Work{ID: id, Kind: kind, Title: rows[id].Title,
			Sect: rows[id].Sect, Via: "tmux", Started: sess.Created,
			// Конвейерная сессия это сессия дашборда: он её и поднял, её имя
			// собрано по его же образцу.
			Own: true, Model: s.chatModel("", sess.Name), Talk: talk[sess.Name],
			Live: live, Moved: moved})
		seen[kind+"-"+id] = true
		busy[id] = true
	}
	for _, goal := range works.RegistryGoals(s.cfg.Home, projectPath) {
		if seen["goal-"+goal] {
			continue
		}
		// Цикл цели из реестра поднят мимо дашборда, его сессии он не видит:
		// состояние тут называется мёртвым нарочно, зелёным такая строка гореть
		// не должна.
		list = append(list, Work{ID: goal, Kind: "goal", Title: rows[goal].Title,
			Sect: rows[goal].Sect, Via: "registry", Live: workDead})
		busy[goal] = true
	}
	return append(list, s.sessionWorks(projectPath, prefix, rows, busy)...)
}

// tmuxTalk называет tmux-сессии, где клиент жив, а хода в них нет. Груминг
// черновика идёт живым чатом, а не headless-ходом, и его tmux-сессия переживает
// конец разбора: клиент остаётся стоять на приглашении и ждать человека. Строка
// с таким соседом показывала один «Стоп» и запустить себя не давала, хотя
// разбор кончился час назад (замечание пользователя). Работа это ход агента, а
// не живой клиент: досчитавшая сессия остаётся разговором, её видно разделом
// «Агенты» и списком чатов, а строка возвращается к своим кнопкам.
//
// Простой признаётся только доказанным: запись реестра клиента говорит idle, и
// транскрипт это подтверждает (тот же двойной признак, каким занятость чата
// меряет handleChatStatus). Сессия, о которой в реестре нет записи, остаётся
// работой, как и раньше: неизвестность это не повод снимать «Стоп» с идущего
// конвейера.
func (s *server) tmuxTalk(projPath string) map[string]bool {
	talk := map[string]bool{}
	for _, p := range s.peers() {
		// В реестре стоит полный адрес пары («task-DK-499:@896.%896»), а имя
		// сессии это его первое звено.
		name := strings.SplitN(p.Tmux, ":", 2)[0]
		if name == "" || p.Status != "idle" {
			continue
		}
		if info, ok := findSession(s.transcriptRoots(), projPath, p.SessionID); ok &&
			s.sessionBusy(info.path, s.now()) {
			continue
		}
		talk[name] = true
	}
	return talk
}

// tmuxMissingCheck называет ненайденный tmux: без него живые работы это
// молча пустой список, неотличимый от «агенты не работают», а по LLD
// («Молчание различимо») это обязаны быть разные ответы. tmux не утилита
// devkit и соседом по каталогу не лежит, PATH launchd-агенту дописывает
// devkitctl doctor --fix.
func tmuxMissingCheck() string {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "tmux не нашёлся в PATH: живые работы tmux-сессий не видны, поставить tmux " +
			"или дописать PATH launchd-агенту (devkitctl doctor --fix)"
	}
	return ""
}

// tmuxSessions отдаёт одни имена: занятость задачи и поиск сессии по имени
// временем не интересуются. Ненулевой код tmux ls это штатное «сессий нет»:
// без единой сессии tmux не держит сервера и ls отвечает ошибкой, поломкой это
// не считается; ненайденный бинарь называет tmuxMissingCheck на уровне ответа,
// здесь оба случая дают пустой список.
func tmuxSessions() []string {
	out, err := runProc("tmux", "ls", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func globSorted(pattern string) []string {
	paths, _ := filepath.Glob(pattern)
	return paths
}

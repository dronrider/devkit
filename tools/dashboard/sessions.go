package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Живой статус агента, сторона транскрипта: сессии проекта из каталогов
// журналов (transcriptRoots) и лента реплик одной сессии с пагинацией назад и живым
// дострением через SSE. Транскрипт весит мегабайты и пишется чужим процессом,
// поэтому режет его сервер, а клиент получает последние реплики готовым
// JSON (LLD DK-112, «Граница сервер-клиент»). Разбор реплик держится общим
// куском: переписка DK-220 переиспользует его как есть.

// claudeDirName кодирует путь проекта в имя каталога транскриптов, как это
// делает Claude Code: всё, что не буква, не цифра и не дефис, становится
// дефисом (/Users/x/dev -> -Users-x-dev), точки и подчёркивания тоже.
func claudeDirName(projPath string) string {
	var b strings.Builder
	for _, r := range projPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// transcriptRoots перечисляет корни, под которыми лежат каталоги транскриптов:
// свой ~/.claude/projects плюс projects каждой подписки со своим хозяйством.
// Второй корень появился с выбором подписки на запуске (DK-326): клиент второй
// подписки поднимается с чужим каталогом конфигурации и журнал разговора пишет
// туда, поэтому headless-сессия, поднятая с доски, из ~/.claude не видна вовсе
// (DK-362). Повторы отсеиваются: подписка вправе назвать своим каталогом тот
// же ~/.claude, и тогда обход прошёл бы по нему дважды.
func transcriptRoots(home string, harnessHomes []string) []string {
	roots := []string{filepath.Join(home, ".claude", "projects")}
	seen := map[string]bool{roots[0]: true}
	for _, h := range harnessHomes {
		if h == "" {
			continue
		}
		root := filepath.Join(h, "projects")
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}

// transcriptHomes отдаёт каталоги хозяйства подписок из раскладки машины.
func (v HarnessView) transcriptHomes() []string {
	var out []string
	for _, h := range v.Harnesses {
		if h.Home != "" {
			out = append(out, h.Home)
		}
	}
	return out
}

// transcriptRoots сервера это те же корни на раскладке подписок из памяти
// процесса: спрашивается она на каждой сборке экрана, и подпроцесс за ней
// ходит не чаще срока harnessTTL.
func (s *server) transcriptRoots() []string {
	return transcriptRoots(s.cfg.Home, s.harnesses().transcriptHomes())
}

// transcriptDir это каталог транскриптов вместе с корнем, из которого он
// пришёл: по корню разговор называет свою подписку, и вывести его из пути
// каталога задним числом нельзя, корни бывают вложенными.
type transcriptDir struct {
	path string
	root string
}

// sessionDirs собирает каталоги транскриптов проекта: в каждом корне сам
// каталог по пути с дефисами плюс те, чьё имя продолжает его дефисом, так в
// список попадают боковые деревья задач (../<проект>-<id>) и копия окна (LLD).
func sessionDirs(roots []string, projPath string) []transcriptDir {
	name := claudeDirName(projPath)
	var dirs []transcriptDir
	for _, base := range roots {
		dirs = append(dirs, transcriptDir{path: filepath.Join(base, name), root: base})
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), name+"-") {
				dirs = append(dirs, transcriptDir{path: filepath.Join(base, e.Name()), root: base})
			}
		}
	}
	return dirs
}

// harnessRoots сводит корни журналов к именам подписок: транскрипт лежит под
// хозяйством той подписки, чьим клиентом он написан, и список разговоров
// называет её словом. Своё ~/.claude без подписки остаётся безымянным, и это
// честнее выдуманного имени.
// harnessOfRoot называет подписку по корню транскриптов. Своего каталога у
// подписки по умолчанию нет, она пишет в ~/.claude, и в карте корней её нет
// вовсе: тут этот корень и получает её имя.
func harnessOfRoot(view HarnessView, home, root string) string {
	if name := harnessRoots(view)[root]; name != "" {
		return name
	}
	if root != filepath.Join(home, ".claude", "projects") {
		return ""
	}
	for _, h := range view.Harnesses {
		if h.Home == "" {
			return h.Name
		}
	}
	return ""
}

func harnessRoots(view HarnessView) map[string]string {
	out := map[string]string{}
	for _, h := range view.Harnesses {
		if h.Home == "" {
			continue
		}
		out[filepath.Join(h.Home, "projects")] = h.Name
	}
	return out
}

// harnessRootsOwn это та же карта домов, дополненная домами, где журнал ведёт
// подписка по умолчанию: настоящий дом человека и дом самого дашборда, если он
// свой (второй экземпляр POC живёт под своим HOME). Вывод тут не догадка:
// вторая подписка всегда уводит журналы в своё хозяйство каталогом
// конфигурации, и транскрипт, лежащий вне её дома, ей принадлежать не может.
// Без этого подписка оставалась пустой у всех работ, кроме второй, то есть
// ровно там, где её чаще всего и спрашивают (замечание пользователя про строки
// таба сессий).
func (s *server) harnessRootsOwn() map[string]string {
	view := s.harnesses()
	out := harnessRoots(view)
	def := ""
	for _, h := range view.Harnesses {
		if h.Default {
			def = h.Name
		}
	}
	if def == "" {
		return out
	}
	for _, home := range []string{realHome(), s.cfg.Home} {
		if home == "" {
			continue
		}
		root := filepath.Join(home, ".claude", "projects")
		if _, taken := out[root]; !taken {
			out[root] = def
		}
	}
	return out
}

// sessionInfo это строка списка сессий: id, время последней записи, ветка и
// первая реплика человека, по ней сессию узнают в списке. Tree это боковое
// дерево, в котором шёл разговор (у главного дерева пусто): двух разговоров
// одной задачи ветка не разводит, груминг и исполнение идут по одной, а
// деревья у них разные (DK-290). Task с подписью TaskNote говорят, чью работу
// сессия ведёт и чем она узнана; нераспознанная сессия остаётся в списке с
// подписью, а в ленту задачи не идёт (DK-252).
type sessionInfo struct {
	ID       string `json:"id"`
	Mtime    string `json:"mtime"`
	Branch   string `json:"branch,omitempty"`
	Tree     string `json:"tree,omitempty"`
	First    string `json:"first,omitempty"`
	Task     string `json:"task,omitempty"`
	TaskNote string `json:"taskNote,omitempty"`
	// Bound это разряд привязки для экрана: lead значит «ведёт» по записи
	// реестра, about значит «говорит о» по угадыванию. Пусто у сессии без
	// задачи. Слова подписи для человека, разряд для кода: разбирать подпись
	// на экране значило бы держать её словарь в двух местах.
	Bound string `json:"bound,omitempty"`
	// Reply называет ручку для реплики этого разговора, а ReplyNote причину
	// словами: session это живой разговор, task это кончившийся разговор с
	// живой задачей (реплику берёт ручка задачи), пусто это кончившийся
	// разговор без задачи, у которого отвечать некому. Меру считает chatReply
	// (LLD DK-430, решение 2), и выбирает ручку она, а не экран: ошибка в
	// адресате стоит потерянной реплики.
	Reply     string `json:"reply,omitempty"`
	ReplyNote string `json:"replyNote,omitempty"`
	// Harness называет подписку, чьим хозяйством написан транскрипт: у второй
	// подписки свой каталог журналов (DK-362), и в списке разговоров задачи она
	// обязана называть себя. Пусто у своего ~/.claude и у подписки, которую
	// раскладка машины не назвала: выдумывать имя тут нечем.
	Harness string `json:"harness,omitempty"`
	// Live это свежесть транскрипта: разговор, писавший недавно, идёт прямо
	// сейчас. Мера мягкая и годится только на слово состояния в списке;
	// кончившийся разговор считает жёстко chatReply, и путать их нельзя.
	Live   bool `json:"live,omitempty"`
	path   string
	suffix string
	stamp  string
	root   string
	mod    time.Time
}

// sessionFiles обходит каталоги транскриптов; сортировка свежие сверху, при
// равном времени по id, чтобы порядок был устойчив. Вместе с файлом берётся
// хвост имени каталога (боковое дерево задачи называет им задачу) и отпечаток
// файла для памяти процесса на шапку.
func sessionFiles(roots []string, projPath string) []sessionInfo {
	var out []sessionInfo
	base := claudeDirName(projPath)
	for _, dir := range sessionDirs(roots, projPath) {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(filepath.Base(dir.path), base), "-")
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if !ok || e.IsDir() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, sessionInfo{
				ID: name, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
				path:   filepath.Join(dir.path, e.Name()),
				suffix: suffix,
				root:   dir.root,
				stamp:  fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()),
				mod:    fi.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].mod.Equal(out[j].mod) {
			return out[i].mod.After(out[j].mod)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// metaScanLimit ограничивает чтение шапки транскрипта: ветка, первая реплика
// и названная ею задача лежат в первых записях, тянуть мегабайты ради них
// незачем.
const metaScanLimit = 256 * 1024

// sessionHead это всё, что читается из шапки транскрипта: ветка, первая
// реплика человека для списка и ID задачи, названный этой репликой.
type sessionHead struct {
	Branch string
	First  string
	Named  string
	// Born это метка первой записи транскрипта, то есть когда разговор
	// заведён. Спрашивают её там, где содержательных реплик не нашлось вовсе:
	// давность такой сессии считается от её начала, а время правки файла в
	// этот ответ не годится (его двигает всякое касание снаружи).
	Born string
	// Said это метка последней содержательной реплики транскрипта: слова
	// человека или агента, те самые, что видны в ленте. Время правки файла на
	// этот вопрос не отвечает: транскрипт трогает всякая служебщина (постановка
	// реплики в очередь, отметки харнеса, служебные вставки, которые лента и
	// так прячет), и разговор, где месяц никто не писал, выходил в списке
	// первым (замечание пользователя). Пусто, когда в прочитанном хвосте
	// содержательных реплик не нашлось.
	Said string
	// Bye говорит, что последним словом агента в разговоре была служебная
	// строка про истёкший логин: клиент не работает и ждёт /login. Признак
	// живёт при шапке, а не при ленте, потому что спрашивают его список
	// разговоров и панель, а обе читают шапку и так. Гаснет он сам: следующий
	// настоящий ответ агента заменяет собой служебную строку.
	Bye bool
	// Model это модель, которой сессия работает на самом деле: её пишет харнес
	// в каждую запись ответа (message.model). Выбор, сохранённый дашбордом,
	// говорит лишь о том, чем поднимать сессию в следующий раз, а чем она
	// работает сейчас, знает только транскрипт (замечание 7 седьмого круга POC).
	Model string
	// Summary это заголовок разговора, который пишет сам харнес записью
	// {"type":"summary"} в начале транскрипта. Им подписывает диалоги
	// расширение Claude Code для vscode и им же зовётся разговор в списке
	// `claude --resume`, поэтому в списке диалогов он старше первой реплики:
	// у долгого разговора первая реплика давно не про то, чем он кончился.
	// Записи этой нет у большинства транскриптов, и тогда заголовком остаётся
	// обрезанная первая реплика.
	Summary string
}

// readSessionHead вычитывает шапку транскрипта; служебные вставки в угловых
// скобках (<ide_opened_file> и родня) репликой не считаются. Второй ответ
// говорит, дочитана ли голова до предела: дальше файл только дописывается, и
// такая голова больше не меняется, на этом стоит память процесса.
// tailParsed читает хвост транскрипта и разбирает его тем же разбором, каким
// собрана лента: читателей у хвоста двое, время последней реплики и разбор
// работы по задаче, и держать два чтения ради одного файла незачем.
func tailParsed(path string) []reply {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	from := int64(0)
	if fi.Size() > modelTailLimit {
		from = fi.Size() - modelTailLimit
	}
	if _, err := f.Seek(from, 0); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return parseReplies(data, 0)
}

// tailReplies отдаёт последние n записей хвоста: работа по задаче видна
// последними ходами, а глубже начинается прошлое сессии.
func tailReplies(path string, n int) []reply {
	list := tailParsed(path)
	if n > 0 && len(list) > n {
		return list[len(list)-n:]
	}
	return list
}

// lastSaid отвечает, когда в разговоре последний раз сказали что-то по делу.
// Разбор тут общий с лентой (parseReplies): содержательным считается ровно то,
// что лента показывает пузырём, слова человека и ответ агента. Ходы
// инструментов, размышления и служебные строки время не двигают, иначе список
// снова стал бы сортироваться по касаниям файла.
//
// Читается хвост, а не файл целиком: транскрипт долгого разговора это мегабайты,
// а последняя реплика лежит в самом конце.
func lastSaid(path string) string {
	said, _ := tailFacts(path)
	return said
}

// tailFacts читает хвост один раз и отвечает двумя фактами: когда в разговоре
// последний раз сказали что-то по делу и не отказался ли клиент работать без
// логина. Разбор хвоста стоит чтения файла, и второго ради одного признака тут
// не заводится.
//
// Разлогин меряется последним ответом агента, а не последней репликой вообще:
// человек вправе написать после отказа сколько угодно, и разговор от этого не
// становится рабочим. Настоящий ответ агента признак снимает.
func tailFacts(path string) (string, bool) {
	list := tailParsed(path)
	said, bye, seen := "", false, false
	for i := len(list) - 1; i >= 0; i-- {
		r := list[i]
		if !saidReply(r) {
			continue
		}
		if said == "" {
			said = r.Time
		}
		if !seen && r.Role == "assistant" {
			seen, bye = true, loginGone(r.Text)
		}
		if said != "" && seen {
			break
		}
	}
	return said, bye
}

// saidReply отделяет сказанное от машинного: пузырём в ленте стоят реплика
// человека и ответ агента, и только они говорят о том, что разговор жив.
func saidReply(r reply) bool {
	if r.Role != "user" && r.Role != "assistant" {
		return false
	}
	return strings.TrimSpace(r.Text) != ""
}

// saidUnix переводит метку транскрипта в unix-секунды. Второй ответ говорит,
// что метки нет вовсе: ноль тут значит «времени не видно», и выдавать его за
// начало эпохи экран не должен.
func saidUnix(stamp string) (int64, bool) {
	if stamp == "" {
		return 0, false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return 0, false
	}
	return at.Unix(), true
}

func readSessionHead(path string) (sessionHead, bool) {
	var head sessionHead
	f, err := os.Open(path)
	if err != nil {
		return head, false
	}
	defer f.Close()
	buf := make([]byte, metaScanLimit)
	// Чтение до заполнения буфера, а не одним Read: короткий ответ ядра выдал
	// бы недочитанную голову за дочитанную, и память процесса поверила бы ей.
	n, err := io.ReadFull(f, buf)
	full := err == nil
	for _, ln := range strings.Split(string(buf[:n]), "\n") {
		var rec struct {
			Type      string `json:"type"`
			GitBranch string `json:"gitBranch"`
			Summary   string `json:"summary"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if head.Born == "" && rec.Timestamp != "" {
			head.Born = rec.Timestamp
		}
		if head.Summary == "" && rec.Type == "summary" {
			head.Summary = firstLine(rec.Summary)
		}
		if head.Branch == "" {
			head.Branch = rec.GitBranch
		}
		if head.First == "" && rec.Type == "user" {
			for _, text := range contentTexts(rec.Message.Content) {
				// Служебные обёртки панели (снимок экрана, выделение) не слова
				// человека: они срезаются до проверки, и First берётся из того,
				// что он написал сам. Реплика, начатая снимком, оставляла шапку
				// пустой, и чат жил с фолбэком «чат <id8>» до haiku (живой
				// случай, сессия d055dcf5 с осмысленной первой репликой).
				text = cutFirstWraps(text)
				if !strings.HasPrefix(text, "<") {
					head.First = firstLine(text)
					// ID ищется по всей реплике, а не по обрезанной для списка
					// строке: заказ работы бывает длиннее ста шестидесяти знаков.
					head.Named = taskIDInText(text)
					break
				}
			}
		}
		if head.Branch != "" && head.First != "" {
			break
		}
	}
	head.Said, head.Bye = tailFacts(path)
	return head, full
}

// headTTL это потолок доверия к запомненной шапке. Отпечаток файла ловит
// правку сам, а дочитанной голове верят и после дописывания, но переписанный
// с нуля файл не поймать ни тем, ни другим, поэтому доверие кончается по
// сроку.
const headTTL = 5 * time.Minute

// headEntry это запомненная шапка одного транскрипта.
type headEntry struct {
	head  sessionHead
	stamp string
	full  bool
	born  time.Time
}

// sessionHeadCached отдаёт шапку транскрипта, по возможности из памяти
// процесса (образец кэша DK-242). Экран агента ходит за сессиями на каждом
// открытии, а сессий у проекта десятки: без памяти каждый запрос перечитывал
// бы по четверти мегабайта на сессию.
func (s *server) sessionHeadCached(path, stamp string) sessionHead {
	now := s.now()
	s.mu.Lock()
	e, hit := s.heads[path]
	s.mu.Unlock()
	if hit && now.Sub(e.born) < headTTL && (e.full || e.stamp == stamp) {
		return e.head
	}
	head, full := readSessionHead(path)
	s.mu.Lock()
	s.heads[path] = headEntry{head: head, stamp: stamp, full: full, born: now}
	s.mu.Unlock()
	return head
}

// taskIDTextRe ловит ID задачи в тексте первой реплики. Префикс только
// прописными: так «Выполни цель DK-112» отличается от «top-10» и прочих слов
// с числом через дефис.
var taskIDTextRe = regexp.MustCompile(`\b([A-Z]{2,10})-(\d{1,6})\b`)

// taskIDNameRe узнаёт ID в имени: ветка dk-252, хвост каталога бокового
// дерева dk-252, имя с приставкой вроде dk-252-fix. Регистр тут любой, имена
// пишутся строчными.
var taskIDNameRe = regexp.MustCompile(`^([A-Za-z]{2,10})-(\d{1,6})(?:-|$)`)

func taskIDInText(text string) string {
	return upperID(taskIDTextRe.FindStringSubmatch(text))
}

func taskIDInName(name string) string {
	return upperID(taskIDNameRe.FindStringSubmatch(name))
}

func upperID(m []string) string {
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2]
}

// unknownTaskNote подписывает разговор без задачи. Третьего состояния у чата
// нет: он либо о задаче, либо свободный, и отчёт о том, чего дашборд про него
// не узнал, человеку не говорил ничего.
const unknownTaskNote = "свободный чат"

// Задачу сессии называет реестр чатов (registry.go): запись журнала старше
// угадывания по транскрипту, а сами три способа угадывания («Выполни цель
// DK-112» в первой реплике, хвост каталога бокового дерева, имя ветки) остались
// запасным разрядом и живут там же, в sessionBinds.task.

// sessionLiveTTL это порог живости интерактивной сессии. Транскрипт
// дописывается каждым ходом, но ход бывает долгим: одиночный прогон в
// foreground харнес держит до десяти минут, и вокруг него идут ещё
// размышления и правка, поэтому паузу в записи короче этого срока считать
// концом работы нельзя. Пятнадцать минут держали бы закрытое окно живым
// четверть часа, двенадцать оставляют запас над самым долгим ходом и убирают
// доделанную работу с глаз тем же ходом, что человек уходит от окна.
const sessionLiveTTL = 12 * time.Minute

// foreignTaskNote подписывает сессию, чья задача узнана, но принадлежит чужой
// доске: ходить по ней на экран задачи некуда, а сама работа идёт и в списке
// остаётся.
const foreignTaskNote = "задача не с доски проекта"

// aboutTaskNote подписывает разговор о задаче: работой он не считается, но
// сказать, о чём он, надо. Молчаливое «интерактивная сессия» тут читалось бы
// как окно ни о чём.
func aboutTaskNote(task, note string) string {
	return fmt.Sprintf("чат о %s (%s)", task, note)
}

// groomOrderPrefix это начало заказа headless-сессии, поднимаемой самим
// дашбордом (groomPrompt в drafts.go). Транскрипт такой сессии узнаётся им:
// первая реплика там не разговор человека, а заказ, и жить этим транскриптом
// работа не может. Кто сказал заказ, транскрипт не отличает, поэтому своё
// интерактивное окно с теми же словами в чате карточку тоже теряет; жертва
// названа в разборе DK-358 и README.
const groomOrderPrefix = "Проведи груминг "

// sessionWorks собирает работы из транскриптов: интерактивное окно агента не
// заводит ни tmux-сессии, ни записи в реестре, и единственный его след это
// свежий транскрипт. Доска приходит уже разобранной: строка ищется на каждую
// сессию, а разбирать ответ taskctl заново на каждой из них незачем. Занятые задачи (busy) сюда не идут: headless-сессия
// конвейера тоже пишет транскрипт, и её работа уже собрана из tmux, а вторая
// карточка о той же задаче читалась бы как два агента вместо одного.
func (s *server) sessionWorks(projPath, prefix string, rows map[string]boardRow, busy map[string]bool) []Work {
	works := []Work{}
	// Подписка работы узнаётся по дому её транскрипта, тем же способом, каким
	// её узнаёт список чатов: своего поля у неё нет ни в реестре, ни в имени
	// tmux-сессии.
	roots := s.harnessRootsOwn()
	cutoff := s.now().Add(-sessionLiveTTL)
	binds := s.binds()
	bySid, byTmux := s.livePeers()
	for _, f := range sessionFiles(s.transcriptRoots(), projPath) {
		// Список идёт свежими сверху, дальше первого протухшего смотреть нечего.
		if f.mod.Before(cutoff) {
			break
		}
		head := s.sessionHeadCached(f.path, f.stamp)
		// Транскрипт с заказом дашборда в первой реплике жив, пока жива его
		// tmux-сессия: claude -p умирает вместе с ней, а транскрипт остаётся
		// свежим, и без этой проверки законченный разбор висел бы работой до
		// порога протухания (DK-358). Живость здесь не сверяется: живую
		// сессию уже забрал список tmux меткой busy, до сюда она не доходит.
		if strings.HasPrefix(head.First, groomOrderPrefix) {
			continue
		}
		// Служебная сессия суммаризации работой не является по той же причине,
		// по которой её нет в списке чатов: её завёл дашборд ради заголовка.
		if titleSession(head.First) {
			continue
		}
		task, note, bound := bindTask(binds, f.ID, f.suffix, head)
		if task != "" && (prefix == "" || !strings.HasPrefix(task, prefix+"-")) {
			task, note = "", foreignTaskNote
		} else if bound == boundAbout {
			// Разговор про задачу это не работа над ней: карточка живой работы
			// стоит на записи реестра, а угаданная по первой реплике сессия
			// конкурировала бы с настоящим исполнителем (DK-360). Чужая доска
			// разбирается раньше: там о задаче сказать нечего вовсе, ни строки,
			// ни экрана.
			task, note = "", aboutTaskNote(task, note)
		}
		// Разговорный чат задачи остаётся её работой на экране «Агенты»: сессия
		// живая, разговор идёт, и номер задачи у него свой. Строку он при этом
		// не присваивает, и признак разводит эти два случая: без него чат по
		// чужой задаче возвращал на неё кнопки конвейера (жалоба на DK-460).
		talk := task != "" && !leadsTask(binds[f.ID].Tmux, f.suffix, note)
		kind, title, sect := "session", "", ""
		if task != "" {
			if busy[task] {
				continue
			}
			busy[task] = true
			note = ""
			// Цель это цель и в интерактивном окне: переписка и журнал цикла
			// открываются только у неё, и вид работы берётся со строки доски,
			// а не из того, что окно вообще живо.
			kind = "task"
			if row, ok := rows[task]; !ok {
				kind = "session"
			} else {
				title, sect = row.Title, row.Sect
				if isGoalTitle(row.Title) {
					kind = "goal"
				}
			}
			if title == "" {
				// Строки на доске нет (задача закрыта и уехала в архив):
				// работа подписывается заголовком своего разговора, а не
				// голым номером (workTitle в board.go).
				title, _ = s.titleFor(f.ID, head.Summary, head.First, false)
			}
		}
		if task == "" {
			// Разговор без задачи подписывается своим заголовком, а не
			// отчётом о том, чего дашборд про него не узнал: прежняя подпись
			// про нераспознанную задачу не говорила ни о чём. Лестница
			// заголовка тут та же, что у списка чатов (titleFor): своего
			// разбора раздел «Агенты» не заводит, иначе один и тот же чат
			// назывался бы на соседних экранах по-разному (замечание 1
			// восьмого круга POC).
			if said, _ := s.titleFor(f.ID, head.Summary, head.First, false); said != "" {
				note = said
			}
		}
		// Своя работа это та, чью tmux-сессию поднял дашборд: её имя лежит в
		// записи реестра. У окна человека имени нет вовсе.
		tmux := binds[f.ID].Tmux
		name := strings.SplitN(tmux, ":", 2)[0]
		live, moved, silent := s.workState(projPath, task, f.ID, name, bySid, byTmux)
		works = append(works, Work{ID: task, Kind: kind, Title: title, Sect: sect,
			Via: "session", Session: f.ID, Note: note, Talk: talk,
			Own: name != "", Tmux: name, Model: s.chatModel(f.ID, tmux),
			Harness: roots[f.root],
			Live:    live, Moved: moved, Silent: silent})
	}
	return works
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return truncate(line, 160)
}

func truncate(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n]) + "..."
}

// contentTexts отдаёт текстовые куски содержимого реплики: строку как есть
// либо text-блоки из списка.
func contentTexts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			out = append(out, b.Text)
		}
	}
	return out
}

// reply это одна строка ленты: реплика человека или агента, свёрнутый вызов
// инструмента либо пометка о размышлениях. Вызовы инструментов сворачиваются
// в одну строку, тянуть их содержимое на телефон незачем (LLD); размышления
// сворачиваются в пометку без текста.
type reply struct {
	Seq  int    `json:"seq"`
	Role string `json:"role"` // user | assistant | thinking | tool | toolout
	Time string `json:"time,omitempty"`
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	Note string `json:"note,omitempty"`
	// Who это автор реплики словом, когда он не тот, о ком говорит роль:
	// каналом живых сессий в ленту приходят слова другого агента, а роль у
	// такой записи всё равно user, и панель подписывала их «вы».
	Who string `json:"who,omitempty"`
	// Sel это выделенный человеком кусок постановки, приложенный к реплике
	// контекстом, а SelFile называет файл, откуда он взят. В ленте это
	// свёрнутый блок при пузыре, а агенту оно уезжает префиксом самой реплики:
	// протокола сложнее префикса тут не нужно, это тот же приём, каким Claude
	// Code носит открытый файл (замечание 3 девятого круга POC).
	Sel     string `json:"sel,omitempty"`
	SelFile string `json:"selFile,omitempty"`
	// Pick это места экрана, выбранные пикером, а PickScreen называет экран, с
	// которого их взяли. В ленте это свёрнутый блок при пузыре, а агенту оно
	// уезжает префиксом реплики, той же дорогой, что и выделение.
	Pick       string `json:"pick,omitempty"`
	PickScreen string `json:"pickScreen,omitempty"`
	// Shot это путь картинки, приложенной к реплике: агент читает её сам, а
	// лента показывает миниатюру.
	Shot string `json:"shot,omitempty"`
	// Human помечает ход, которым агент обратился к человеку: отправка в панель
	// каналом сессий это реплика разговора, а не служебный ход инструмента.
	Human bool `json:"human,omitempty"`
	// Quote это кусок реплики человека, на которую отвечает агент, а QuoteKey
	// её ключ в ленте. В пузыре ответа она стоит узкой строкой сверху, и по ней
	// же в ленту переходят, как это принято в мессенджерах. QuoteMany говорит,
	// что реплик в пачке было несколько и цитируется последняя.
	Quote     string `json:"quote,omitempty"`
	QuoteKey  string `json:"quoteKey,omitempty"`
	QuoteMany bool   `json:"quoteMany,omitempty"`
	// Sub подписывает запись бокового журнала субагента: работа ушла ему, и
	// весь бегущий лог пишется туда, а не в транскрипт сессии.
	Sub string `json:"sub,omitempty"`
	// Mark это машинная пометка события ленты: по ней кружок записи красится,
	// не разбирая слов заголовка. Пока такая пометка одна, agent: весть о том,
	// что фоновый агент закончил работу. Слово это половина пары с вызовом
	// Task, вторую половину лента узнаёт по имени инструмента.
	Mark string `json:"mark,omitempty"`
	// About это пояснение хода словами, как его написал сам агент (поле
	// description у вызова). Лента ставит его заголовком записи, а команда
	// остаётся рядом: без пояснения строка Bash говорила только «что
	// запущено», но не «зачем».
	About string `json:"about,omitempty"`
	// Report помечает финальный ответ субагента: последнюю запись его журнала
	// с текстом. Наружу он не едет, это опора сшивки: тем же событием приходит
	// весть харнеса «фоновый агент завершил работу», и в ленте они сводятся в
	// один свёрнутый блок.
	Report bool `json:"-"`
	// Key это устойчивый ключ записи: «источник:номер в своём файле». Номер в
	// ленте (Seq) считается местом в слитой ленте и от заезда к заезду плывёт:
	// боковой журнал растёт, у сессии заводится новый субагент, и запись,
	// которая была тысячной, становится тысяча первой. Пагинация назад по
	// такому номеру давала дубли и дыры, поэтому «раньше» режется по этому
	// ключу, а не по месту (замечание пользователя про подгрузку истории).
	Key string `json:"key,omitempty"`
	// ToolID это идентификатор вызова инструмента, по нему боковой журнал
	// субагента сводится со своим вызовом Task. Наружу не едет: он нужен
	// только сшивке на сервере.
	ToolID string `json:"-"`
	// Spent это длительность размышлений в миллисекундах, посчитанная по
	// меткам времени соседних записей транскрипта. Модель отдаёт размышления
	// запечатанными чаще, чем текстом, и тогда сказать про них можно только
	// это: сколько агент думал. Так же подписывает их расширение для vscode
	// («Thought for 5s»).
	Spent int64 `json:"spent,omitempty"`
	// Logout помечает ответ агента, который на деле служебная строка про
	// истёкший логин («Login expired. Please run /login»). По ней панель
	// поднимает состояние разговора, не разбирая слов ответа сама: признак
	// один и считает его сервер. Следующий настоящий ответ приходит без
	// пометки и гасит состояние.
	Logout bool `json:"logout,omitempty"`
	// Fail помечает ответ инструмента, который вернулся ошибкой (is_error у
	// tool_result). По нему лента красит точку записи: зелёная у сделанного,
	// красная у упавшего, и провал видно, не читая вывод.
	Fail bool `json:"fail,omitempty"`
	// Args это поля вызова инструмента как они есть: по ним лента рисует ход
	// по-своему у каждого инструмента (файл с диапазоном строк у Read, дифф у
	// Edit). Text для этого не годится: он собран в одну строку под копирование
	// и режется по общему потолку.
	Args map[string]string `json:"args,omitempty"`
}

// toolNoteKeys это порядок полей ввода, из которых собирается однострочная
// подпись вызова: первое найденное и есть суть вызова. Описание хода тут
// последнее нарочно: подпись говорит, что именно вызвано (команда, файл,
// шаблон), а человеческое пояснение едет своим полем.
var toolNoteKeys = []string{"command", "file_path", "path", "skill", "pattern", "url", "summary", "prompt", "id", "query", "description"}

// toolAbout это пояснение хода словами: агент пишет его сам полем description
// у вызова (Bash, Task и родня). В ленте оно и стоит заголовком, как в vscode,
// а команда уходит второй частью строки: «у нас нет поясняющих сообщений к
// командам» человек сказал ровно потому, что дашборд это поле не вёз.
func toolAbout(input map[string]any) string {
	v, _ := input["description"].(string)
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return truncate(strings.Join(strings.Fields(v), " "), 160)
}

// toolBodyLimit это потолок текста инструмента в ленте: вывод сборки бывает в
// мегабайт, и тянуть его на телефон незачем, а первых тысяч знаков хватает,
// чтобы понять, чем занят агент.
const toolBodyLimit = 4000

// toolBody собирает читаемое тело вызова: команда целиком, а не обрезанная
// подпись, плюс остальные строковые поля ввода.
// argLimit это потолок одного поля вызова: дифф правки бывает длинным, и
// лента показывает его свёрнутым, а остальные поля коротки сами по себе.
const argLimit = 8000

// toolArgs отдаёт поля вызова строками. Числа и признаки тоже идут в дело:
// у Read диапазон строк лежит числами, и без них строка хода молчала бы о том,
// какой кусок файла читали.
func toolArgs(input map[string]any) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range input {
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) == "" {
				continue
			}
			out[k] = truncate(t, argLimit)
		case bool:
			out[k] = strconv.FormatBool(t)
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toolBody(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v, ok := input[k].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(k + ": " + v)
	}
	return truncate(b.String(), toolBodyLimit)
}

// resultText разворачивает содержимое tool_result: строкой либо списком
// text-блоков, как их пишет харнес.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return truncate(strings.TrimRight(s, "\n"), toolBodyLimit)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			out = append(out, b.Text)
		}
	}
	return truncate(strings.TrimRight(strings.Join(out, "\n"), "\n"), toolBodyLimit)
}

func toolNote(input map[string]any) string {
	for _, key := range toolNoteKeys {
		if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncate(strings.Join(strings.Fields(v), " "), 160)
		}
	}
	return ""
}

// parseReplies разбирает строки jsonl в реплики, нумеруя с startSeq: живое
// дострение продолжает счёт, и пагинация назад не съезжает. Битая строка
// пропускается без обрушения ленты, служебные записи (queue-operation,
// attachment и родня) и ветки субагентов (isSidechain) в ленту не попадают.
// parseReplies разбирает транскрипт сессии: ветки субагентов (isSidechain)
// сюда не идут, у них свои боковые журналы.
func parseReplies(data []byte, startSeq int) []reply {
	return parseRepliesOpt(data, startSeq, false)
}

// parseRepliesOpt это тот же разбор с одним отличием: боковой журнал субагента
// состоит из записей isSidechain целиком, и для него отсев надо снимать, иначе
// файл читается пустым (находка тринадцатого круга POC).
func parseRepliesOpt(data []byte, startSeq int, side bool) []reply {
	return parseRepliesSpan(data, startSeq, parseSpan{side: side})
}

// parseSpan описывает кусок журнала: чей он, с какого байта файла прочитан и
// снимать ли отсев записей isSidechain. Названный источник значит, что разбор
// сам проставляет записям устойчивый ключ «источник:смещение строки.номер
// блока»: журналы только дописываются, и смещение строки в своём файле не
// меняется никогда. Ключ по смещению, а не по номеру записи, потому что номер
// записи известен только тому, кто прочитал файл с начала, а хвост ленты
// читается кусками с конца (feed.go).
type parseSpan struct {
	src  string
	off  int64
	side bool
}

func parseRepliesSpan(data []byte, startSeq int, sp parseSpan) []reply {
	var out []reply
	seq := startSeq
	// Место строки в файле и номер блока внутри строки: из них собирается ключ
	// записи. Одна строка jsonl даёт несколько записей ленты (текст, вызов
	// инструмента, его ответ), поэтому одного смещения ключу мало.
	lineAt, blk := int64(0), 0
	// prev это метка предыдущей разобранной записи: длительность размышления
	// это расстояние от неё до метки самого размышления, потому что думать
	// агент начинает сразу после того, что было до него.
	var prev time.Time
	// Адреса, с которых в этот разговор приходил человек панелью. Свой сокет
	// стоит тут с самого начала, а прежние приезжают из самой переписки: по ним
	// узнаётся ответная отправка агента человеку.
	dash := map[string]bool{peerSelfAddr(): true}
	// Реплика человека, которая ждёт ответа, и счёт таких реплик подряд. Агент
	// отвечает на пачку замечаний одним сообщением, и цитируется последняя из
	// пачки, а о самой пачке говорит признак.
	askText, askKey, askAt := "", "", 0
	add := func(item reply) {
		if item.Role == roleThink {
			if at, err := time.Parse(time.RFC3339, item.Time); err == nil && !prev.IsZero() {
				if d := at.Sub(prev); d > 0 && d < time.Hour {
					item.Spent = d.Milliseconds()
				}
			}
		}
		if at, err := time.Parse(time.RFC3339, item.Time); err == nil {
			prev = at
		}
		item.Seq = seq
		if sp.src != "" {
			item.Key = sp.src + ":" + strconv.FormatInt(sp.off+lineAt, 10) + "." + strconv.Itoa(blk)
		}
		blk++
		seq++
		switch {
		case askedReply(item):
			askText, askKey = item.Text, item.Key
			askAt++
		case answerReply(item) && askAt > 0:
			item.Quote = peekRunes(askText, quotePeek)
			item.QuoteKey = askKey
			item.QuoteMany = askAt > 1
			askText, askKey, askAt = "", "", 0
		}
		out = append(out, item)
	}
	text := string(data)
	for pos := 0; pos <= len(text); {
		ln := text[pos:]
		lineAt, blk = int64(pos), 0
		if cut := strings.IndexByte(ln, '\n'); cut >= 0 {
			ln, pos = ln[:cut], pos+cut+1
		} else {
			pos = len(text) + 1
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			// Пометки харнеса на записи, которую он подставил от имени
			// человека. isCompactSummary это пересказ съеденного начала
			// разговора, isMeta это его же служебные строки вроде «continue
			// from where you left off». Пузырём человека они были бы враньём:
			// человек этого не писал (замечание пользователя про портянку на
			// несколько тысяч слов в чате).
			IsCompactSummary bool `json:"isCompactSummary"`
			IsMeta           bool `json:"isMeta"`
			Message          struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if (rec.IsSidechain && !sp.side) || (rec.Type != "user" && rec.Type != "assistant") {
			continue
		}
		var s string
		if json.Unmarshal(rec.Message.Content, &s) == nil {
			if sock := dashSock(s); sock != "" {
				dash[sock] = true
			}
			if rec.IsCompactSummary {
				add(reply{Role: roleNote, Time: rec.Timestamp, Text: s,
					Note: compactWord, Mark: compactMark})
				continue
			}
			addUser(add, rec.Type, rec.Timestamp, s, rec.IsMeta)
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Input     map[string]any  `json:"input"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if sock := dashSock(b.Text); sock != "" {
					dash[sock] = true
				}
				if rec.IsCompactSummary {
					add(reply{Role: roleNote, Time: rec.Timestamp, Text: b.Text,
						Note: compactWord, Mark: compactMark})
					continue
				}
				addUser(add, rec.Type, rec.Timestamp, b.Text, rec.IsMeta)
			case "thinking":
				// Текст размышлений едет в ленту (POC ветки poc-chat): прежде
				// сервер выбрасывал его, и на экране стояла метка «размышления
				// свёрнуты», из которой ничего не следовало. Свёрнутым блоком
				// его рисует панель, разворот кликом.
				add(reply{Role: roleThink, Time: rec.Timestamp, Text: b.Thinking})
			case "tool_use":
				// Отправка в панель это обращение к человеку: агент отвечает
				// ему тем же каналом, которым пришла реплика, и в ленте такому
				// ходу место пузырём разговора. Узнаётся он по адресу
				// доставки, а не по имени инструмента: тем же SendMessage
				// агент говорит и с субагентом, и это служебный ход.
				add(reply{Role: "tool", Time: rec.Timestamp, Tool: b.Name,
					Note: toolNote(b.Input), About: toolAbout(b.Input),
					Text: toolBody(b.Input), Args: toolArgs(b.Input), ToolID: b.ID,
					Human: b.Name == sendTool && dash[sendToTarget(b.Input)]})
			case "tool_result":
				// Вывод инструмента показывается как есть, обрезанным по длине:
				// по нему видно, что агент делает, а свёрнутая строка «Bash»
				// про это молчала.
				if text := resultText(b.Content); text != "" {
					add(reply{Role: roleToolOut, Time: rec.Timestamp, Text: text,
						Fail: b.IsError, ToolID: b.ToolUseID})
				} else {
					// Пустой ответ инструмента в ленту не идёт, но закрытие
					// вызова им отмечается: по нему видно, кончился ли субагент.
					add(reply{Role: roleToolOut, Time: rec.Timestamp, Fail: b.IsError, ToolID: b.ToolUseID})
				}
			}
		}
	}
	return out
}

// roleThink это роль размышления в ленте: имя одно на сервер и на панель.
const roleThink = "thinking"

// roleToolOut это роль ответа инструмента: имя одно на сервер и на панель.
const roleToolOut = "toolout"

// Реплика, пришедшая каналом живых сессий, лежит в транскрипте в служебной
// обёртке: строка «Another Claude session sent a message:», тег
// cross-session-message с атрибутами отправителя и хвостовое предостережение
// харнеса про permission laundering. Всё это адресовано агенту, а не человеку,
// и в ленте оно закрывало собой сам текст: две строки про раскладку показывались
// простынёй на пятнадцать. Разворачивается обёртка тут, на сервере: читателей у
// ленты двое, и разбор в клиенте пришлось бы держать в двух местах.
var peerWrapRe = regexp.MustCompile(`(?s)<cross-session-message\b([^>]*)>(.*?)</cross-session-message>`)

var peerFromRe = regexp.MustCompile(`from-name="([^"]*)"`)

// peerAddrRe вынимает адрес отправителя рамки. По нему узнаётся обратная
// сторона разговора: агент отвечает человеку в тот же сокет, и ход отправки с
// этим адресом это реплика, а не служебный ход.
var peerAddrRe = regexp.MustCompile(`\bfrom="([^"]*)"`)

// dashboardPeer это имя отправителя, каким подписывается сам дашборд: реплику
// с него пишет человек, дашборд только несёт.
const dashboardPeer = "dashboard"

// Автор реплики словом: «вы» у человека, «агент-диспетчер» у живой сессии
// клиента, «агент» у всего прочего, что пришло каналом. Слова одни на сервер и
// на панель.
const (
	whoHuman = "вы"
	whoLead  = "агент-диспетчер"
	whoAgent = "агент"
)

// peerKinds это короткий кэш реестра живых сессий клиента: у каждой записи там
// написано имя и чем сессия является. Реестр читается не чаще раза в
// peerKindsTTL: реплик канала в ленте бывают сотни, и обход каталога на каждую
// стоил бы дороже самой ленты.
var peerKinds struct {
	sync.Mutex
	at   time.Time
	kind map[string]string
}

const peerKindsTTL = 5 * time.Second

// peerKind говорит, чем был отправитель реплики: interactive это живая сессия
// клиента (окно человека либо сессия, поднятая дашбордом), прочее это другой
// процесс. Имени нет в реестре, значит сессия уже кончилась, и чем она была,
// сказать нечем.
func peerKind(name string) string {
	peerKinds.Lock()
	defer peerKinds.Unlock()
	if peerKinds.kind == nil || time.Since(peerKinds.at) > peerKindsTTL {
		peerKinds.kind = readPeerKinds()
		peerKinds.at = time.Now()
	}
	return peerKinds.kind[name]
}

// peerRegistryDir это каталог реестра живых сессий клиента. Он машинный, а не
// проектный, и живёт в настоящем доме человека: у второго экземпляра дашборда
// (POC) свой подложный дом, а клиент пишет реестр всё равно в настоящий.
// Отдельной переменной он ради стенда: подсовывать стенду настоящий реестр
// машины значило бы проверять чужие живые сессии.
var peerRegistryDir = func() string { return peerDir(realHomeOr("")) }

// forgetPeerKinds сбрасывает кэш реестра: зовётся стендом, у которого реестр
// меняется в пределах одного прогона.
func forgetPeerKinds() {
	peerKinds.Lock()
	defer peerKinds.Unlock()
	peerKinds.kind = nil
}

func readPeerKinds() map[string]string {
	out := map[string]string{}
	dir := peerRegistryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p peer
		if json.Unmarshal(data, &p) != nil || p.Name == "" {
			continue
		}
		out[p.Name] = p.Kind
	}
	return out
}

// peerAuthor называет автора реплики, пришедшей каналом живых сессий. «Вы»
// достаётся только тому, что человек написал сам: реплике с дашборда и словам,
// набранным в самом окне сессии. Всё, что прислал другой процесс клиента, это
// агент, и подпись у него своя: живая сессия машины ведёт работу, значит
// диспетчер, а всё прочее (субагент, чужой процесс, умершая сессия) остаётся
// просто агентом. Пузырь у неавторских реплик нейтральный, не пользовательский:
// подпись «вы» под чужими словами врала цветом заодно с именем (замечание
// пользователя).
func peerAuthor(name string) string {
	if name == "" || name == dashboardPeer {
		return ""
	}
	if peerKind(name) == "interactive" {
		return whoLead
	}
	return whoAgent
}

// peerSource подписывает реплику каналом. Своя реплика с дашборда это реплика
// человека: пишет её он, а дашборд только несёт. Чужая сессия называется своим
// именем, иначе непонятно, кто вмешался в разговор.
func peerSource(name string) string {
	// Реплика с дашборда это реплика человека, и подписывать её источником
	// незачем: он и так видит, где написал. Подпись остаётся только у чужой
	// сессии, вмешавшейся в разговор (замечание 17 двенадцатого круга POC).
	if name == dashboardPeer {
		return ""
	}
	if name == "" {
		return "из другой сессии"
	}
	return "из сессии " + name
}

// unwrapPeer достаёт текст реплики из обёртки канала и имя отправителя. Второй
// ответ говорит, была ли обёртка вообще: обычная реплика человека проходит
// мимо разбора нетронутой.
func unwrapPeer(text string) (string, string, bool) {
	m := peerWrapRe.FindStringSubmatch(text)
	if m == nil {
		return text, "", false
	}
	name := ""
	if f := peerFromRe.FindStringSubmatch(m[1]); f != nil {
		name = f[1]
	}
	return strings.TrimSpace(m[2]), name, true
}

// askedReply отвечает, реплика ли это человека. Чужие слова, приехавшие
// каналом от другого агента, подписаны своим автором и вопросом не считаются.
func askedReply(r reply) bool {
	return r.Role == "user" && r.Text != "" && r.Who == ""
}

// answerReply отвечает, ответ ли это агента человеку. Ходы инструментов,
// размышления и служебные вставки в счёт не идут, а отправка человеку каналом
// идёт наравне с обычным пузырём: она и есть ответ.
func answerReply(r reply) bool {
	if r.Text == "" {
		return false
	}
	if r.Role == "assistant" {
		return true
	}
	return r.Role == "tool" && r.Human
}

// quotePeek обрезает цитату: в пузыре она стоит одной строкой, и везти в
// панель весь абзац человека незачем.
const quotePeek = 200

func peekRunes(text string, max int) string {
	said := strings.Join(strings.Fields(text), " ")
	runes := []rune(said)
	if len(runes) <= max {
		return said
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}

// dashSock вынимает адрес дашборда из рамки, которой он принёс реплику
// человека. Адрес считается по самой переписке, а не по своему номеру процесса:
// дашборд перезапускается, сокет у него каждый раз новый, а разговор с прежним
// адресом в транскрипте лежит и читается дальше.
func dashSock(text string) string {
	m := peerWrapRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	f := peerFromRe.FindStringSubmatch(m[1])
	if f == nil || f[1] != dashboardPeer {
		return ""
	}
	a := peerAddrRe.FindStringSubmatch(m[1])
	if a == nil {
		return ""
	}
	return a[1]
}

// sendTool это инструмент межсессионной отправки, а sendToTarget называет, куда
// уехало сообщение: харнес зовёт поле to, а в части заказов recipient.
const sendTool = "SendMessage"

func sendToTarget(input map[string]any) string {
	for _, key := range []string{"to", "recipient"} {
		if s, ok := input[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// Служебные вставки харнеса в репликах роли user. Харнес кладёт их туда же, где
// лежат слова человека: уведомление о законченном фоновом субагенте, напоминание
// системы, оговорка про локальную команду и сама команда. Пузырём человека они
// не являются ни одна, и рисовать их так значит выдавать машинную служебку за
// сказанное человеком. Правило поэтому общее, а не на каждый тег отдельно:
// известные вставки вырезаются из текста, часть из них оседает короткой
// служебной строкой, а что осталось после вырезания и есть слова человека.
// Разбор живёт на сервере: читателей у ленты двое, и держать его в двух местах
// значило бы чинить каждый новый тег дважды.
const roleNote = "note"

// Замечание стоп-хука (DK-693): харнес кладёт его репликой роли user с
// префиксом «Stop hook feedback:» и без тегов, а адресовано оно модели, и
// пузырём человека оно было бы враньём. После двоеточия харнес ставит
// перевод строки.
const stopHookPrefix = "Stop hook feedback:"

const stopHookWord = "стоп-хук"

// svcTag это одна известная вставка: имя тега, показывать ли её строкой и как
// её подписать.
type svcTag struct {
	name string
	// show говорит, оставлять ли служебную строку. Оговорка про локальные
	// команды и напоминание системы адресованы модели, а не человеку, и в
	// ленте от них нет никакого проку.
	show bool
	word string
}

var svcTags = []svcTag{
	{name: "task-notification", show: true, word: "фоновый агент"},
	{name: "system-reminder", show: false},
	{name: "local-command-caveat", show: false},
	{name: "command-message", show: false},
	{name: "command-args", show: false},
	{name: "command-name", show: true, word: "команда"},
	{name: "command-contents", show: false},
	// Заказ перезапуска со сменой модели: его пишет дашборд, а не человек, и
	// пузырём человека он был бы враньём. В ленте смену называет разделитель
	// из журнала разговора, и второй записи об одном и том же там не надо.
	{name: "devkit-remodel", show: false},
	// Терминальная команда из чата (DK-480): строку с `!` клиент исполняет без
	// витка модели, а в транскрипт кладёт её и вывод тегами в реплики роли
	// user. Пузырём человека они были бы враньём, а без вывода команда из
	// панели выглядела бы ушедшей в никуда.
	{name: "bash-input", show: true, word: "команда терминала"},
	{name: "bash-stdout", show: true, word: "вывод терминала"},
	{name: "bash-stderr", show: true, word: "stderr терминала"},
}

// svcRe собирает вырезалку на каждый известный тег: тело берётся нежадно до
// своего закрывающего, поэтому вложенные теги уведомления не рвут разбор.
var svcRe = func() map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for _, t := range svcTags {
		out[t.name] = regexp.MustCompile(`(?s)<` + t.name + `(?:\s[^>]*)?>(.*?)</` + t.name + `>`)
	}
	return out
}()

// svcSummaryRe достаёт человеческую часть уведомления о фоновом агенте: сводку
// («Agent "..." finished»), а при её отсутствии итог работы. Идентификаторы
// задачи, вызова и путь файла вывода человеку не говорят ничего.
var svcSummaryRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)

var svcStatusRe = regexp.MustCompile(`(?s)<status>(.*?)</status>`)

// svcLine это готовая служебная запись: заголовок строкой и тело блоком под
// ней. Тело бывает пустым, и тогда запись это одна строка. Разделение нужно
// ленте: длинная сводка фонового агента в одну строку не влезала и лезла из
// ряда прочих записей (замечание 8).
type svcLine struct {
	head string
	body string
	// mark едет в запись ленты машинной пометкой события: слова заголовка тут
	// меняются, а пометка нет, и кружок красится по ней.
	mark string
}

// svcNote собирает служебную запись по телу вставки.
func svcNote(tag svcTag, body string) svcLine {
	body = strings.TrimSpace(body)
	switch tag.name {
	case "task-notification":
		said := "Фоновый агент завершил работу"
		if m := svcStatusRe.FindStringSubmatch(body); m != nil {
			if st := strings.TrimSpace(m[1]); st != "" && st != "completed" {
				said = "Фоновый агент: " + st
			}
		}
		if m := svcSummaryRe.FindStringSubmatch(body); m != nil {
			if sum := strings.TrimSpace(m[1]); sum != "" {
				return svcLine{head: said, mark: "agent",
					body: truncate(strings.TrimSpace(sum), toolBodyLimit)}
			}
		}
		return svcLine{head: said, mark: "agent"}
	case "command-name":
		return svcLine{head: "Команда " + truncate(strings.Join(strings.Fields(body), " "), 80)}
	case "bash-input":
		return svcLine{head: "! " + truncate(strings.Join(strings.Fields(body), " "), 80)}
	case "bash-stdout", "bash-stderr":
		// Пустой поток это не событие: у большинства команд stderr молчит, и
		// строка о пустоте только разбавляла бы ленту.
		if body == "" {
			return svcLine{}
		}
		return svcLine{head: tag.word, body: truncate(body, toolBodyLimit)}
	}
	return svcLine{head: tag.word}
}

// dispatchWrapRe ловит рамку, которой харнес заворачивает реплику, пришедшую
// работающему субагенту: сверху строка «кто прислал», снизу приписка «разберись
// с этим до конца работы». Пока боковые журналы в ленту не попадали, рамку
// никто не видел; со слиянием она полезла в чат человека английской простынёй
// поверх его же слов.
var dispatchWrapRe = regexp.MustCompile(`(?s)\A(The coordinator|Another Claude session|The user) sent a message while you were working:\s*\n(.*?)\s*\z`)

// dispatchTailRe это хвостовая приписка той же рамки: она адресована агенту, а
// не человеку, и в ленте ей делать нечего.
var dispatchTailRe = regexp.MustCompile(`(?s)\s*\n\s*Address this before completing your current task\.\s*\z`)

// dispatchWord называет отправителя рамки по-русски: в ленте это служебная
// строка, а не пузырь человека, и по подписи видно, кто вмешался в работу.
func dispatchWord(who string) string {
	switch who {
	case "Another Claude session":
		return "чужая сессия -> субагенту"
	case "The user":
		return "человек -> субагенту"
	}
	return "диспетчер -> субагенту"
}

// unwrapDispatch снимает рамку и отдаёт то, что в ней написано, вместе с
// подписью отправителя.
func unwrapDispatch(text string) (string, string, bool) {
	m := dispatchWrapRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	inner := dispatchTailRe.ReplaceAllString(m[2], "")
	return strings.TrimSpace(inner), dispatchWord(m[1]), true
}

// orderRulesWord подписывает свёрнутую строку служебного хвоста заказа в ленте.
// Слово «приписки» человеку ничего не говорило: это инструкции, которые
// дашборд даёт агенту поверх реплики, так они и называются (замечание
// пользователя).
const orderRulesWord = "инструкции агента"

// rotateRuleRe узнаёт правило ротации с любым числом порога: порог приезжает
// из машинного конфига, и в разных заказах он разный. Шаблон собирается из
// самого правила, чтобы смена формулировки не разъезжалась с вырезалкой.
var rotateRuleRe = func() *regexp.Regexp {
	probe := rotateRule(987654321)
	return regexp.MustCompile(strings.Replace(regexp.QuoteMeta(probe), "987654321", `\d+`, 1))
}()

// planRuleRe узнаёт правило плана с запасным адресом: имя tmux-сессии в нём у
// каждого заказа своё. Шаблон собирается из самого правила тем же способом,
// что у ротации; старые заказы без запасного адреса сверяются константой.
var planRuleRe = func() *regexp.Regexp {
	probe := planRuleFor("probe987654321")
	return regexp.MustCompile(strings.Replace(regexp.QuoteMeta(probe), "probe987654321", `[A-Za-z0-9._-]+`, 1))
}()

// cutOrderRules отрезает от реплики приписки заказа: правила плана, ротации и
// отзывчивости, которые дашборд приклеивает к тексту человека при подъёме
// сессии (chatCmd и родня). В ленте они выглядели словами человека одним
// пузырём с его репликой (замечание пользователя: «что я написал отдельно и
// что отправилось агенту отдельно»). Шов это начало первого известного
// правила, и хвост от шва обязан состоять из известных правил целиком: правила
// сверяются точными текстами своих констант, а неузнанный хвост (сменилась
// формулировка, чужая приписка) остаётся в пузыре весь. Спрятать кусок реплики
// человека дороже, чем показать служебное.
func cutOrderRules(text string) (said, rules string) {
	cut := -1
	if i := strings.Index(text, planRule); i >= 0 {
		cut = i
	}
	if i := strings.Index(text, paceRule); i >= 0 && (cut < 0 || i < cut) {
		cut = i
	}
	if i := strings.Index(text, channelRule); i >= 0 && (cut < 0 || i < cut) {
		cut = i
	}
	if i := rotateRuleRe.FindStringIndex(text); i != nil && (cut < 0 || i[0] < cut) {
		cut = i[0]
	}
	if cut < 0 {
		return text, ""
	}
	rest := text[cut:]
	for {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		// Правило с запасным адресом длиннее голой константы, и первым
		// сверяется оно: константа съела бы общий префикс, а хвост про запасной
		// файл остался бы неузнанным, и реплика вернулась бы целиком.
		if m := planRuleRe.FindStringIndex(rest); m != nil && m[0] == 0 {
			rest = rest[m[1]:]
			continue
		}
		switch {
		case strings.HasPrefix(rest, planRule):
			rest = rest[len(planRule):]
		case strings.HasPrefix(rest, paceRule):
			rest = rest[len(paceRule):]
		case strings.HasPrefix(rest, channelRule):
			rest = rest[len(channelRule):]
		default:
			m := rotateRuleRe.FindStringIndex(rest)
			if m == nil || m[0] != 0 {
				return text, ""
			}
			rest = rest[m[1]:]
		}
	}
	return strings.TrimSpace(text[:cut]), strings.TrimSpace(text[cut:])
}

// addUser кладёт в ленту реплику роли user (и текстовый блок ответа агента тем
// же путём). Пузырём человека рисуется только то, что человек написал:
// служебные вставки харнеса уходят отдельными строками, а реплика, кроме них не
// несущая ничего, пузыря не заводит вовсе.
func addUser(add func(reply), role, at, text string, meta bool) {
	// Приписки заказа отрезаются первыми: они стоят хвостом после слов
	// человека, а префиксные рамки (картинка, выделение) разбираются дальше по
	// оставшемуся. В ленту приписки едут свёрнутой служебной строкой после
	// пузыря, тем же видом, что и прочая служебка с телом.
	if said, rules := cutOrderRules(text); rules != "" {
		text = said
		defer add(reply{Role: roleNote, Time: at, Text: rules, Note: orderRulesWord})
	}
	// Скилл приезжает в ленту дважды: вызовом инструмента Skill и следом
	// простынёй самого скилла, которую харнес кладёт репликой человека. Второе
	// это инструкция модели, а не разговор, и показывать её незачем вовсе:
	// в ленте от скилла остаётся строка «Skill имя» (замечание 5).
	if skillBodyRe.MatchString(text) {
		return
	}
	// Реплика, пришедшая субагенту посреди работы, стоит служебной строкой со
	// своей подписью: сказал это не человек в этом чате, а тот, кто ведёт
	// работу, и рамка харнеса в ленте не нужна вовсе.
	if inner, word, wrapped := unwrapDispatch(text); wrapped {
		add(reply{Role: roleNote, Time: at, Text: inner, Note: word})
		return
	}
	if inner, name, wrapped := unwrapPeer(text); wrapped {
		w := cutWraps(inner)
		add(reply{Role: role, Time: at, Text: w.rest, Note: peerSource(name),
			Who: peerAuthor(name), Sel: w.sel, SelFile: w.file, Shot: w.shot,
			Pick: w.pick, PickScreen: w.screen})
		return
	}
	if w := cutWraps(text); w.shot != "" || w.pick != "" || w.sel != "" {
		add(reply{Role: role, Time: at, Text: w.rest, Sel: w.sel, SelFile: w.file,
			Shot: w.shot, Pick: w.pick, PickScreen: w.screen})
		return
	}
	said, notes := splitService(text)
	for _, n := range notes {
		// Служебка с телом едет как ход инструмента: подпись в Note, само
		// содержимое в Text. Без тела остаётся одна строка.
		if n.body != "" {
			add(reply{Role: roleNote, Time: at, Text: n.body, Note: n.head, Mark: n.mark})
			continue
		}
		add(reply{Role: roleNote, Time: at, Text: n.head, Mark: n.mark})
	}
	if said == "" {
		// Одна служебка без единого слова человека: пустой пузырь тут был бы
		// хуже молчания, а сама служебка уже стоит строкой выше.
		return
	}
	if meta {
		// Запись с пометкой харнеса: пузырём человека она была бы враньём,
		// человек её не писал.
		add(reply{Role: roleNote, Time: at, Text: said})
		return
	}
	add(reply{Role: role, Time: at, Text: said})
}

// compactWord это заголовок записи о сжатии разговора, а compactMark её
// машинная пометка. Слова тут про дело человека: разговор был длинный, начало
// съедено, и вместо него лежит пересказ. Про контекст и токены человеку знать
// незачем, это устройство харнеса.
const compactWord = "начало разговора сжато в пересказ"

const compactMark = "compact"

// skillBodyRe узнаёт простыню скилла: харнес кладёт её репликой роли user и
// начинает строкой с каталогом скилла.
var skillBodyRe = regexp.MustCompile(`\A\s*Base directory for this skill:\s`)

// selWrapRe ловит приложенное к реплике выделение. Блок стоит префиксом, и
// текст внутри едет как есть: кавычки, переносы и разметка человека сохраняются
// целиком, потому что править их некому и незачем.
var selWrapRe = regexp.MustCompile(`(?s)\A<selection file="([^"]*)">\n(.*?)\n</selection>\n?`)

// pickWrapRe ловит приложенные к реплике места экрана. Человек тычет в элемент
// пикером вместо описания словами, и в реплику уезжает описатель: тег с
// классами, говорящие атрибуты, обрезанный текст и пара уровней родителей.
// Блок стоит префиксом, как выделение, и вид у него свой.
var pickWrapRe = regexp.MustCompile(`(?s)\A<picked screen="([^"]*)">\n(.*?)\n</picked>\n?`)

// shotWrapRe ловит приложенную картинку. Стоит она первым префиксом, перед
// выделением: так реплика читается сверху вниз, сначала что показали, потом что
// выделили, потом слова.
var shotWrapRe = regexp.MustCompile(`(?s)\A<screenshot file="([^"]*)">\n.*?\n</screenshot>\n?`)

// cutFirstWraps срезает служебные обёртки начала реплики: снимок и выделение
// кладёт панель, а не человек, и в заголовок разговора они не годятся. Порядок
// произвольный, обёрток бывает несколько.
func cutFirstWraps(text string) string {
	for {
		if m := shotWrapRe.FindString(text); m != "" {
			text = strings.TrimLeft(text[len(m):], "\n")
			continue
		}
		if m := selWrapRe.FindString(text); m != "" {
			text = strings.TrimLeft(text[len(m):], "\n")
			continue
		}
		if m := pickWrapRe.FindString(text); m != "" {
			text = strings.TrimLeft(text[len(m):], "\n")
			continue
		}
		return text
	}
}

// cutShot отрезает префикс картинки от остального.
func cutShot(text string) (string, string) {
	m := shotWrapRe.FindStringSubmatch(text)
	if m == nil {
		return "", text
	}
	return m[1], strings.TrimSpace(text[len(m[0]):])
}

// cutSelection отрезает префикс выделения от слов человека. Пустое выделение
// значит, что реплика приехала без него, и это обычный случай.
// wraps это приложенное к реплике, снятое с её начала: снимок экрана, места
// пикера и выделенный кусок текста. Порядок съёма один на все дороги реплики,
// потому что кладёт префиксы панель в том же порядке.
type wraps struct {
	shot   string
	pick   string
	screen string
	sel    string
	file   string
	rest   string
}

func cutWraps(text string) wraps {
	var w wraps
	w.shot, w.rest = cutShot(text)
	w.pick, w.screen, w.rest = cutPicked(w.rest)
	w.sel, w.file, w.rest = cutSelection(w.rest)
	return w
}

// cutPicked отрезает от реплики блок выбранных мест: в пузыре остаются слова
// человека, а сами описатели встают при пузыре свёрнутым блоком.
func cutPicked(text string) (pick, screen, rest string) {
	m := pickWrapRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", text
	}
	return m[2], m[1], strings.TrimSpace(text[len(m[0]):])
}

func cutSelection(text string) (sel, file, rest string) {
	m := selWrapRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", text
	}
	return m[2], m[1], strings.TrimSpace(text[len(m[0]):])
}

// splitService вырезает из реплики известные служебные вставки. Первый ответ
// это то, что написал человек, второй это служебные строки в порядке
// появления. Незнакомая вставка тут не трогается вовсе: выдумывать правило на
// тег, которого мы не видели, дороже, чем показать его как есть.
func splitService(text string) (string, []svcLine) {
	var notes []svcLine
	out := text
	// Замечание стоп-хука едет префиксом без тегов и обёрток, и теговые узоры
	// его не узнают: запись оставалась безымянной служебкой, и лента рисовала
	// её серой строкой во всю высоту (DK-693). С подписью она рисуется тем же
	// блоком, что и прочая служебка с телом.
	if body, ok := strings.CutPrefix(out, stopHookPrefix); ok {
		if body = strings.TrimSpace(body); body != "" {
			notes = append(notes, svcLine{head: stopHookWord, body: truncate(body, toolBodyLimit)})
			out = ""
		}
	}
	for _, tag := range svcTags {
		re := svcRe[tag.name]
		for {
			m := re.FindStringSubmatchIndex(out)
			if m == nil {
				break
			}
			body := out[m[2]:m[3]]
			if tag.show {
				if n := svcNote(tag, body); n.head != "" {
					notes = append(notes, n)
				}
			}
			out = out[:m[0]] + out[m[1]:]
		}
	}
	return strings.TrimSpace(out), notes
}

const repliesDefault = 40

const repliesMax = 500

// headScanMax держит число сессий, у которых читается шапка в общем списке:
// список показывают десятком свежих строк, а проект с сотней транскриптов
// иначе перечитывал бы их все на первый же запрос.
const headScanMax = 50

// taskScanBudget это потолок ожидания при поиске сессий одной задачи. Поиск по
// ID идёт по всем транскриптам проекта: разговор законченной работы лежит тем
// глубже, чем дольше она закончилась, и под окном свежих его как раз и не
// находили (DK-280). Цена обхода это чтение шапок, и она платится один раз:
// дочитанная голова оседает в памяти процесса, второй заход укладывается в
// миллисекунды. Бюджет остаётся страховкой на дерево, где транскриптов накопились
// тысячи: экран получает найденное и слова о том, что обход не дошёл до конца,
// вместо долгого ожидания.
const taskScanBudget = 3 * time.Second

// taskParamRe сито параметра ?task=: ID задачи, а не любая строка из адреса.
var taskParamRe = regexp.MustCompile(`^[A-Z]{2,10}-\d{1,6}$`)

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "сессии")
	if found == nil {
		return
	}
	want := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("task")))
	// free=1 это список разговоров проекта без задачи: им живёт общий чат доски
	// (LLD DK-430, решение 7), и своего экрана «Чаты» под него не заводится.
	// Вместе с ?task= ключ не едет: там спрашивают разговоры одной задачи, тут
	// ровно те, у которых её нет.
	free := r.URL.Query().Get("free") == "1"
	if free && want != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "free=1 и task= вместе не читаются: первый просит чаты без задачи, второй чаты одной задачи"})
		return
	}
	if want != "" && !taskParamRe.MatchString(want) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", want)})
		return
	}
	roots := s.transcriptRoots()
	files := sessionFiles(roots, found.Path)
	limit := headScanMax
	if want != "" {
		limit = len(files)
	}
	binds := s.binds()
	deadline := s.now().Add(taskScanBudget)
	sessions := []sessionInfo{}
	var scanned, others, unknown int
	cut := false
	for _, f := range files {
		if scanned >= limit {
			break
		}
		if want != "" && s.now().After(deadline) {
			cut = true
			break
		}
		scanned++
		head := s.sessionHeadCached(f.path, f.stamp)
		f.Branch, f.First = head.Branch, head.First
		f.Tree = f.suffix
		f.Task, f.TaskNote, f.Bound = bindTask(binds, f.ID, f.suffix, head)
		if f.Task == "" {
			unknown++
		} else if want != "" && f.Task != want {
			others++
		}
		// Чужая сессия в ленту задачи не идёт: до DK-252 экран брал свежую по
		// mtime, и при двух живых окнах под заголовком одной задачи шёл ход
		// соседней.
		if want != "" && f.Task != want {
			continue
		}
		if free && f.Task != "" {
			continue
		}
		sessions = append(sessions, f)
	}
	if n := intParam(r, "n", 20, 100); len(sessions) > n {
		sessions = sessions[:n]
	}
	s.fillChatState(found.Path, sessions)
	resp := map[string]any{"project": found.Name, "sessions": sessions}
	// Пустоты различимы: транскриптов нет вовсе это слова с адресом, где они
	// искались, а «сессий этой задачи нет» это счёт по чужим и нераспознанным.
	// Обрыв обхода по бюджету называется словами и при найденной сессии: она
	// может оказаться не единственной, и молчание тут выдало бы неполный поиск
	// за полный.
	switch {
	case len(files) == 0:
		resp["note"] = fmt.Sprintf("транскриптов нет: с путём %s нет сессий ни в одном каталоге журналов (%s)",
			found.Path, strings.Join(roots, ", "))
	case want != "" && cut:
		resp["note"] = fmt.Sprintf(
			"обход прерван по времени: просмотрено %d транскриптов из %d, чат задачи %s мог остаться дальше",
			scanned, len(files), want)
	case len(sessions) == 0 && free:
		resp["note"] = fmt.Sprintf(
			"чатов без задачи в проекте %s нет: просмотрено %d транскриптов, у каждого нашлась своя задача",
			found.Name, scanned)
	case len(sessions) == 0 && want != "":
		resp["note"] = fmt.Sprintf(
			"сессий задачи %s нет: просмотрены все %d транскриптов проекта, %d о других задачах, %d без распознанной задачи",
			want, scanned, others, unknown)
	}
	writeJSON(w, http.StatusOK, resp)
}

// fillChatState дописывает списку то, чем разговоры различает список панели:
// подписку, свежесть транскрипта и ручку реплики с причиной. Считается это на
// отобранной странице, а не на всех транскриптах проекта: доска и список
// tmux-сессий спрашиваются по разу на заход, а вот stat дерева идёт на каждую
// строку, и платить им за сессии, которых человек не увидит, незачем.
func (s *server) fillChatState(projPath string, list []sessionInfo) {
	if len(list) == 0 {
		return
	}
	var rows map[string]boardRow
	if raw, err := s.projectBoard(projPath); err == nil {
		rows, _ = parseBoardRows(raw)
	}
	names := harnessRoots(s.harnesses())
	alive := tmuxAliveFn()
	cutoff := s.now().Add(-sessionLiveTTL)
	for i := range list {
		list[i].Harness = names[list[i].root]
		list[i].Live = list[i].mod.After(cutoff)
		list[i].Reply, list[i].ReplyNote = s.chatReply(projPath, list[i], rows, alive)
	}
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

// findSession находит транскрипт по id среди каталогов проекта и собирает про
// него ту же строку, что стоит в списке сессий: путь с отпечатком для памяти
// процесса и хвост имени каталога, которым задача подписана в боковом дереве.
// Полный обход каталогов ради одной сессии не нужен, имя файла известно.
func findSession(roots []string, projPath, sid string) (sessionInfo, bool) {
	base := claudeDirName(projPath)
	for _, dir := range sessionDirs(roots, projPath) {
		p := filepath.Join(dir.path, sid+".jsonl")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		return sessionInfo{
			ID: sid, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
			path:   p,
			root:   dir.root,
			suffix: strings.TrimPrefix(strings.TrimPrefix(filepath.Base(dir.path), base), "-"),
			stamp:  fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()),
			mod:    fi.ModTime(),
		}, true
	}
	return sessionInfo{}, false
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "сессия")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	info, ok := findSession(s.transcriptRoots(), found.Path, sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("транскрипта %s нет среди сессий проекта %s", sid, found.Name)})
		return
	}
	path := info.path
	// Шапка читается до ленты: по ней зовётся задача сессии, а по задаче
	// собирается набор журналов отправленного, который подмешивается к
	// транскрипту (outbox.go).
	head := s.sessionHeadCached(path, info.stamp)
	info.Task, info.TaskNote, info.Bound = bindTask(s.binds(), info.ID, info.suffix, head)
	keys := saidKeys(sid, info.Task, info.Bound)
	// Чтение ленты это и есть показ разговора человеку: панель читает её,
	// пока разговор открыт на экране. Отметка нужна автоматике уборки, она по
	// ней отличает непрочитанный ответ от прочитанного.
	s.chatSeenMark(sid)
	if r.URL.Query().Get("stream") == "1" {
		s.streamSession(w, r, sid, path, keys)
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("транскрипт не прочитался: %v", err)})
		return
	}
	n := intParam(r, "n", repliesDefault, repliesMax)
	// «Раньше» режется по устойчивому ключу записи, а не по её месту в ленте:
	// место плывёт от роста боковых журналов, и страницы истории налезали друг
	// на друга. Число тут понимается как место и остаётся ради старых вкладок,
	// открытых до этой правки.
	before := r.URL.Query().Get("before")
	// Лента собирается хвостом в запрошенное окно, а не чтением всех журналов
	// разговора целиком (feed.go). Страница истории просит окно шире, пока её
	// курсор в окно не попал: дальше начала разговора просить нечего.
	want := n + feedSlack
	var feed sessionFeed
	var items []reply
	for {
		feed = sessionFeedOf(path, want)
		items = feed.items
		// Журнал разговора режется по тому же окну, каким собран транскрипт:
		// целиком он старше окна и вставал бы одной кучей перед его первой
		// записью (saidCut).
		from := feedFrom(items, feed.whole)
		for _, key := range keys {
			items = saidMerge(items, saidCut(saidLoad(s.cfg.Home, key), from))
		}
		if feed.whole || !strings.Contains(before, ":") || keyRoom(items, before, n) || want >= feedMost {
			break
		}
		want *= feedGrow
	}
	total := len(items)
	kept := beforeCut(items, before)
	// Начало разговора называет сервер: считать его по номеру первой записи
	// клиент больше не может, номера у него не свои. Начало это целиком
	// собранная лента, у которой хвост не обрезан окном.
	start := feed.whole && len(kept) > 0 && len(kept) <= n
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	items = kept
	if items == nil {
		items = []reply{}
	}
	// Шапка едет вместе с лентой: экран агента открывается и по id сессии, а
	// строки доски у такого захода нет, и заголовок ему брать больше неоткуда
	// (DK-294). Задача тут названа так же, как в списке: узнанная с подписью,
	// чем узнана, либо пустая с подписью «свободный чат».
	info.Branch, info.First = head.Branch, head.First
	info.Tree = info.suffix
	// Ручка для реплики едет в шапке разговора: панель не считает её сама,
	// иначе мера кончившегося разговора разошлась бы с той, по которой
	// сторожок будит строку. Доска тут читается из памяти процесса, а не
	// вызовом taskctl на каждый заход; не прочиталась, значит признака
	// парковки нет, а два остальных работают.
	var rows map[string]boardRow
	if raw, err := s.projectBoard(found.Path); err == nil {
		rows, _ = parseBoardRows(raw)
	}
	info.Harness = harnessRoots(s.harnesses())[info.root]
	info.Live = info.mod.After(s.now().Add(-sessionLiveTTL))
	info.Reply, info.ReplyNote = s.chatReply(found.Path, info, rows, tmuxAliveFn())
	resp := map[string]any{"session": sid, "head": info, "total": total, "items": items, "start": start}
	// План сессии едет вместе с лентой: экран задачи показывает его блоком, а
	// считать его на клиенте значило бы второй разбор транскрипта.
	if plan := planOf(s.home(), sid, s.sessionTmux(sid), path, s.now()); plan != nil {
		resp["plan"] = plan
	}
	if total == 0 {
		resp["note"] = emptyTranscriptNote
	}
	writeJSON(w, http.StatusOK, resp)
}

// beforeCut отрезает всё, что стоит раньше названной записи. Ключ устойчив
// («источник:номер в файле»), и найденное место не зависит от того, сколько
// записей приехало в ленту с прошлой страницы. Пустой параметр это хвост
// ленты, неизвестный ключ тоже: пропавшая запись не повод отдать пустоту.
func beforeCut(items []reply, before string) []reply {
	if before == "" {
		return items
	}
	if n, err := strconv.Atoi(before); err == nil {
		if n >= 0 && n < len(items) {
			return items[:n]
		}
		return items
	}
	for i, it := range items {
		if it.Key == before {
			return items[:i]
		}
	}
	return items
}

// emptyTranscriptNote называет пустую ленту словами и в обычном ответе, и
// первым событием потока: молчащий стрим неотличим от оборвавшегося
// (замечание ревью DK-219).
const emptyTranscriptNote = "в транскрипте пока нет реплик"

// streamSession шлёт последние реплики и дальше дострение по мере записи:
// каждое событие это одна реплика в JSON. Нумерация продолжается с конца
// файла, разбираются только целые строки.
// tailSrc это дочитываемый файл ленты: своё смещение в нём и свой счётчик
// разобранных записей, из которого собирается устойчивый ключ.
type tailSrc struct {
	file  string
	label string
	src   string
	off   int64
	idx   int
}

// streamSession шлёт последние реплики и дальше дострение по мере записи:
// каждое событие это одна реплика в JSON. Дочитываются все файлы разговора
// сразу: и транскрипт сессии, и боковые журналы субагентов. Набор журналов
// пересматривается на каждом тике, потому что субагента зовут и посреди
// открытого потока, а прежний стрим цеплял один «живой» файл на открытии и
// ронял его насовсем, стоило в транскрипте закрыться любому вызову
// инструмента: после этого лента молчала до перезагрузки страницы.
func (s *server) streamSession(w http.ResponseWriter, r *http.Request, sid, path string, keys []string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	seq := 0
	// Журналы отправленного дочитываются наравне с транскриптом: реплика,
	// уехавшая с одного устройства, приходит событием потока на все открытые
	// экраны, а не остаётся местным пузырём отправителя (outbox.go).
	saidTails := []*tailSrc{}
	for _, key := range keys {
		saidTails = append(saidTails, &tailSrc{
			file: saidFile(s.cfg.Home, key), src: saidSrc + key})
	}
	subs := map[string]*tailSrc{}
	// known помнит журналы, заведённые до открытия потока: их хвост уже уехал
	// в ленту, и дочитывать их надо с текущего конца. Журнал, появившийся
	// позже, читается с начала: он пуст в момент появления, и всё в нём новое.
	{
		feed := sessionFeedOf(path, repliesDefault+feedSlack)
		items := feed.items
		from := feedFrom(items, feed.whole)
		for _, key := range keys {
			items = saidMerge(items, saidCut(saidLoad(s.cfg.Home, key), from))
		}
		seq = len(items)
		for i, t := range saidTails {
			// Номер записи журнала это её номер в файле, а не место в слитой
			// ленте: слияние выбрасывает записи, у которых уже пришло эхо, и
			// счёт по выжившим уезжал назад. Дописанная строка получала тогда
			// чужой номер, лента видела ту же реплику под двумя ключами и
			// показывала её двумя пузырями (снимок пользователя).
			t.idx = saidLines(s.cfg.Home, keys[i])
			if fi, err := os.Stat(t.file); err == nil {
				t.off = fi.Size()
			}
		}
		for _, log := range subLogs(path) {
			src := srcName(log.File)
			subs[log.File] = &tailSrc{file: log.File, label: log.Label,
				src: src, off: feed.ends[src]}
		}
		if len(items) > repliesDefault {
			items = items[len(items)-repliesDefault:]
		}
		for _, item := range items {
			sseEvent(w, f, "", marshalReply(item))
		}
		offset = feed.ends[mainSrc]
	}
	// Пустая лента называется первым событием, как в обычном ответе: молчащий
	// поток неотличим от оборвавшегося. Дострение дальше идёт как обычно.
	if seq == 0 {
		sseEvent(w, f, "note", emptyTranscriptNote)
	}
	stamp := subStamp(path)
	// Имя tmux читается один раз на поток: реестр это файл на диске, и ходить в
	// него каждым тиком незачем, а запись сессии ложится хуком старта, то есть
	// раньше, чем открывается поток её ленты.
	tmux := s.sessionTmux(sid)
	planAt := planStamp(s.home(), sid, tmux)
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			// План переписан файлом: транскрипт при этом молчит, и заметить
			// смену можно только по метке файла своей сессии. Чужие планы в
			// каталоге не смотрятся вовсе.
			if now := planStamp(s.home(), sid, tmux); now != planAt {
				planAt = now
				sendPlan(w, f, planOf(s.home(), sid, tmux, path, s.now()))
			}
			// Новый субагент заводит свой файл, и каталог журналов меняется:
			// по этой метке набор и пересматривается, без чтения мета-файлов
			// на каждом тике.
			if now := subStamp(path); now != stamp {
				stamp = now
				for _, log := range subLogs(path) {
					if _, ok := subs[log.File]; ok {
						continue
					}
					subs[log.File] = &tailSrc{file: log.File, label: log.Label,
						src: srcName(log.File)}
				}
			}
			// Отправленное с любого устройства первым: оно уже уехало
			// агенту, и ждать, пока его отразит транскрипт (а по дороге
			// задачи он не отразит вовсе), значит держать чужой экран
			// пустым.
			for _, t := range saidTails {
				var lines []string
				lines, t.off = newLines(t.file, t.off)
				if len(lines) == 0 {
					continue
				}
				list, next := saidReplies(lines, t.src, t.idx)
				t.idx = next
				for _, item := range list {
					item.Seq = seq
					seq++
					sseEvent(w, f, "", marshalReply(item))
				}
			}
			// Сперва субагенты: пока они работают, транскрипт сессии молчит, и
			// ждать его записи значило бы держать ленту пустой. Обход по имени
			// файла, чтобы порядок событий не зависел от обхода карты.
			files := make([]string, 0, len(subs))
			for file := range subs {
				files = append(files, file)
			}
			sort.Strings(files)
			for _, file := range files {
				src := subs[file]
				var list []reply
				list, _, src.off = newChunk(src.file, src.src, src.off, true)
				for _, item := range list {
					item.Sub = src.label
					// Заказ субагенту (первая запись журнала) уже показан
					// карточкой вызова Agent, а пузыря человека в боковом
					// журнале не бывает вовсе: там пишет не он. Первая запись
					// узнаётся по своему ключу: он считается от смещения строки
					// в файле, и счёт разобранных записей стриму держать
					// больше незачем.
					if item.Role == "user" {
						if item.Key == src.src+":0.0" {
							continue
						}
						item.Role = roleNote
					}
					// Тот же дубль на живом дописывании: карточка отправителя
					// уже стоит в ленте.
					if item.Role == roleNote && item.Note == dispatchWord("") {
						continue
					}
					item.Seq = seq
					seq++
					sseEvent(w, f, "", marshalReply(item))
				}
			}
			var list []reply
			var raw []byte
			list, raw, offset = newChunk(path, mainSrc, offset, false)
			if len(raw) == 0 {
				continue
			}
			// Субагент заводится посреди хода, и с ним сессия становится
			// диспетчером: подпись её ответов считается на каждой порции, а не
			// один раз на открытие потока.
			lead := len(subLogs(path)) > 0
			for _, item := range markLogout(markLead(list, lead)) {
				item.Seq = seq
				seq++
				sseEvent(w, f, "", marshalReply(item))
			}
			// План сессии живёт своим событием: он приходит целиком и меняет
			// не ленту, а блок плана и кольцо, поэтому в поток реплик его
			// класть нечем.
			if todo, _ := sessionPlan(raw); todo != nil {
				sendPlan(w, f, planOf(s.home(), sid, tmux, path, s.now()))
			}
		}
	}
}

// home это дом дашборда, безопасный к пустому серверу: стенды поднимают его
// голым, и лезть в настройки без проверки значит ронять поток на ровном месте.
func (s *server) home() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.Home
}

// planStamp это метка файла плана сессии: по ней стрим замечает, что агент
// переписал план, не тронув транскрипта.
func planStamp(home, sid, tmux string) string {
	out := ""
	for _, dir := range []string{realHome(), home} {
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(planPath(dir, sid)); err == nil {
			out += fi.ModTime().String()
			continue
		}
		// Запасной адрес по имени tmux смотрится тем же порядком, что в planOf:
		// иначе план, выполненный по запасному правилу, менялся бы незаметно
		// для потока.
		if tmux != "" {
			if fi, err := os.Stat(planPath(dir, tmux)); err == nil {
				out += fi.ModTime().String()
			}
		}
	}
	return out
}

// sessionTmux называет tmux-имя сессии по реестру чатов: план сессии без
// CLAUDE_CODE_SESSION_ID лежит файлом этого имени, и без реестра его не найти.
// Голый сервер стендов реестра не держит, ему пустое имя.
func (s *server) sessionTmux(sid string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.binds()[sid].Tmux
}

// sendPlan шлёт план событием потока. Пустой план тоже событие: пункты бывают
// сняты все разом, и лента обязана убрать блок, а не держать старый список.
func sendPlan(w http.ResponseWriter, f http.Flusher, plan []planItem) {
	if plan == nil {
		plan = []planItem{}
	}
	if data, err := json.Marshal(plan); err == nil {
		sseEvent(w, f, "plan", string(data))
	}
}

// subStamp это метка боковых журналов: по ней поток видит, что у сессии
// что-то изменилось. Каталог меняется от нового субагента, а вот ход уже
// начатой работы каталога не трогает вовсе, и метки по каталогу не хватало:
// работа кончалась, сегмент кольца обязан был позеленеть, а план не
// пересылался, пока не заведётся следующий субагент. Кольцо стояло
// замороженным (жалоба пользователя, третий заход к одной теме). Поэтому в
// метку идёт и время последней записи в самих журналах: пишет субагент, метка
// двигается.
func subStamp(path string) string {
	dir := subDir(path)
	fi, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fi.ModTime().String()
	}
	last := time.Time{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return fi.ModTime().String() + ":" + strconv.Itoa(len(entries)) + ":" + last.String()
}

func marshalReply(item reply) string {
	data, err := json.Marshal(item)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// modelTailLimit это сколько байт хвоста транскрипта читается ради модели:
// запись ответа лежит в конце, а файл весит мегабайты, и тянуть его целиком
// ради одного поля незачем.
const modelTailLimit = 256 * 1024

// readSessionModel достаёт модель из последней записи ответа. Пусто значит,
// что ответов в хвосте нет вовсе (свежая сессия, один вопрос человека) либо
// харнес поля не пишет.
func readSessionModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	from := int64(0)
	if size > modelTailLimit {
		from = size - modelTailLimit
	}
	if _, err := f.Seek(from, 0); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], "\"model\"") {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue
		}
		// <synthetic> это не модель, а пометка харнеса на записях, которых
		// модель не писала вовсе (служебные вставки, оборванный ход): показывать
		// её выбором модели было бы враньём, и поиск идёт дальше вглубь.
		if rec.Type == "assistant" && rec.Message.Model != "" &&
			!strings.HasPrefix(rec.Message.Model, "<") {
			return rec.Message.Model
		}
	}
	return ""
}

// modelShort сводит идентификатор модели к короткому имени лестницы:
// claude-fable-5 это fable, claude-sonnet-4-5-20250929 это sonnet. Таблица та
// же, что у agentctl: ярусы там названы этими же словами, и список выбора в
// панели собран из них.
func modelShort(id string) string {
	if id == "" {
		return ""
	}
	low := strings.ToLower(id)
	for _, name := range []string{"fable", "opus", "sonnet", "haiku"} {
		if strings.Contains(low, name) {
			return name
		}
	}
	// Чужой поставщик называет модели по-своему (glm-5.3), и резать их нечем:
	// имя показывается как есть.
	return id
}

// busyFresh это порог свежести транскрипта, за которым сессия считается
// работающей: запись падает в журнал на каждом куске ответа.
const busyFresh = 20 * time.Second

// Работает ли сессия, решает транскрипт, а не поле status реестра: у сессий
// vscode харнес его не пишет вовсе (пусто у всех до единой), и мера по нему
// объявляла работающего агента простаивающим. Признаков два, хватает любого:
// журнал писался только что, либо в хвосте висит вызов инструмента без ответа,
// то есть агент сейчас в ходе (долгий ход не пишет в журнал минутами).
// busyEntry это разбор хвоста транскрипта, запомненный под отпечатком файла:
// время последней записи и число незакрытых вызовов. Ответ «работает ли
// сессия» считается уже из них и текущего времени, поэтому память не устаревает
// от одного лишь хода часов, а сам разбор (чтение хвоста и парсинг json) не
// повторяется, пока файл не двинулся. Считалось это по десятку раз на сборку
// работ и по разу в несколько секунд на живой опрос таба сессий: без памяти
// живой список стоил бы тех самых тормозов, которые лечила правка ленты.
type busyEntry struct {
	stamp string
	last  time.Time
	open  int
}

// sessionBusy отвечает, работает ли сессия, и держит разбор в памяти процесса.
func (s *server) sessionBusy(path string, now time.Time) bool {
	stamp := ""
	if fi, err := os.Stat(path); err == nil {
		stamp = fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
	}
	s.mu.Lock()
	e, hit := s.busy[path]
	s.mu.Unlock()
	if hit && stamp != "" && e.stamp == stamp {
		return busyNow(e.last, e.open, now)
	}
	last, open := sessionBusyTail(path)
	if stamp != "" {
		s.mu.Lock()
		s.busy[path] = busyEntry{stamp: stamp, last: last, open: open}
		s.mu.Unlock()
	}
	return busyNow(last, open, now)
}

// busyNow это само решение по разобранному хвосту: журнал писался только что
// либо в хвосте висит незакрытый вызов инструмента, то есть агент сейчас в
// ходе. Незакрытый вызов старше получаса это брошенный хвост закрытого окна, а
// не работа.
func busyNow(last time.Time, open int, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	if now.Sub(last) < busyFresh {
		return true
	}
	return open > 0 && now.Sub(last) < 30*time.Minute
}

// sessionBusyTail читает хвост транскрипта и отдаёт из него две меры: время
// последней записи и число незакрытых вызовов инструмента.
func sessionBusyTail(path string) (time.Time, int) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return time.Time{}, 0
	}
	from := int64(0)
	if fi.Size() > modelTailLimit {
		from = fi.Size() - modelTailLimit
	}
	if _, err := f.Seek(from, 0); err != nil {
		return time.Time{}, 0
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return time.Time{}, 0
	}
	open := map[string]bool{}
	last := time.Time{}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		if at, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil && at.After(last) {
			last = at
		}
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if b.ID != "" {
					open[b.ID] = true
				}
			case "tool_result":
				delete(open, b.ToolUseID)
			}
		}
	}
	return last, len(open)
}

// Боковые журналы субагентов (находка тринадцатого круга POC). Когда сессия
// отдаёт работу субагенту вызовом Task, её собственный транскрипт держит один
// незакрытый tool_use и молчит минутами: весь бегущий лог, который человек
// видит в окне клиента, пишется в боковой файл
// <транскрипт без .jsonl>/subagents/agent-<id>.jsonl рядом. Дашборд их не
// читал вовсе, отсюда и «в vscode бежит, на дашборде пусто». Сшиваются они по
// toolUseId из мета-файла: он же стоит идентификатором вызова Task в
// транскрипте сессии.

// subDir это каталог боковых журналов при транскрипте.
func subDir(path string) string {
	return strings.TrimSuffix(path, ".jsonl") + string(filepath.Separator) + "subagents"
}

// subMeta это то, что боковой журнал говорит о себе: чей он вызов и как назвать
// его человеку.
type subMeta struct {
	Agent  string `json:"agentType"`
	About  string `json:"description"`
	ToolID string `json:"toolUseId"`
	// Ended это время возврата работы, которое написал тот, кто её ждал.
	// Своих вызовов харнес так не помечает, поле пишет agentctl run делегату
	// второй подписки: ответа на вызов в транскрипте разговора у такой работы
	// нет вовсе, и без метки она висела бы идущей до срока молчания журнала
	// (DK-581).
	Ended string `json:"ended"`
}

// subLabel подписывает субагента в ленте: заказ словами, а при его отсутствии
// имя определения. Пустая подпись не бывает: блок должен называть себя.
func (m subMeta) label() string {
	if m.About != "" {
		return m.About
	}
	if m.Agent != "" {
		return m.Agent
	}
	return "субагент"
}

// subLog это боковой журнал вызова: сам файл, подпись вызова и заказ мета-файла
// отдельно. Заказ лежит рядом с подписью, потому что подпись бывает и служебной
// («claude», «Explore»): у вызова без заказа имя определения это всё, что о нём
// сказал харнес, и подпись работе тогда ищется в самом журнале (subOrder).
type subLog struct {
	File  string
	Label string
	About string
	Ended string
}

// subLogs сводит боковые журналы транскрипта в отображение «id вызова -> файл».
// Каталога может не быть вовсе, и это обычный случай: сессия без субагентов.
func subLogs(path string) map[string]subLog {
	out := map[string]subLog{}
	dir := subDir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m subMeta
		if json.Unmarshal(data, &m) != nil || m.ToolID == "" {
			continue
		}
		log := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".meta.json")+".jsonl")
		if _, err := os.Stat(log); err != nil {
			continue
		}
		out[m.ToolID] = subLog{File: log, Label: m.label(), About: strings.TrimSpace(m.About),
			Ended: strings.TrimSpace(m.Ended)}
	}
	return out
}

// planItem это пункт плана сессии: текст, его состояние и та же мысль в форме
// «делаю» (activeForm), которой харнес подписывает идущий пункт. Пункты
// приходят вызовом TodoWrite целиком, а не дельтой, поэтому планом сессии
// считается последний такой вызов.
type planItem struct {
	Text   string `json:"text"`
	State  string `json:"state"`
	Active string `json:"active,omitempty"`
	// Src называет источник пункта, когда это не слова агента: sub значит, что
	// пункт собран по боковому журналу субагента, а не написан планом. Пусто у
	// пунктов самого плана. Без этого человек, сверяющий кольцо с файлом
	// плана, видит в нём пункты, которых в файле нет, и читает это чужим
	// планом (живой разбор сессии devkit-2e).
	Src string `json:"src,omitempty"`
}

// planSrcSub это пункт, собранный по боковому журналу субагента: своих слов в
// плане у него нет, он машинный след розданной работы.
const planSrcSub = "sub"

// planSrcErr это пункт-жалоба на нечитаемый файл плана. Кольцо без него молчит
// ровно так же, как молчит у сессии вовсе без плана, и человек видит пустоту
// там, где этапность есть (жалоба пользователя по цели XR-286).
const planSrcErr = "err"

// planFromInput разбирает поле todos вызова TodoWrite.
func planFromInput(input map[string]any) []planItem {
	raw, ok := input["todos"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]planItem, 0, len(raw))
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["content"].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		state, _ := m["status"].(string)
		active, _ := m["activeForm"].(string)
		out = append(out, planItem{Text: truncate(text, 200), State: state,
			Active: truncate(active, 200)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sessionPlan достаёт план сессии из её транскрипта: последний вызов TodoWrite
// по файлу. Боковые журналы субагентов сюда не идут: у субагента план свой, и
// подмешанный в план сессии он читался бы как её собственные шаги.
func sessionPlan(data []byte) ([]planItem, time.Time) {
	var out []planItem
	var at time.Time
	for _, ln := range strings.Split(string(data), "\n") {
		if !strings.Contains(ln, "TodoWrite") {
			continue
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			// Пометки харнеса на записи, которую он подставил от имени
			// человека. isCompactSummary это пересказ съеденного начала
			// разговора, isMeta это его же служебные строки вроде «continue
			// from where you left off». Пузырём человека они были бы враньём:
			// человек этого не писал (замечание пользователя про портянку на
			// несколько тысяч слов в чате).
			IsCompactSummary bool `json:"isCompactSummary"`
			IsMeta           bool `json:"isMeta"`
			Message          struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil || rec.IsSidechain || rec.Type != "assistant" {
			continue
		}
		var blocks []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" || b.Name != "TodoWrite" {
				continue
			}
			if plan := planFromInput(b.Input); plan != nil {
				out = plan
				if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
					at = t
				}
			}
		}
	}
	return out, at
}

// planDir это каталог планов: сессии пишут туда файл своего плана сами, и
// каталог заводится нами, чтобы агенту не пришлось думать о его создании.
// realHomeOr отдаёт настоящий дом, а при его отсутствии тот, что дали.
func realHomeOr(home string) string {
	if h := realHome(); h != "" {
		return h
	}
	return home
}

func planDir(home string) string {
	return filepath.Join(home, ".devkit", "plans")
}

// planPath это файл плана одной сессии.
func planPath(home, sid string) string {
	return filepath.Join(planDir(home), sid+".json")
}

// planFileItem это пункт плана, как его пишет агент. Основная пара это text и
// state, а what, title и status держатся потому, что живые планы пишутся и так:
// в ~/.devkit/plans текст лежит в what у 59 пунктов и в title у 8, состояние в
// status у 15. Пункт с текстом в чужом поле раньше оседал в кольце пустотой.
type planFileItem struct {
	Text   string `json:"text"`
	What   string `json:"what"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Status string `json:"status"`
}

// label это текст пункта: первое непустое из трёх известных полей.
func (it planFileItem) label() string {
	for _, s := range []string{it.Text, it.What, it.Title} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// mark это состояние пункта, сведённое к трём известным кольцу. Слово done
// живые планы пишут наравне с completed, и без перевода закрытая работа
// показывалась ждущей.
func (it planFileItem) mark() string {
	state := it.State
	if strings.TrimSpace(state) == "" {
		state = it.Status
	}
	switch state {
	case "pending", "in_progress", "completed":
		return state
	case "done":
		return "completed"
	}
	return "pending"
}

// planFileFields перечисляет поля, за которыми лежат пункты у плана-объекта.
// Порядок тут это порядок предпочтения, а сам список взят по живым файлам
// (stages, steps, items), а не придуман.
var planFileFields = []string{"stages", "steps", "items"}

// planFileItems достаёт пункты из содержимого файла. Вид у планов два: массив
// пунктов верхнего уровня, как велит правило, и объект, у которого пункты
// лежат полем, а рядом стоят пометки самого агента (цель, виток, ветка). Второй
// вид агенты пишут сами, и разбирать его надо наравне с первым. Второе
// возвращаемое значение говорит, разобрался ли файл вообще: пустой план это не
// то же самое, что план нечитаемый.
func planFileItems(data []byte) ([]planFileItem, bool) {
	var arr []planFileItem
	if json.Unmarshal(data, &arr) == nil {
		return arr, true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return nil, false
	}
	for _, field := range planFileFields {
		raw, ok := obj[field]
		if !ok {
			continue
		}
		var list []planFileItem
		if json.Unmarshal(raw, &list) == nil {
			return list, true
		}
	}
	return nil, false
}

// readPlanFile читает план сессии из файла. Третье значение это жалоба: файл
// лежит, а плана из него не вышло. Раньше такой случай возвращался как «плана
// нет», и ошибка разбора была неотличима от штатной работы.
func readPlanFile(path string) ([]planItem, time.Time, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	raw, ok := planFileItems(data)
	if !ok {
		return nil, time.Time{}, true
	}
	out := make([]planItem, 0, len(raw))
	for _, it := range raw {
		text := it.label()
		if text == "" {
			continue
		}
		out = append(out, planItem{Text: truncate(text, 200), State: it.mark()})
	}
	if len(out) == 0 {
		// Пустой список пунктов это пустой план, а список, из которого не
		// вышло ни одного пункта, это всё та же нечитаемая запись.
		return nil, time.Time{}, len(raw) > 0
	}
	return out, fi.ModTime(), false
}

// subDoneLimit это потолок закрытых работ из журналов: у сессии, которая
// делегирует третий день, журналов десятки, а кольцо отвечает на вопрос «чем
// занята работа сейчас». Закрытые режутся до последних, живые не режутся вовсе.
const subDoneLimit = 6

// subStart это время первой записи бокового журнала, то есть время, когда
// работа ушла субагенту. Порядок работ считается по нему, а не по имени файла:
// в имени стоит id вызова, а не время. Головы файла хватает: первая запись
// лежит в начале, а читать журнал целиком ради одной метки незачем.
func subStart(file string) time.Time {
	f, err := os.Open(file)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	head := make([]byte, 8<<10)
	n, _ := f.Read(head)
	for _, ln := range strings.Split(string(head[:n]), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		if at, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
			return at
		}
	}
	return time.Time{}
}

// subOrderLimit это потолок подписи работы: строка длиннее семидесяти знаков в
// кольце и в списке работ не читается, а хвост её всё равно не виден.
const subOrderLimit = 70

// subOrder читает заказ субагента: первую содержательную строку первой реплики
// его бокового журнала. Это тот текст, который диспетчер написал субагенту, и о
// работе он говорит больше, чем короткая подпись вызова («claude», «Explore»),
// которую харнес пишет мета-файлом. Приписки заказа (правила плана, ротации,
// темпа, канала) отрезаются тем же разбором, что и в ленте: словами о работе
// они не являются. Головы файла хватает: заказ лежит первой записью.
func subOrder(file string) string {
	stamp := ""
	if fi, err := os.Stat(file); err == nil {
		stamp = fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
	}
	if said, hit := subOrderSeen.Load(file); hit && stamp != "" {
		if kept, ok := said.(subOrderEntry); ok && kept.stamp == stamp {
			return kept.said
		}
	}
	said := readSubOrder(file)
	if stamp != "" {
		subOrderSeen.Store(file, subOrderEntry{stamp: stamp, said: said})
	}
	return said
}

// Память заказов по отпечатку файла: план сессии пересчитывается на каждое
// движение журналов, а заказов у сессии, делегирующей третий день, под сотню.
// Без памяти каждый пересчёт читал бы голову каждого журнала заново, и живое
// кольцо стоило бы тех самых тормозов, которые мы лечили у ленты.
type subOrderEntry struct {
	stamp string
	said  string
}

var subOrderSeen sync.Map

func readSubOrder(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 64<<10)
	n, _ := f.Read(head)
	for _, ln := range strings.Split(string(head[:n]), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil || rec.Type != "user" {
			continue
		}
		said, _ := cutOrderRules(strings.Join(contentTexts(rec.Message.Content), "\n"))
		if line := firstSaid(said); line != "" {
			return line
		}
	}
	return ""
}

// firstSaid берёт первую содержательную строку текста: пустые строки и
// разметочные обёртки (заголовок, маркер списка, цитата) снимаются, потому что
// подпись это слова, а не разметка.
func firstSaid(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), "#>*-+ \t"))
		if ln == "" {
			continue
		}
		return truncate(ln, subOrderLimit)
	}
	return ""
}

// subClosed собирает id вызовов, у которых в транскрипте сессии есть ответ.
// Ответ пришёл, значит работа вернулась: это машинный признак закрытой работы,
// и он точнее свежести журнала (субагент, который думает третью минуту, в
// журнал не пишет, а работать не перестал).
func subClosed(data []byte) map[string]bool {
	out := map[string]bool{}
	for _, ln := range strings.Split(string(data), "\n") {
		if !strings.Contains(ln, "tool_result") {
			continue
		}
		var rec struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		var blocks []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				out[b.ToolUseID] = true
			}
		}
	}
	return out
}

// subFresh и subStale это два срока молчания журнала. Первый отделяет работу,
// которая прямо сейчас пишет, от закончившей: субагента продолжают репликой
// (SendMessage), и ответ на первый вызов у него давно есть, а работает он
// дальше, поэтому свежий журнал сильнее ответа. Второй срок закрывает работу,
// у которой ответа не было вовсе: сессию сняли посреди хода, ответа уже не
// будет, и висеть такой работе идущей вечно незачем.
const (
	subFresh = 2 * time.Minute
	subStale = 30 * time.Minute
)

// subWorks собирает работы сессии из её боковых журналов. Знание тут машинное:
// журнал на каждый вызов субагента заводит сам харнес, и показ перестаёт
// зависеть от того, переписал ли агент файл плана. Держаться на его дисциплине
// показ уже не может: работу раздали, файл не тронули, и кольцо врало
// (замечание пользователя, случай третий). Подпись работе даёт заказ из
// мета-файла, а состояние ответ на её вызов в транскрипте: есть ответ, работа
// вернулась, нет ответа и журнал ещё пишется, работа идёт.
func subWorks(path string, closed map[string]bool, now time.Time) []subWork {
	logs := subLogs(path)
	if len(logs) == 0 {
		return nil
	}
	type work struct {
		item subWork
		at   time.Time
	}
	list := make([]work, 0, len(logs))
	for id, log := range logs {
		quiet := subStale + time.Hour
		if fi, err := os.Stat(log.File); err == nil {
			quiet = now.Sub(fi.ModTime())
		}
		// Работа идёт, пока её журнал пишется; ответа на вызов при этом может и
		// не быть (работа ещё не вернулась) и может быть (её продолжают
		// репликой). Молчащая работа закрыта: ответом либо сроком. Метка
		// возврата в мете старше обоих признаков: её пишет тот, кто работу
		// ждал, и знает он точнее всех.
		state := "completed"
		if log.Ended == "" {
			switch {
			case quiet <= subFresh:
				state = "in_progress"
			case !closed[id] && quiet <= subStale:
				state = "in_progress"
			}
		}
		// Подпись работы это заказ, который диспетчер написал субагенту.
		// Коротким его пишет мета-файл вызова, и он же читается лучше всего;
		// нет его там, значит подпись берётся из самого журнала, первой
		// содержательной строкой заказа, и только потом остаётся служебное имя
		// определения, которым работа не называется вовсе.
		label := log.About
		if label == "" {
			label = subOrder(log.File)
		}
		if label == "" {
			label = log.Label
		}
		label = truncate(label, subOrderLimit)
		list = append(list, work{
			item: subWork{item: planItem{Text: label, State: state}, alias: log.Label},
			at:   subStart(log.File)})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	// Закрытое режется, живое остаётся целиком: старая работа это история, а
	// вопрос к кольцу про сейчас.
	done := 0
	for _, w := range list {
		if w.item.item.State == "completed" {
			done++
		}
	}
	skip := done - subDoneLimit
	out := make([]subWork, 0, len(list))
	for _, w := range list {
		if w.item.item.State == "completed" && skip > 0 {
			skip--
			continue
		}
		out = append(out, w.item)
	}
	return out
}

// subWork это работа из бокового журнала: сам пункт и короткая подпись вызова
// рядом. Подпись в паре не для показа, а для сверки с планом: пункт плана и
// подпись вызова пишет один и тот же агент почти одними словами, а заказ
// субагенту он пишет своими, и по одному заказу работа с пунктом не сходится.
type subWork struct {
	item  planItem
	alias string
}

// planKey сводит текст пункта к сравнимому виду: пункт плана и заказ субагента
// пишет один и тот же агент, но пробелы и регистр у них расходятся.
func planKey(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// withSubWorks дополняет план работами из журналов. Пункт, у которого нашлась
// своя работа, берёт состояние у неё: журнал знает про ход работы больше, чем
// список, переписанный руками агента. Работа, которой в плане нет вовсе, встаёт
// наравне с пунктами: раздали и не записали это ровно тот случай, ради которого
// журналы сюда и приехали.
func withSubWorks(plan []planItem, subs []subWork) []planItem {
	if len(subs) == 0 {
		return plan
	}
	out := append([]planItem{}, plan...)
	for _, sub := range subs {
		// Ключей у работы два: подпись вызова и её заказ. Сверяются оба, потому
		// что пункт плана агент пишет теми же словами, что и подпись вызова, а
		// заказ субагенту он пишет своими, и по одному заказу работа с пунктом
		// не сошлась бы и встала бы рядом двойником.
		hit := -1
		for _, key := range []string{planKey(sub.alias), planKey(sub.item.Text)} {
			if key == "" {
				continue
			}
			for i := range out {
				own := planKey(out[i].Text)
				if own == "" {
					continue
				}
				if own == key || strings.Contains(own, key) || strings.Contains(key, own) {
					hit = i
					break
				}
			}
			if hit >= 0 {
				break
			}
		}
		if hit < 0 {
			own := sub.item
			own.Src = planSrcSub
			out = append(out, own)
			continue
		}
		if sub.item.State == "in_progress" {
			out[hit].State = "in_progress"
		}
	}
	return out
}

// planOf сводит три источника: файл плана сессии, последний вызов TodoWrite в
// её транскрипте и работы из боковых журналов субагентов. Первые два это слова
// агента, и побеждает свежий (харнес в обход разрешений TodoWrite не даёт
// вовсе, и файл там единственная дорога, а где инструмент работает, прежний
// способ остаётся живым). Третий это машинный след, и он не спорит со словами,
// а дополняет их: работа, которую агент раздал и не записал, всё равно видна.
func planOf(home, sid, tmux, path string, now time.Time) []planItem {
	var filePlan []planItem
	var fileAt time.Time
	// Имя файла, который лежит на месте, но планом не читается.
	var badFile string
	// Начало сессии берётся у её же журнала первой записью: по нему запасной
	// файл плана отличает свой от чужого, оставшегося под тем же именем
	// tmux-сессии.
	born := subStart(path)
	// Домов тут два: настоящий дом человека, куда правило велит писать план, и
	// дом самого дашборда. У второго экземпляра (POC) они разные, и смотреть
	// надо в оба: свежий файл и побеждает.
	for _, dir := range []string{realHome(), home} {
		if dir == "" {
			continue
		}
		path := planPath(dir, sid)
		plan, at, bad := readPlanFile(path)
		if bad {
			badFile = path
		}
		// Запасной адрес правила плана: сессия без CLAUDE_CODE_SESSION_ID
		// (контур второй подписки) пишет план файлом имени своей tmux-сессии,
		// а файла по sid у неё не заводится вовсе.
		//
		// Имя tmux-сессии это не имя разговора: дашборд переиспользует их
		// (chat-1, chat-2, task-DK-100), и файл по такому имени вполне может
		// быть чужим, оставшимся от прошлого жильца имени. Своим он считается
		// только когда написан после начала самой сессии: раньше её начала
		// написать её план было некому. Файл по sid этой проверки не просит,
		// имя сессии не переиспользуется.
		if plan == nil && tmux != "" {
			own, ownAt, _ := readPlanFile(planPath(dir, tmux))
			if own != nil && (born.IsZero() || !ownAt.Before(born)) {
				plan, at = own, ownAt
			}
		}
		if plan != nil && at.After(fileAt) {
			filePlan, fileAt = plan, at
		}
	}
	// Транскрипт разбирается один раз на оба вопроса: и про план из TodoWrite,
	// и про закрытые вызовы субагентов. Разбор лежит в памяти процесса и
	// дочитывается с прошлого места (feed.go): кольцо ходит сюда каждым тиком.
	tx := transcriptDigest(path)
	subs := subWorks(path, tx.closed, now)
	todoPlan, todoAt := tx.plan, tx.planAt
	said := filePlan
	switch {
	case filePlan == nil:
		said = todoPlan
	case todoPlan != nil && todoAt.After(fileAt):
		said = todoPlan
	}
	// Нечитаемый файл плана говорит о себе сам и только когда сказать больше
	// нечего: слова агента, откуда бы они ни пришли, важнее жалобы на разбор.
	if said == nil && badFile != "" {
		said = []planItem{{
			Text:  "план сессии не разобран: " + filepath.Base(badFile),
			State: "in_progress",
			Src:   planSrcErr,
		}}
	}
	return planOrdered(withSubWorks(said, subs))
}

// planRank это место состояния в списке: сделанное сверху, идущее следом,
// ждущее последним.
func planRank(state string) int {
	switch state {
	case "completed":
		return 0
	case "in_progress":
		return 1
	}
	return 2
}

// planOrdered выстраивает пункты по состоянию, не трогая порядка внутри
// состояния. Порядок тут был случаен: сперва шёл план в том виде, в каком его
// написал агент, а следом работы из журналов, которых в плане не нашлось, и
// закрытое с ждущим стояли вперемешку («работы в кружке выстроены как зря»,
// замечание пользователя). Сортировка устойчивая нарочно: внутри состояния
// хронология уже собрана, пункты плана идут в порядке письма, работы журналов в
// порядке своего начала, и переставлять их второй раз нечем.
func planOrdered(plan []planItem) []planItem {
	if len(plan) < 2 {
		return plan
	}
	out := append([]planItem{}, plan...)
	sort.SliceStable(out, func(i, j int) bool {
		return planRank(out[i].State) < planRank(out[j].State)
	})
	return out
}

// mainSrc это имя источника для самого транскрипта сессии в ключах записей.
const mainSrc = "m"

// srcName зовёт боковой журнал по имени файла без расширения: agent-<id>.
func srcName(file string) string {
	return strings.TrimSuffix(filepath.Base(file), ".jsonl")
}

// markLogout помечает служебные строки про истёкший логин. Лента показывает их
// как есть, пузырём: это и правда сказал агент, и прятать сказанное нельзя.
// Пометка нужна не ленте, а состоянию разговора: панель поднимает по ней плашку
// разлогина и гасит её следующим настоящим ответом, не разбирая английских слов
// у себя.
func markLogout(items []reply) []reply {
	hit := false
	for _, it := range items {
		if it.Role == "assistant" && loginGone(it.Text) {
			hit = true
			break
		}
	}
	if !hit {
		return items
	}
	// Правка идёт по копии по той же причине, что и у подписи диспетчера:
	// разобранный кусок транскрипта уезжает следующему заходу тем же.
	items = append([]reply(nil), items...)
	for i := range items {
		if items[i].Role == "assistant" && loginGone(items[i].Text) {
			items[i].Logout = true
		}
	}
	return items
}

// markLead подписывает ответы главной сессии диспетчерскими. Диспетчер это
// сессия, которая делегирует, и видно это по её же боковым журналам: завела
// хоть один субагентский журнал, значит работу ведут за неё, а её собственные
// ответы это ответы диспетчера (замечание пользователя). Записи субагентов
// остаются просто агентскими, они и так стоят с отступом и заказом. Чужую
// подпись правка не трогает: пришедшее каналом уже названо своим источником.
func markLead(items []reply, lead bool) []reply {
	if !lead {
		return items
	}
	// Правка идёт по копии: разобранный кусок транскрипта лежит в памяти
	// процесса и уезжает следующему заходу тем же (feed.go), а подпись
	// считается на каждом заходе своя.
	items = append([]reply(nil), items...)
	for i := range items {
		if items[i].Role == "assistant" && items[i].Who == "" && items[i].Sub == "" {
			items[i].Who = whoLead
		}
	}
	return items
}

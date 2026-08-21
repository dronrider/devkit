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
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if head.Summary == "" && rec.Type == "summary" {
			head.Summary = firstLine(rec.Summary)
		}
		if head.Branch == "" {
			head.Branch = rec.GitBranch
		}
		if head.First == "" && rec.Type == "user" {
			for _, text := range contentTexts(rec.Message.Content) {
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
	cutoff := s.now().Add(-sessionLiveTTL)
	binds := s.binds()
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
		works = append(works, Work{ID: task, Kind: kind, Title: title, Sect: sect,
			Via: "session", Session: f.ID, Note: note})
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
	// Sel это выделенный человеком кусок постановки, приложенный к реплике
	// контекстом, а SelFile называет файл, откуда он взят. В ленте это
	// свёрнутый блок при пузыре, а агенту оно уезжает префиксом самой реплики:
	// протокола сложнее префикса тут не нужно, это тот же приём, каким Claude
	// Code носит открытый файл (замечание 3 девятого круга POC).
	Sel     string `json:"sel,omitempty"`
	SelFile string `json:"selFile,omitempty"`
	// Shot это путь картинки, приложенной к реплике: агент читает её сам, а
	// лента показывает миниатюру.
	Shot string `json:"shot,omitempty"`
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
	var out []reply
	seq := startSeq
	// prev это метка предыдущей разобранной записи: длительность размышления
	// это расстояние от неё до метки самого размышления, потому что думать
	// агент начинает сразу после того, что было до него.
	var prev time.Time
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
		seq++
		out = append(out, item)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if (rec.IsSidechain && !side) || (rec.Type != "user" && rec.Type != "assistant") {
			continue
		}
		var s string
		if json.Unmarshal(rec.Message.Content, &s) == nil {
			addUser(add, rec.Type, rec.Timestamp, s)
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
				addUser(add, rec.Type, rec.Timestamp, b.Text)
			case "thinking":
				// Текст размышлений едет в ленту (POC ветки poc-chat): прежде
				// сервер выбрасывал его, и на экране стояла метка «размышления
				// свёрнуты», из которой ничего не следовало. Свёрнутым блоком
				// его рисует панель, разворот кликом.
				add(reply{Role: roleThink, Time: rec.Timestamp, Text: b.Thinking})
			case "tool_use":
				add(reply{Role: "tool", Time: rec.Timestamp, Tool: b.Name,
					Note: toolNote(b.Input), About: toolAbout(b.Input),
					Text: toolBody(b.Input), Args: toolArgs(b.Input), ToolID: b.ID})
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

// peerSource подписывает реплику каналом. Своя реплика с дашборда это реплика
// человека: пишет её он, а дашборд только несёт. Чужая сессия называется своим
// именем, иначе непонятно, кто вмешался в разговор.
func peerSource(name string) string {
	// Реплика с дашборда это реплика человека, и подписывать её источником
	// незачем: он и так видит, где написал. Подпись остаётся только у чужой
	// сессии, вмешавшейся в разговор (замечание 17 двенадцатого круга POC).
	if name == "dashboard" {
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

// addUser кладёт в ленту реплику роли user (и текстовый блок ответа агента тем
// же путём). Пузырём человека рисуется только то, что человек написал:
// служебные вставки харнеса уходят отдельными строками, а реплика, кроме них не
// несущая ничего, пузыря не заводит вовсе.
func addUser(add func(reply), role, at, text string) {
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
		shot, rest0 := cutShot(inner)
		sel, file, rest := cutSelection(rest0)
		add(reply{Role: role, Time: at, Text: rest, Note: peerSource(name),
			Sel: sel, SelFile: file, Shot: shot})
		return
	}
	if shot, rest0 := cutShot(text); shot != "" {
		sel, file, rest := cutSelection(rest0)
		add(reply{Role: role, Time: at, Text: rest, Sel: sel, SelFile: file, Shot: shot})
		return
	}
	if sel, file, rest := cutSelection(text); sel != "" {
		add(reply{Role: role, Time: at, Text: rest, Sel: sel, SelFile: file})
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
	add(reply{Role: role, Time: at, Text: said})
}

// skillBodyRe узнаёт простыню скилла: харнес кладёт её репликой роли user и
// начинает строкой с каталогом скилла.
var skillBodyRe = regexp.MustCompile(`\A\s*Base directory for this skill:\s`)

// selWrapRe ловит приложенное к реплике выделение. Блок стоит префиксом, и
// текст внутри едет как есть: кавычки, переносы и разметка человека сохраняются
// целиком, потому что править их некому и незачем.
var selWrapRe = regexp.MustCompile(`(?s)\A<selection file="([^"]*)">\n(.*?)\n</selection>\n?`)

// shotWrapRe ловит приложенную картинку. Стоит она первым префиксом, перед
// выделением: так реплика читается сверху вниз, сначала что показали, потом что
// выделили, потом слова.
var shotWrapRe = regexp.MustCompile(`(?s)\A<screenshot file="([^"]*)">\n.*?\n</screenshot>\n?`)

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
	for _, tag := range svcTags {
		re := svcRe[tag.name]
		for {
			m := re.FindStringSubmatchIndex(out)
			if m == nil {
				break
			}
			body := out[m[2]:m[3]]
			if tag.show {
				notes = append(notes, svcNote(tag, body))
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
	if r.URL.Query().Get("stream") == "1" {
		s.streamSession(w, r, sid, path, keys)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("транскрипт не прочитался: %v", err)})
		return
	}
	items := expandSubs(path, parseReplies(data, 0))
	for _, key := range keys {
		items = saidMerge(items, saidLoad(s.cfg.Home, key))
	}
	total := len(items)
	// «Раньше» режется по устойчивому ключу записи, а не по её месту в ленте:
	// место плывёт от роста боковых журналов, и страницы истории налезали друг
	// на друга. Число тут понимается как место и остаётся ради старых вкладок,
	// открытых до этой правки.
	items = beforeCut(items, r.URL.Query().Get("before"))
	n := intParam(r, "n", repliesDefault, repliesMax)
	if len(items) > n {
		items = items[len(items)-n:]
	}
	// Начало разговора называет сервер: считать его по номеру первой записи
	// клиент больше не может, номера у него не свои.
	start := len(items) > 0 && items[0].Seq == 0
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
	if plan := planOf(s.home(), sid, path); plan != nil {
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
	// Устойчивый ключ дописанной записи продолжает счёт своего файла, а не
	// ленты: mainIdx и idx у бокового журнала это столько записей, сколько в
	// файле уже разобрано.
	mainIdx := 0
	subs := map[string]*tailSrc{}
	// known помнит журналы, заведённые до открытия потока: их хвост уже уехал
	// в ленту, и дочитывать их надо с текущего конца. Журнал, появившийся
	// позже, читается с начала: он пуст в момент появления, и всё в нём новое.
	if data, err := os.ReadFile(path); err == nil {
		data = lastComplete(data)
		items := expandSubs(path, parseReplies(data, 0))
		for _, key := range keys {
			items = saidMerge(items, saidLoad(s.cfg.Home, key))
		}
		seq = len(items)
		counts := map[string]int{}
		for _, item := range items {
			if i := strings.LastIndex(item.Key, ":"); i > 0 {
				counts[item.Key[:i]]++
			}
		}
		mainIdx = counts[mainSrc]
		for _, t := range saidTails {
			t.idx = counts[t.src]
			if fi, err := os.Stat(t.file); err == nil {
				t.off = fi.Size()
			}
		}
		for _, log := range subLogs(path) {
			src := srcName(log.File)
			t := &tailSrc{file: log.File, label: log.Label, src: src, idx: counts[src]}
			if fi, err := os.Stat(log.File); err == nil {
				t.off = fi.Size()
			}
			subs[log.File] = t
		}
		if len(items) > repliesDefault {
			items = items[len(items)-repliesDefault:]
		}
		for _, item := range items {
			sseEvent(w, f, "", marshalReply(item))
		}
		offset = int64(len(data))
	}
	// Пустая лента называется первым событием, как в обычном ответе: молчащий
	// поток неотличим от оборвавшегося. Дострение дальше идёт как обычно.
	if seq == 0 {
		sseEvent(w, f, "note", emptyTranscriptNote)
	}
	stamp := subStamp(path)
	planAt := planStamp(s.home(), sid)
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
			if now := planStamp(s.home(), sid); now != planAt {
				planAt = now
				sendPlan(w, f, planOf(s.home(), sid, path))
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
				var lines []string
				lines, src.off = newLines(src.file, src.off)
				if len(lines) == 0 {
					continue
				}
				for _, item := range parseRepliesOpt([]byte(strings.Join(lines, "\n")), seq, true) {
					item.Sub = src.label
					item.Key = src.src + ":" + strconv.Itoa(src.idx)
					src.idx++
					// Заказ субагенту (первая запись журнала) уже показан
					// карточкой вызова Agent, а пузыря человека в боковом
					// журнале не бывает вовсе: там пишет не он.
					if item.Role == "user" {
						if src.idx == 1 {
							continue
						}
						item.Role = roleNote
					}
					// Тот же дубль на живом дописывании: карточка отправителя
					// уже стоит в ленте.
					if item.Role == roleNote && item.Note == dispatchWord("") {
						continue
					}
					seq = item.Seq + 1
					sseEvent(w, f, "", marshalReply(item))
				}
			}
			var lines []string
			lines, offset = newLines(path, offset)
			if len(lines) == 0 {
				continue
			}
			raw := []byte(strings.Join(lines, "\n"))
			for _, item := range parseReplies(raw, seq) {
				item.Key = mainSrc + ":" + strconv.Itoa(mainIdx)
				mainIdx++
				seq = item.Seq + 1
				sseEvent(w, f, "", marshalReply(item))
			}
			// План сессии живёт своим событием: он приходит целиком и меняет
			// не ленту, а блок плана и кольцо, поэтому в поток реплик его
			// класть нечем.
			if todo, _ := sessionPlan(raw); todo != nil {
				sendPlan(w, f, planOf(s.home(), sid, path))
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
func planStamp(home, sid string) string {
	out := ""
	for _, dir := range []string{realHome(), home} {
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(planPath(dir, sid)); err == nil {
			out += fi.ModTime().String()
		}
	}
	return out
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

// subStamp это метка каталога боковых журналов: по ней видно, что у сессии
// завёлся новый субагент. Дописывание в стоящий файл каталога не трогает, а
// новый файл трогает, и этого хватает.
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
	return fi.ModTime().String() + ":" + strconv.Itoa(len(entries))
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

// sessionBusy решает по транскрипту, работает ли сессия. Поле status реестра
// тут не годится: у сессий vscode харнес его не пишет вовсе (пусто у всех до
// единой), и мера по нему объявляла работающего агента простаивающим. Признаков
// два, хватает любого: журнал писался только что, либо в хвосте висит вызов
// инструмента без ответа, то есть агент сейчас в ходе (долгий ход не пишет в
// журнал минутами).
func sessionBusy(path string, now time.Time) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	from := int64(0)
	if fi.Size() > modelTailLimit {
		from = fi.Size() - modelTailLimit
	}
	if _, err := f.Seek(from, 0); err != nil {
		return false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
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
	if !last.IsZero() && now.Sub(last) < busyFresh {
		return true
	}
	// Незакрытый вызов старше получаса это брошенный хвост закрытого окна, а не
	// работа.
	return len(open) > 0 && !last.IsZero() && now.Sub(last) < 30*time.Minute
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

// subLogs сводит боковые журналы транскрипта в отображение «id вызова -> файл».
// Каталога может не быть вовсе, и это обычный случай: сессия без субагентов.
func subLogs(path string) map[string]struct {
	File  string
	Label string
} {
	out := map[string]struct {
		File  string
		Label string
	}{}
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
		out[m.ToolID] = struct {
			File  string
			Label string
		}{File: log, Label: m.label()}
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
}

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
			Message     struct {
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

// planFileItem это пункт плана, как его пишет агент: текст и состояние. Форма
// нарочно короче той, что приходит от TodoWrite: писать план руками надо в два
// поля, а не в четыре.
type planFileItem struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// readPlanFile читает план сессии из файла. Битый JSON, чужие поля и пустые
// строки молча пропускаются: план это подспорье, и рушить из-за него ленту
// нельзя.
func readPlanFile(path string) ([]planItem, time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}
	}
	var raw []planFileItem
	if json.Unmarshal(data, &raw) != nil {
		return nil, time.Time{}
	}
	out := make([]planItem, 0, len(raw))
	for _, it := range raw {
		if strings.TrimSpace(it.Text) == "" {
			continue
		}
		state := it.State
		switch state {
		case "pending", "in_progress", "completed":
		default:
			state = "pending"
		}
		out = append(out, planItem{Text: truncate(it.Text, 200), State: state})
	}
	if len(out) == 0 {
		return nil, time.Time{}
	}
	return out, fi.ModTime()
}

// planOf сводит два источника плана: файл сессии и последний вызов TodoWrite в
// её транскрипте. Побеждает свежий: харнес в обход разрешений TodoWrite не
// даёт вовсе, и файл там единственная дорога, а где инструмент работает,
// прежний способ остаётся живым.
func planOf(home, sid, path string) []planItem {
	var filePlan []planItem
	var fileAt time.Time
	// Домов тут два: настоящий дом человека, куда правило велит писать план, и
	// дом самого дашборда. У второго экземпляра (POC) они разные, и смотреть
	// надо в оба: свежий файл и побеждает.
	for _, dir := range []string{realHome(), home} {
		if dir == "" {
			continue
		}
		plan, at := readPlanFile(planPath(dir, sid))
		if plan != nil && at.After(fileAt) {
			filePlan, fileAt = plan, at
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return filePlan
	}
	todoPlan, todoAt := sessionPlan(data)
	if filePlan == nil {
		return todoPlan
	}
	if todoPlan == nil || !todoAt.After(fileAt) {
		return filePlan
	}
	return todoPlan
}

// mainSrc это имя источника для самого транскрипта сессии в ключах записей.
const mainSrc = "m"

// srcName зовёт боковой журнал по имени файла без расширения: agent-<id>.
func srcName(file string) string {
	return strings.TrimSuffix(filepath.Base(file), ".jsonl")
}

// expandSubs вплетает записи боковых журналов в ленту по меткам времени.
// Прежде они вставлялись за своим вызовом Task, а журнал, который пишется
// прямо сейчас, целиком уезжал в хвост, и хвостовое окно ленты состояло из
// него одного: у субагента, которого продолжают через SendMessage не первый
// день, записей тысячи, а реплики человека и ответы сессии, шедшие с ними
// вперемешку, оказывались за этой тысячей вверху. Слияние по времени ставит
// каждый кусок работы туда, где он и шёл, а идущая сейчас работа сама
// оказывается в хвосте.
//
// У каждой записи тут появляется устойчивый ключ «источник:номер»: файлы
// только дописываются, и номер записи в своём файле не меняется никогда, а вот
// место в слитой ленте плывёт. По ключу и режется «раньше». Выбирать «тот
// самый живой журнал» тут больше нечего: стрим дочитывает все разом и
// пересматривает их набор на каждом тике.
func expandSubs(path string, items []reply) []reply {
	type keyed struct {
		it  reply
		at  time.Time
		src string
		idx int
	}
	var all []keyed
	// Метка времени есть не у каждой записи, поэтому ключ слияния тянется от
	// предыдущей записи своего же потока. Назад время внутри источника не
	// ходит: перескок метки (у боковых журналов он случается) переставил бы
	// записи одного файла местами, а порядок внутри файла и есть порядок, в
	// котором агент работал.
	push := func(src string, list []reply) {
		var prev time.Time
		for i, it := range list {
			at := prev
			if t, err := time.Parse(time.RFC3339, it.Time); err == nil && t.After(prev) {
				at = t
			}
			prev = at
			it.Key = src + ":" + strconv.Itoa(i)
			all = append(all, keyed{it: it, at: at, src: src, idx: i})
		}
	}
	push(mainSrc, items)
	logs := subLogs(path)
	// Порядок файлов у os.ReadDir свой, а лента должна собираться одинаково от
	// захода к заходу, поэтому журналы обходятся по имени файла.
	ids := make([]string, 0, len(logs))
	for id := range logs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return logs[ids[i]].File < logs[ids[j]].File })
	for _, id := range ids {
		log := logs[id]
		data, err := os.ReadFile(log.File)
		if err != nil {
			continue
		}
		side := parseRepliesOpt(data, 0, true)
		// Человек в боковой журнал не пишет, и пузыря человека там быть не
		// может. Первая запись это заказ субагенту, тот же текст уже стоит
		// карточкой вызова Agent, и вторым разом жёлтой простынёй он читался
		// как реплика человека (жалоба пользователя). Остальные записи роли
		// user это служебное: рамки диспетчера свои правила уже разобрали, а
		// что осталось, идёт служебной строкой, а не пузырём.
		if len(side) > 0 && side[0].Role == "user" {
			side = side[1:]
		}
		// Финальный ответ субагента это последняя его запись с текстом: она и
		// есть отчёт, который харнес пересказывает сводкой в своей вести.
		for i := len(side) - 1; i >= 0; i-- {
			if side[i].Role == "assistant" && strings.TrimSpace(side[i].Text) != "" {
				side[i].Report = true
				break
			}
		}
		kept := side[:0]
		for i := range side {
			side[i].Sub = log.Label
			if side[i].Role == "user" {
				side[i].Role = roleNote
			}
			// Реплика диспетчера субагенту в слитой ленте стоит дважды: своей
			// карточкой SendMessage в транскрипте сессии и рамкой в боковом
			// журнале. Пара у неё есть всегда, и рамка тут чистый дубль
			// (жалоба пользователя по снимку). Встречные рамки остаются: у
			// реплики человека и чужой сессии карточки в ленте нет.
			if side[i].Role == roleNote && side[i].Note == dispatchWord("") {
				continue
			}
			kept = append(kept, side[i])
		}
		side = kept
		push(srcName(log.File), side)
	}
	// Порядок полный и не зависит от того, что ещё лежит в ленте: время, потом
	// источник, потом номер в файле. Иначе один и тот же заезд собирал бы ленту
	// по-разному, и ключ «раньше» указывал бы в разные места.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].at.Equal(all[j].at) {
			return all[i].at.Before(all[j].at)
		}
		if all[i].src != all[j].src {
			return all[i].src < all[j].src
		}
		return all[i].idx < all[j].idx
	})
	// Весть о конце фоновой работы и сам отчёт субагента это одно событие с
	// двух сторон: строка харнеса со сводкой и полный текст в боковом журнале.
	// В ленте они сходятся одним свёрнутым блоком, а сырой отчёт вторым
	// элементом рядом больше не стоит (замечание пользователя по снимку).
	drop := map[int]bool{}
	for i := range all {
		it := &all[i].it
		if it.Role != roleNote || it.Mark != "agent" {
			continue
		}
		pick := -1
		for j := 0; j < i; j++ {
			if all[j].it.Report && !drop[j] {
				pick = j
			}
		}
		if pick < 0 {
			continue
		}
		head, sum := it.Note, it.Text
		if head == "" {
			head, sum = it.Text, ""
		}
		if strings.TrimSpace(sum) != "" {
			head += ": " + sum
		}
		it.Note, it.Text = head, all[pick].it.Text
		drop[pick] = true
	}
	out := make([]reply, 0, len(all))
	for i, k := range all {
		if drop[i] {
			continue
		}
		k.it.Seq = len(out)
		out = append(out, k.it)
		_ = i
	}
	return out
}

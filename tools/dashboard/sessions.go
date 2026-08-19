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
	Live      bool `json:"live,omitempty"`
	path      string
	suffix    string
	stamp     string
	root      string
	mod       time.Time
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

// unknownTaskNote подписывает сессию, чья задача не узнана. Молча выкидывать
// такую сессию из списка нельзя: пропавшая работа неотличима от несделанной.
const unknownTaskNote = "задача не распознана"

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
			// Работа без узнанной задачи подписывается заголовком чата, а не
			// отчётом о том, чего дашборд про неё не узнал: «интерактивная
			// сессия, задача не распознана» не говорило ни о чём. Лестница
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
	// Spent это длительность размышлений в миллисекундах, посчитанная по
	// меткам времени соседних записей транскрипта. Модель отдаёт размышления
	// запечатанными чаще, чем текстом, и тогда сказать про них можно только
	// это: сколько агент думал. Так же подписывает их расширение для vscode
	// («Thought for 5s»).
	Spent int64 `json:"spent,omitempty"`
}

// toolNoteKeys это порядок полей ввода, из которых собирается однострочная
// подпись вызова: первое найденное и есть суть вызова.
var toolNoteKeys = []string{"command", "file_path", "path", "skill", "pattern", "url", "prompt", "id", "query", "description"}

// toolBodyLimit это потолок текста инструмента в ленте: вывод сборки бывает в
// мегабайт, и тянуть его на телефон незачем, а первых тысяч знаков хватает,
// чтобы понять, чем занят агент.
const toolBodyLimit = 4000

// toolBody собирает читаемое тело вызова: команда целиком, а не обрезанная
// подпись, плюс остальные строковые поля ввода.
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
func parseReplies(data []byte, startSeq int) []reply {
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
		if rec.IsSidechain || (rec.Type != "user" && rec.Type != "assistant") {
			continue
		}
		var s string
		if json.Unmarshal(rec.Message.Content, &s) == nil {
			addUser(add, rec.Type, rec.Timestamp, s)
			continue
		}
		var blocks []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    map[string]any  `json:"input"`
			Content  json.RawMessage `json:"content"`
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
					Note: toolNote(b.Input), Text: toolBody(b.Input)})
			case "tool_result":
				// Вывод инструмента показывается как есть, обрезанным по длине:
				// по нему видно, что агент делает, а свёрнутая строка «Bash»
				// про это молчала.
				if text := resultText(b.Content); text != "" {
					add(reply{Role: "toolout", Time: rec.Timestamp, Text: text})
				}
			}
		}
	}
	return out
}

// roleThink это роль размышления в ленте: имя одно на сервер и на панель.
const roleThink = "thinking"

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

// svcNote собирает служебную строку по телу вставки.
func svcNote(tag svcTag, body string) string {
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
				return said + ": " + truncate(strings.Join(strings.Fields(sum), " "), 300)
			}
		}
		return said
	case "command-name":
		return "Команда " + truncate(strings.Join(strings.Fields(body), " "), 80)
	}
	return tag.word
}

// addUser кладёт в ленту реплику роли user (и текстовый блок ответа агента тем
// же путём). Пузырём человека рисуется только то, что человек написал:
// служебные вставки харнеса уходят отдельными строками, а реплика, кроме них не
// несущая ничего, пузыря не заводит вовсе.
func addUser(add func(reply), role, at, text string) {
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
		add(reply{Role: roleNote, Time: at, Text: n})
	}
	if said == "" {
		// Одна служебка без единого слова человека: пустой пузырь тут был бы
		// хуже молчания, а сама служебка уже стоит строкой выше.
		return
	}
	add(reply{Role: role, Time: at, Text: said})
}

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
func splitService(text string) (string, []string) {
	var notes []string
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
	if r.URL.Query().Get("stream") == "1" {
		s.streamSession(w, r, path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("транскрипт не прочитался: %v", err)})
		return
	}
	items := parseReplies(data, 0)
	total := len(items)
	before := intParam(r, "before", total, total)
	if before < total {
		items = items[:before]
	}
	n := intParam(r, "n", repliesDefault, repliesMax)
	if len(items) > n {
		items = items[len(items)-n:]
	}
	if items == nil {
		items = []reply{}
	}
	// Шапка едет вместе с лентой: экран агента открывается и по id сессии, а
	// строки доски у такого захода нет, и заголовок ему брать больше неоткуда
	// (DK-294). Задача тут названа так же, как в списке: узнанная с подписью,
	// чем узнана, либо пустая с подписью «задача не распознана».
	head := s.sessionHeadCached(path, info.stamp)
	info.Branch, info.First = head.Branch, head.First
	info.Tree = info.suffix
	info.Task, info.TaskNote, info.Bound = bindTask(s.binds(), info.ID, info.suffix, head)
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
	resp := map[string]any{"session": sid, "head": info, "total": total, "items": items}
	if total == 0 {
		resp["note"] = emptyTranscriptNote
	}
	writeJSON(w, http.StatusOK, resp)
}

// emptyTranscriptNote называет пустую ленту словами и в обычном ответе, и
// первым событием потока: молчащий стрим неотличим от оборвавшегося
// (замечание ревью DK-219).
const emptyTranscriptNote = "в транскрипте пока нет реплик"

// streamSession шлёт последние реплики и дальше дострение по мере записи:
// каждое событие это одна реплика в JSON. Нумерация продолжается с конца
// файла, разбираются только целые строки.
func (s *server) streamSession(w http.ResponseWriter, r *http.Request, path string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	seq := 0
	if data, err := os.ReadFile(path); err == nil {
		data = lastComplete(data)
		items := parseReplies(data, 0)
		seq = len(items)
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
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			var lines []string
			lines, offset = newLines(path, offset)
			if len(lines) == 0 {
				continue
			}
			for _, item := range parseReplies([]byte(strings.Join(lines, "\n")), seq) {
				seq = item.Seq + 1
				sseEvent(w, f, "", marshalReply(item))
			}
		}
	}
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

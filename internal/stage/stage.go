// Package stage держит живое состояние этапа работы над задачей: чем занята
// задача прямо сейчас и с какого момента. Запись лежит вне репозитория, в
// ~/.devkit/runs, там же где реестр целей: правка рабочего дерева на каждом
// переходе стоила задаче DK-120, где незакоммиченная строка вердикта отбивала
// merge. Пишут запись точки конвейера (agentctl pick, taskctl move), читает её
// дашборд, а при смене статуса накопленные этапы уезжают пакетом в раздел «Ход
// работы» файла задачи. Разбор в docs/tasks/DK-338.md.
package stage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/taskform"
)

// Словарь видов деятельности. Слова из него рисуют экраны доски и задачи
// (DK-355, DK-356), и новое слово к ним не добавляется без нужды: колонка
// экрана узкая, а длинное слово её ломает.
const (
	// Dev это работа исполнителя над кодом задачи, включая грумминговый вердикт:
	// разбирают задачу тем же заходом, что и делают.
	Dev = "разработка"
	// Review это чтение диффа ревьювером.
	Review = "ревью"
	// Verify это прогон сценария проверки чужими руками (DK-642): гоняет его не
	// автор правки, и запись несёт имя прогонявшего.
	Verify = "проверка"
	// Outside это ожидание не нас: проверка на проде, блокер, чужая работа.
	Outside = "снаружи"
	// Ask это вопрос пользователю, на который ждут ответа.
	Ask = "уточнение"
)

// Kinds это словарь целиком, в порядке типичного хода задачи.
var Kinds = []string{Dev, Review, Verify, Outside, Ask}

// Known отвечает, знаком ли вид деятельности. Незнакомое слово отбивается на
// входе: запись с ним доехала бы до экрана пустой колонкой, и разбираться
// пришлось бы уже там.
func Known(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// VerifyNote собирает текст записи прогона сценария. Форма одна на всех
// писателей: по ней ворота закрытия находят прогонявшего, и свободная проза
// вместо неё оставила бы ворота слепыми.
func VerifyNote(by string) string { return "сценарий прогнал " + by }

// verifyRunnerRe находит имя прогонявшего в тексте записи и в строке «Хода
// работы». Хвостовые запятая и точка срезаются отдельно: в имени модели
// бывает точка (glm-5.2), и регулярка с границей по ней резала бы имя.
var verifyRunnerRe = regexp.MustCompile(`сценарий прогнал (\S+)`)

// VerifyRunner достаёт имя прогонявшего сценарий. Второе значение false, когда
// записи прогона в тексте нет.
func VerifyRunner(text string) (string, bool) {
	m := verifyRunnerRe.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return "", false
	}
	name := strings.TrimRight(m[len(m)-1][1], ",.")
	if name == "" {
		return "", false
	}
	return name, true
}

// WorkNote собирает хвост записи с активной работой ревью: ходы и минуты без
// ожидания (DK-731). Формат канон, тот же принцип, что у VerifyNote: писатель
// один, agentctl stage, а читают его ParseWork и review stats из уже
// выгруженной строки «Хода работы».
func WorkNote(turns, minutes int) string {
	return fmt.Sprintf("ходов %d, минут %d", turns, minutes)
}

// workNoteRe ловит хвост ходов и минут в тексте записи или в строке «Хода
// работы»: то же место, где VerifyRunner ищет прогонявшего.
var workNoteRe = regexp.MustCompile(`ходов (\d+), минут (\d+)`)

// ParseWork достаёт ходы и минуты активной работы из текста. Второе значение
// false, когда хвоста в тексте нет: старая запись без него печатается как
// раньше, а свод review stats просто пропускает её в счёте.
func ParseWork(text string) (turns, minutes int, ok bool) {
	m := workNoteRe.FindStringSubmatch(text)
	if m == nil {
		return 0, 0, false
	}
	t, err1 := strconv.Atoi(m[1])
	mm, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return t, mm, true
}

// executorRe находит модель исполнителя в тексте записи этапа разработки:
// pick пишет её как «субагент opus/high по вердикту pick», и имя стоит перед
// косой чертой. Ручная строка без вердикта pick исполнителя не несёт.
var executorRe = regexp.MustCompile(`(\S+)/\S+ по вердикту pick`)

// Executor достаёт модель исполнителя из текста записи этапа. Второе значение
// false, когда вердикта pick в тексте нет.
func Executor(text string) (string, bool) {
	m := executorRe.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return "", false
	}
	return m[len(m)-1][1], true
}

// devLine это ярлык строки «Хода работы» об этапе разработки. По ярлыку этап и
// узнаётся: текст ревью тоже несёт вердикт pick, и без ярлыка ревьювер сошёл бы
// за исполнителя.
const devLine = "- Разработка:"

// LastExecutor находит модель исполнителя последнего этапа «разработка». На
// входе строки раздела «Ход работы» файла задачи и этапы незакрытого пакета из
// ~/.devkit/runs; пакет свежее файла, поэтому спрашивается первым. Второе
// значение false, когда исполнителя не назвал ни один источник.
//
// Спрашивают об этом двое и об одном и том же: ворота закрытия сверяют
// прогонявшего сценарий с автором правки, а подъём прогона после выката решает,
// кому прогон не отдавать.
func LastExecutor(lines []string, pending []Stage) (string, bool) {
	for i := len(pending) - 1; i >= 0; i-- {
		if pending[i].Kind != Dev {
			continue
		}
		if name, ok := Executor(pending[i].Note); ok {
			return name, true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), devLine) {
			continue
		}
		if name, ok := Executor(lines[i]); ok {
			return name, true
		}
	}
	return "", false
}

// LastVerifyRunner находит последнюю запись прогона сценария теми же двумя
// источниками и в том же порядке.
func LastVerifyRunner(lines []string, pending []Stage) (string, bool) {
	for i := len(pending) - 1; i >= 0; i-- {
		if pending[i].Kind != Verify {
			continue
		}
		if name, ok := VerifyRunner(pending[i].Note); ok {
			return name, true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := VerifyRunner(lines[i]); ok {
			return name, true
		}
	}
	return "", false
}

// NeedsSession отвечает, обязана ли за этапом стоять живая сессия агента.
// Разработка, ревью и уточнение ведутся сессией, и запись без неё это
// оборванный этап, тот же случай, что gone у признака Run. Ожидание снаружи
// сессии не требует по смыслу: там ждут человека, и требовать живого агента
// значило бы гасить единственный честный этап.
func NeedsSession(kind string) bool { return kind != Outside }

// Stamp это формат времени в записи: секунды без зоны, как у реестра целей.
const Stamp = "2006-01-02T15:04:05"

// LineStamp это формат момента в строке «Хода работы»: минуты, читает их и
// человек. Им пишет Lines и по нему же читает ParseLine.
const LineStamp = "2006-01-02 15:04"

// Stage это один этап: вид деятельности, момент начала и текст записи, который
// уедет в «Ход работы». Текст собирает тот, кто этап открыл: pick знает про
// модель, маппинг и квоту, а taskctl про статус.
//
// Session это разговор, открывший этап. Поля этого у записи не было, и потому
// от неё нельзя было дойти до чата, который задачу ведёт: экран знал, что идёт
// разработка, а спросить исполнителя было негде (предмет DK-716). ID приезжает
// из окружения самой сессии, и пусто оно там, где этап открыли вне харнеса:
// рукой из терминала, скриптом, стендом.
type Stage struct {
	Kind    string
	Start   time.Time
	Note    string
	Session string
}

// Record это запись задачи целиком: шапка и накопленные этапы в порядке
// открытия. Живой этап последний.
type Record struct {
	ID     string
	Root   string
	Stages []Stage
}

// Live отдаёт живой этап записи.
func (r Record) Live() (Stage, bool) {
	if len(r.Stages) == 0 {
		return Stage{}, false
	}
	return r.Stages[len(r.Stages)-1], true
}

// Elapsed отдаёт время с момента Start последнего этапа с совпавшим Kind
// (LLD DK-503, решение 1): лимит жизненного цикла агента меряется временем,
// а не числом обращений или объёмом контекста. Ищет от конца записи, а не
// берёт только живой этап, потому что вызывающий (taskctl elapsed)
// спрашивает конкретный вид деятельности, а живым к моменту вопроса может
// стоять уже другой этап. Второе значение false, если этапа такого вида в
// записи нет вовсе.
func Elapsed(rec Record, kind string, now time.Time) (time.Duration, bool) {
	for i := len(rec.Stages) - 1; i >= 0; i-- {
		if rec.Stages[i].Kind == kind {
			return now.Sub(rec.Stages[i].Start), true
		}
	}
	return 0, false
}

// dirName это каталог записей внутри ~/.devkit.
const dirName = "runs"

// Home отдаёт домашнюю директорию. Отдельной функцией, чтобы вызывающие не
// разбирались с ошибкой os.UserHomeDir каждый по-своему: без дома записи не
// ведутся, и это не повод ронять команду.
func Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// Dir это каталог записей: ~/.devkit/runs.
func Dir(home string) string { return filepath.Join(home, ".devkit", dirName) }

// Slug делает из пути корня имя, годное в имя файла: два проекта с одинаковым
// именем директории не должны занимать одну запись, как и в реестре целей.
func Slug(root string) string {
	var b strings.Builder
	for _, r := range root {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return strings.Trim(b.String(), "-")
}

// Path это путь записи задачи.
func Path(home, root, id string) string {
	return filepath.Join(Dir(home), id+"-"+Slug(root)+".run")
}

// MainRoot приводит корень к основному чекауту. Этап разработки открывают из
// дерева задачи (pick зовут с -C <worktree>), а закрывает его смена статуса из
// основного чекаута, и без приведения это были бы две разные записи: имя файла
// и поле root считаются от пути, а у линкованного дерева путь свой. Приведение
// идёт через git-common-dir, как linkedWorktree в taskctl. Вне git-дерева и при
// недоступном git возвращается то, что дали: временные корни тестов и проекты
// без git обязаны работать по-прежнему.
func MainRoot(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return root
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return root
	}
	main := filepath.Dir(common)
	if main == "" || main == "." {
		return root
	}
	return main
}

// Open открывает этап: дописывает его в запись задачи, заводя её при первом
// этапе. Разговор, открывший этап, записывается сам собой: ID сессии лежит в
// окружении харнеса, и спрашивать его у вызывающего значило бы протянуть одно
// и то же значение через десяток точек конвейера. Открытый этап закрывает следующий за ним, а весь пакет закрывает
// смена статуса. Провал записи не роняет вызывающую команду, как и провал
// журнала запусков: без отметки конвейер работает, просто молча.
func Open(home, root, id, kind, note string, now time.Time) error {
	if home == "" {
		return fmt.Errorf("домашней директории не видно, этап записывать некуда")
	}
	if !Known(kind) {
		return fmt.Errorf("неизвестный вид деятельности %q, жду один из: %s", kind, strings.Join(Kinds, ", "))
	}
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		return err
	}
	path := Path(home, root, id)
	rec, err := Load(path)
	if err != nil {
		return err
	}
	rec.ID, rec.Root = id, root
	rec.Stages = append(rec.Stages, Stage{Kind: kind, Start: now, Note: note, Session: sessions.Own()})
	return os.WriteFile(path, []byte(body(rec)), 0o644)
}

// body собирает запись в текст. Формат тот же, что у остальных локальных файлов
// devkit: строки «ключ = значение», решётка комментарий. Этапов в записи много,
// поэтому ключ «этап» повторяется, а поля этапа разделены вертикальной чертой.
func body(rec Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# этапы задачи %s: пишет конвейер devkit, читает дашборд\n", rec.ID)
	fmt.Fprintf(&b, "id = %s\n", rec.ID)
	fmt.Fprintf(&b, "root = %s\n", rec.Root)
	for _, s := range rec.Stages {
		fmt.Fprintf(&b, "этап = %s | %s | %s | %s\n",
			s.Kind, s.Start.Format(Stamp), clean(s.Note), clean(s.Session))
	}
	return b.String()
}

// clean убирает из текста записи то, что развалило бы разбор: разделитель полей
// и перевод строки. Текст сюда приезжает собранным (вердикт pick несёт и
// причину сдвига, и снимок квоты), и молча резать его на первой же черте хуже,
// чем заменить её пробелом.
func clean(s string) string {
	s = strings.ReplaceAll(s, "|", "/")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// Load разбирает запись. Отсутствие файла это пустая запись без ошибки: этап
// открывают и по задаче, которой ещё не касались.
func Load(path string) (Record, error) {
	rec := Record{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rec, nil
		}
		return rec, err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "id":
			rec.ID = val
		case "root":
			rec.Root = val
		case "этап":
			if s, ok := parseStage(val); ok {
				rec.Stages = append(rec.Stages, s)
			}
		}
	}
	return rec, nil
}

// parseStage разбирает строку этапа. Строка с неизвестным видом или битым
// временем пропускается: запись правил не только текущий инструмент, и ронять
// из-за одной строки чтение всей задачи незачем. Четвёртое поле, разговор,
// приехало с DK-716, и записи без него читаются по-прежнему: на диске лежат
// пакеты, открытые прежней сборкой, и терять их из-за нового поля нельзя.
func parseStage(val string) (Stage, bool) {
	parts := strings.SplitN(val, "|", 4)
	if len(parts) < 2 {
		return Stage{}, false
	}
	kind := strings.TrimSpace(parts[0])
	if !Known(kind) {
		return Stage{}, false
	}
	start, err := time.ParseInLocation(Stamp, strings.TrimSpace(parts[1]), time.Local)
	if err != nil {
		return Stage{}, false
	}
	note, sess := "", ""
	if len(parts) >= 3 {
		note = strings.TrimSpace(parts[2])
	}
	if len(parts) == 4 {
		sess = strings.TrimSpace(parts[3])
	}
	return Stage{Kind: kind, Start: start, Note: note, Session: sess}, true
}

// Flush забирает накопленные этапы и убирает запись: пакет уезжает в файл
// задачи, и оставленная запись выдавала бы уже закрытый этап за живой. Пустой
// возврат значит, что записывать нечего.
func Flush(home, root, id string) ([]Stage, error) {
	if home == "" {
		return nil, nil
	}
	path := Path(home, root, id)
	rec, err := Load(path)
	if err != nil {
		return nil, err
	}
	if len(rec.Stages) == 0 {
		os.Remove(path)
		return nil, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return rec.Stages, nil
}

// List собирает записи всех задач проекта: дашборду нужна карта «задача ->
// живой этап», а записи на машине общие для всех проектов и разделены полем
// root. Нечитаемый каталог это пустой список, экран доски из-за него не пустеет.
func List(home, root string) []Record {
	paths, err := filepath.Glob(filepath.Join(Dir(home), "*.run"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)
	var out []Record
	for _, p := range paths {
		rec, err := Load(p)
		if err != nil || rec.ID == "" {
			continue
		}
		if filepath.Clean(rec.Root) != filepath.Clean(root) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Lines разворачивает пакет этапов в строки раздела «Ход работы». Вид
// деятельности идёт с заглавной ярлыком строки, следом текст записи, дальше
// дата и часы этапа: по ним видно не только чем задача занималась, но и сколько
// это заняло. Конец этапа это начало следующего, у последнего это момент
// записи пакета.
func Lines(stages []Stage, end time.Time) []string {
	out := make([]string, 0, len(stages))
	for i, s := range stages {
		fin := end
		if i+1 < len(stages) {
			fin = stages[i+1].Start
		}
		label := s.Kind
		if r := []rune(s.Kind); len(r) > 0 {
			label = strings.ToUpper(string(r[0])) + string(r[1:])
		}
		span := s.Start.Format("15:04")
		if fin.After(s.Start) {
			span += "-" + fin.Format("15:04")
		}
		note := ""
		if s.Note != "" {
			note = s.Note + ", "
		}
		out = append(out, fmt.Sprintf("- %s: %s%s %s.", label, note, s.Start.Format("2006-01-02"), span))
	}
	return out
}

// spanRe ловит хвост строки «Хода работы»: дату и часы этапа, которые собрал
// Lines. Конца может не быть, у этапа нулевой длины Lines его не пишет.
var spanRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})(?:-(\d{2}:\d{2}))?\.?\s*$`)

// ParseLine читает строку раздела «Ход работы» обратно в этап и его
// длительность: сводке цели нужно знать, куда ушло время закрытых задач, а
// живая запись к тому моменту уже уехала в файл задачи и стёрта Flush. Вид
// берётся по словарю Kinds, так что пятое слово словаря читается наравне с
// четырьмя нынешними, а строка с чужим ярлыком или без часов это не находка,
// а обычная проза раздела, и второе значение у неё false.
func ParseLine(ln string) (Stage, time.Duration, bool) {
	t := strings.TrimSpace(ln)
	t = strings.TrimPrefix(t, "- ")
	label, rest, ok := strings.Cut(t, ": ")
	if !ok {
		return Stage{}, 0, false
	}
	kind := strings.ToLower(strings.TrimSpace(label))
	if !Known(kind) {
		return Stage{}, 0, false
	}
	m := spanRe.FindStringSubmatch(rest)
	if m == nil {
		return Stage{}, 0, false
	}
	start, err := time.ParseInLocation(LineStamp, m[1]+" "+m[2], time.Local)
	if err != nil {
		return Stage{}, 0, false
	}
	note := strings.TrimSuffix(strings.TrimSpace(rest[:len(rest)-len(m[0])]), ",")
	span := time.Duration(0)
	if m[3] != "" {
		fin, err := time.ParseInLocation(LineStamp, m[1]+" "+m[3], time.Local)
		if err != nil {
			return Stage{}, 0, false
		}
		// Часы конца меньше часов начала значит этап перешёл через полночь:
		// дату Lines пишет одну, начала, и без переноса такой этап дал бы
		// отрицательную длительность и съел бы чужое время в сводке.
		if fin.Before(start) {
			fin = fin.Add(24 * time.Hour)
		}
		span = fin.Sub(start)
	}
	return Stage{Kind: kind, Start: start, Note: strings.TrimSpace(note)}, span, true
}

// FenceMask и InsertIntoSection живут в пакете taskform вместе с порядком
// разделов формы; здесь остались обёртки под прежние имена, чтобы читатели
// файла задачи звали одно место.
func FenceMask(lines []string) ([]bool, int) { return taskform.FenceMask(lines) }

// InsertIntoSection дописывает строки в конец названного раздела файла задачи
// или цели, а отсутствующий раздел заводит на месте по форме TASKFORM.md.
func InsertIntoSection(content, heading string, lines ...string) string {
	return taskform.InsertIntoSection(content, heading, lines...)
}

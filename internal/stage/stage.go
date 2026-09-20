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

// Словарь видов деятельности (DK-911, принят развилкой «словарь этапов» цели
// DK-909). Восемь этапов работы и три ожидания. Слова из него рисуют экраны
// доски и задачи (DK-355, DK-356), и новое слово к ним не добавляется без
// нужды: колонка экрана узкая, а длинное слово её ломает. Этап ставит
// инструмент, который начинает деятельность: хук спавна субагента, shipctl,
// taskctl на парковке. Из рук диспетчера ставится одна проверка.
const (
	// Setup это разбор записи до кода: груминг черновика, нарезка цели.
	Setup = "постановка"
	// Dev это работа исполнителя над кодом задачи, включая грумминговый вердикт:
	// разбирают задачу тем же заходом, что и делают.
	Dev = "разработка"
	// Proof это вычитка прозы субагентом со свежим контекстом.
	Proof = "вычитка"
	// Review это чтение диффа ревьювером.
	Review = "ревью"
	// Rework это исполнитель, поднятый заново после ревью с замечаниями.
	Rework = "доработка"
	// Merge это слияние ветки задачи в main командой shipctl merge.
	Merge = "слияние"
	// Deploy это выкат поезда командой shipctl ship.
	Deploy = "выкат"
	// Verify это прогон сценария проверки чужими руками (DK-642): гоняет его не
	// автор правки, и запись несёт имя прогонявшего.
	Verify = "проверка"
	// WaitHuman это вопрос человеку либо приёмка глазами: ответа ждут от него.
	WaitHuman = "ждёт человека"
	// WaitEvent это машинное событие: слияние соседа, закрытие предпосылки,
	// таймер agentctl wait.
	WaitEvent = "ждёт события"
	// WaitQueue это занятый замок конвейера: merge или ship упёрлись в чужой
	// заход и повторятся.
	WaitQueue = "ждёт очереди"

	// Outside и Ask это слова прежнего словаря. Они остаются только для
	// чтения старых записей и строк «Хода работы»: Canon переводит их в
	// ожидания, а Open их не принимает.
	Outside = "снаружи"
	Ask     = "уточнение"
)

// Kinds это словарь целиком, в порядке типичного хода задачи.
var Kinds = []string{Setup, Dev, Proof, Review, Rework, Merge, Deploy, Verify, WaitHuman, WaitEvent, WaitQueue}

// works это этапы работы, waits это ожидания. Деление читают elapsed, pilot,
// ворота закрытия и дашборд, и держится оно тут одним местом, а не сравнением
// со словом в каждом читателе.
var (
	works = []string{Setup, Dev, Proof, Review, Rework, Merge, Deploy, Verify}
	waits = []string{WaitHuman, WaitEvent, WaitQueue}
	// execs это работа исполнителя над кодом: по ней меряется срок жизни
	// субагента (elapsed), считаются заходы (pilot) и ищется автор правки у
	// ворот закрытия.
	execs = []string{Dev, Rework}
)

func among(kind string, set []string) bool {
	for _, k := range set {
		if k == kind {
			return true
		}
	}
	return false
}

// IsWork отвечает, этап ли это работы: за ним стоит сессия, которая что-то
// делает.
func IsWork(kind string) bool { return among(kind, works) }

// IsWait отвечает, ожидание ли это. Слова прежнего словаря «снаружи» и
// «уточнение» тоже ожидания: старые пакеты «Хода работы» читаются наравне с
// новыми.
func IsWait(kind string) bool { return among(kind, waits) || Legacy(kind) }

// IsExec отвечает, работа ли это исполнителя над кодом: разработка либо
// доработка после ревью.
func IsExec(kind string) bool { return among(kind, execs) }

// Legacy отвечает, слово ли это прежнего словаря.
func Legacy(kind string) bool { return kind == Outside || kind == Ask }

// blockedEventClasses это разряды парковки, за которыми ждут машинного
// события, а не человека: слияние соседа и закрытие предпосылки (taskctl
// wake). По ним старое «снаружи» из Blocked читается ожиданием события.
var blockedEventClasses = []string{"слияние", "закрытие"}

// Canon приводит вид к нынешнему словарю. Слова прежнего словаря переводятся
// в ожидания по тексту записи: «уточнение» это вопрос человеку, «снаружи» из
// Blocked с машинным разрядом слияния или закрытия это событие, остальное
// «снаружи» это человек (проверка после выката, блокер, чужая работа).
// Нынешнее слово возвращается как есть.
func Canon(kind, note string) string {
	switch kind {
	case Ask:
		return WaitHuman
	case Outside:
		reason := strings.TrimPrefix(note, "блок: ")
		first, _, _ := strings.Cut(reason, ":")
		if among(strings.TrimSpace(first), blockedEventClasses) {
			return WaitEvent
		}
		return WaitHuman
	}
	return kind
}

// Known отвечает, знаком ли вид деятельности, включая слова прежнего словаря.
// Незнакомое слово отбивается на входе: запись с ним доехала бы до экрана
// пустой колонкой, и разбираться пришлось бы уже там.
func Known(kind string) bool { return among(kind, Kinds) || Legacy(kind) }

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

// executorRe находит модель исполнителя в тексте записи этапа разработки. Хук
// спавна пишет её как «субагент opus/high по определению exec-high», прежний
// pick --record писал «субагент opus/high по вердикту pick», и в обоих имя
// стоит перед косой чертой. Ручная строка без того и другого исполнителя не
// несёт.
var executorRe = regexp.MustCompile(`(\S+)/\S+ по (?:вердикту pick|определению)`)

// Executor достаёт модель исполнителя из текста записи этапа. Второе значение
// false, когда вердикта pick в тексте нет.
func Executor(text string) (string, bool) {
	m := executorRe.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return "", false
	}
	return m[len(m)-1][1], true
}

// execLines это ярлыки строк «Хода работы» об этапах исполнителя. По ярлыку
// этап и узнаётся: текст ревью тоже несёт определение и модель, и без ярлыка
// ревьювер сошёл бы за исполнителя.
var execLines = []string{"- Разработка:", "- Доработка:"}

func execLine(ln string) bool {
	ln = strings.TrimSpace(ln)
	for _, l := range execLines {
		if strings.HasPrefix(ln, l) {
			return true
		}
	}
	return false
}

// LastExecutor находит модель исполнителя последнего этапа работы над кодом
// (разработка либо доработка). На входе строки раздела «Ход работы» файла
// задачи и этапы незакрытого пакета из ~/.devkit/runs; пакет свежее файла,
// поэтому спрашивается первым. Второе значение false, когда исполнителя не
// назвал ни один источник.
//
// Спрашивают об этом двое и об одном и том же: ворота закрытия сверяют
// прогонявшего сценарий с автором правки, а подъём прогона после выката решает,
// кому прогон не отдавать.
func LastExecutor(lines []string, pending []Stage) (string, bool) {
	for i := len(pending) - 1; i >= 0; i-- {
		if !IsExec(pending[i].Kind) {
			continue
		}
		if name, ok := Executor(pending[i].Note); ok {
			return name, true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !execLine(lines[i]) {
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
// Этап работы ведётся сессией, и запись без неё это оборванный этап, тот же
// случай, что gone у признака Run. Ожидание сессии не требует по смыслу: там
// ждут человека, событие или очередь, и требовать живого агента значило бы
// гасить единственный честный этап.
func NeedsSession(kind string) bool { return IsWork(kind) }

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
//
// End это конец этапа, когда его закрыл сам писатель: shipctl по концу
// слияния и выката, хук по концу субагента. Нулевое время значит, что этап
// закроет следующий за ним либо смена статуса. Без своего конца выкат висел бы
// живым этапом у строки в Check до самого закрытия (DK-911).
//
// Work это номер работы субагента у этапов, которые открыл хук спавна: по нему
// свод расхода находит транскрипт субагента (DK-912).
type Stage struct {
	Kind    string
	Start   time.Time
	Note    string
	Session string
	End     time.Time
	Work    string
}

// Ended отвечает, закрыт ли этап своим писателем.
func (s Stage) Ended() bool { return !s.End.IsZero() }

// Record это запись задачи целиком: шапка и накопленные этапы в порядке
// открытия. Живой этап последний.
type Record struct {
	ID     string
	Root   string
	Stages []Stage
}

// Live отдаёт живой этап записи: последний, пока его не закрыл писатель.
// Закрытый последний этап это промежуток между деятельностями, живого этапа у
// задачи в нём нет.
func (r Record) Live() (Stage, bool) {
	if len(r.Stages) == 0 || r.Stages[len(r.Stages)-1].Ended() {
		return Stage{}, false
	}
	return r.Stages[len(r.Stages)-1], true
}

// LastOf отдаёт последний этап записи, чей вид подошёл под предикат. Ищет от
// конца, а не берёт только живой этап: спрашивающий (taskctl elapsed) хочет
// свой вид деятельности, а живым к моменту вопроса может стоять другой.
func LastOf(rec Record, match func(string) bool) (Stage, bool) {
	for i := len(rec.Stages) - 1; i >= 0; i-- {
		if match(rec.Stages[i].Kind) {
			return rec.Stages[i], true
		}
	}
	return Stage{}, false
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
	return Put(home, root, id, Stage{Kind: kind, Start: now, Note: note})
}

// Put дописывает этап в запись целиком, с концом и номером работы, когда они у
// писателя есть: хук спавна синхронного субагента узнаёт об этапе по его концу
// и кладёт сразу закрытый. Разговор берётся из окружения, когда писатель его
// не назвал. Слова прежнего словаря не принимаются: старые записи читаются, а
// новые пишутся нынешними словами.
func Put(home, root, id string, s Stage) error {
	if home == "" {
		return fmt.Errorf("домашней директории не видно, этап записывать некуда")
	}
	if Legacy(s.Kind) {
		return fmt.Errorf("вид %q остался в прежнем словаре, ожидание пишется одним из: %s", s.Kind, strings.Join(waits, ", "))
	}
	if !Known(s.Kind) {
		return fmt.Errorf("неизвестный вид деятельности %q, жду один из: %s", s.Kind, strings.Join(Kinds, ", "))
	}
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		return err
	}
	path := Path(home, root, id)
	rec, err := Load(path)
	if err != nil {
		return err
	}
	if s.Session == "" {
		s.Session = sessions.Own()
	}
	rec.ID, rec.Root = id, root
	rec.Stages = append(rec.Stages, s)
	return os.WriteFile(path, []byte(body(rec)), 0o644)
}

// Close закрывает живой этап названного вида: ставит ему конец и дописывает
// хвост к тексту записи (ходы и минуты ревью, итог слияния). Живой этап
// другого вида не трогается: пока слияние шло, смена статуса могла увезти
// пакет и открыть ожидание, и закрывать его за слияние нельзя. Второе значение
// говорит, был ли этап закрыт.
func Close(home, root, id, kind string, now time.Time, extra string) (bool, error) {
	if home == "" {
		return false, nil
	}
	path := Path(home, root, id)
	rec, err := Load(path)
	if err != nil {
		return false, err
	}
	live, ok := rec.Live()
	if !ok || live.Kind != kind {
		return false, nil
	}
	last := &rec.Stages[len(rec.Stages)-1]
	last.End = now
	if extra != "" {
		if last.Note != "" {
			last.Note += ", "
		}
		last.Note += extra
	}
	return true, os.WriteFile(path, []byte(body(rec)), 0o644)
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
		end := ""
		if s.Ended() {
			end = s.End.Format(Stamp)
		}
		fmt.Fprintf(&b, "этап = %s | %s | %s | %s | %s | %s\n",
			s.Kind, s.Start.Format(Stamp), clean(s.Note), clean(s.Session), end, clean(s.Work))
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
// приехало с DK-716, пятое и шестое, конец и работа, с DK-911, и записи без
// них читаются по-прежнему: на диске лежат пакеты, открытые прежней сборкой,
// и терять их из-за нового поля нельзя. Слово прежнего словаря приводится к
// ожиданию тут же, читатели видят один словарь.
func parseStage(val string) (Stage, bool) {
	parts := strings.Split(val, "|")
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
	field := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i])
		}
		return ""
	}
	s := Stage{Kind: kind, Start: start, Note: field(2), Session: field(3), Work: field(5)}
	s.Kind = Canon(kind, s.Note)
	if e := field(4); e != "" {
		if end, err := time.ParseInLocation(Stamp, e, time.Local); err == nil {
			s.End = end
		}
	}
	return s, true
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
// это заняло. Конец этапа это его собственный конец, когда писатель его
// закрыл, иначе начало следующего, а у последнего момент записи пакета.
func Lines(stages []Stage, end time.Time) []string {
	out := make([]string, 0, len(stages))
	for i, s := range stages {
		fin := end
		if i+1 < len(stages) {
			fin = stages[i+1].Start
		}
		if s.Ended() {
			fin = s.End
		}
		label := Label(s.Kind)
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

// Label это ярлык строки «Хода работы» у вида: слово словаря с заглавной
// буквы. Тот же перечень держат сторож прозы (hooks/check-prose.py,
// STAGE_LABELS) и перечень машинных строк вычитки, по нему обе стороны узнают
// строку этапа и не считают её прозой.
func Label(kind string) string {
	r := []rune(kind)
	if len(r) == 0 {
		return kind
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// spanRe ловит хвост строки «Хода работы»: дату и часы этапа, которые собрал
// Lines. Конца может не быть, у этапа нулевой длины Lines его не пишет.
var spanRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})(?:-(\d{2}:\d{2}))?\.?\s*$`)

// ParseLine читает строку раздела «Ход работы» обратно в этап и его
// длительность: сводке цели нужно знать, куда ушло время закрытых задач, а
// живая запись к тому моменту уже уехала в файл задачи и стёрта Flush. Вид
// берётся по словарю Kinds вместе со словами прежнего словаря, и те
// приводятся к ожиданиям тут же: старые пакеты со «снаружи» и «уточнением»
// читаются наравне с новыми. Строка с чужим ярлыком или без часов это не
// находка, а обычная проза раздела, и второе значение у неё false.
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
	note = strings.TrimSpace(note)
	return Stage{Kind: Canon(kind, note), Start: start, Note: note}, span, true
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

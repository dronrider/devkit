// Package chat держит носитель живого разговора: входы .devkit/chat рабочего
// дерева, по которым реплика человека доезжает до идущей сессии, и признак
// ожидания, которым сессия говорит, что ждёт ответа прямо сейчас. Пишет реплики
// дашборд, ждёт их taskctl, разносит по ходам подхват hooks/chat-in.py, и
// разбор строки у всех троих обязан быть один: формат тут держит и адресата, и
// подпись, и срок ожидания, а разошедшиеся копии стоили бы украденной реплики.
// Разбор целиком в docs/lld/DK-430-task-chat.md, решения 2 и 3.
package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Раскладка носителя: каталог входов в дереве работы, расширение входа, признак
// ожидания и замок рядом с ним (DK-440).
const (
	Dir        = "chat"
	Suffix     = ".in"
	AskSuffix  = ".ask"
	LockSuffix = ".lock"
)

// Stamp это формат времени в признаке ожидания, тот же, что у реестра целей и
// у подхвата (STAMP в hooks/chat-in.py): срок читается обоими языками.
const Stamp = "2006-01-02T15:04:05"

// Подпись дашборда и адресат внутри строки разговора: «<стамп>, сессии <ID>, из
// дашборда: текст». Слова тут те же, что разбирает подхват (FROM_DASHBOARD и
// TO_SESSION в hooks/chat-in.py), и менять их можно только обеими сторонами.
const (
	fromDashboard = ", из дашборда: "
	toSession     = ", сессии "
	lineStamp     = "2006-01-02 15:04"
)

// Root это каталог входов дерева, Path вход разговора, AskPath признак его
// ожидания.
func Root(tree string) string          { return filepath.Join(tree, ".devkit", Dir) }
func Path(tree, name string) string    { return filepath.Join(Root(tree), name+Suffix) }
func AskPath(tree, name string) string { return filepath.Join(Root(tree), name+AskSuffix) }

// TaskName называет разговор записи доски по её ID. Имя живёт дольше сессии:
// task-<ID> переживает парковку задачи, и ответ достаётся той сессии, что
// задачу продолжит. Черновик зовётся тем же именем, а не своим draft-<ID>:
// панель дашборда кладёт ответ человека именно сюда (sessionChatName), и
// второй адрес был бы входом, куда никто не пишет (живой случай DK-517).
func TaskName(id string) string { return "task-" + id }

// Line это строка разговора с адресатом: реплика написана сессии sid и доедет
// только до неё.
func Line(at time.Time, sid, text string) string {
	return at.Format(lineStamp) + toSession + sid + fromDashboard + text
}

// TaskLine это безадресная строка разговора задачи. Адрес у реплики это адресат
// разговора, а не состояние собеседника (LLD DK-430, решение 2): безадресную
// строку берёт любая сессия дерева, поэтому ответ припаркованной задаче
// достаётся той, что задачу продолжит, а сторожок считает ответом только такую.
func TaskLine(at time.Time, text string) string {
	return at.Format(lineStamp) + fromDashboard + text
}

// Addressee называет сессию, которой реплика адресована; пустая строка значит
// «любой сессии дерева».
func Addressee(line string) string {
	_, rest, ok := strings.Cut(line, toSession)
	if !ok {
		return ""
	}
	head, _, _ := strings.Cut(rest, ",")
	return strings.TrimSpace(head)
}

// Said достаёт текст реплики из строки: подпись дашборда отрезается, а
// рукописная строка читается как есть.
func Said(line string) string {
	if _, after, ok := strings.Cut(line, fromDashboard); ok {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(line)
}

// ForMe отвечает, достаётся ли строка сессии sid. Безадресная достаётся всем.
func ForMe(line, sid string) bool {
	to := Addressee(line)
	return to == "" || to == sid
}

// LockWait это срок ожидания замка разговора. Замок держит подхват или соседняя
// отправка на секунды, и подождать их дешевле, чем отбить реплику.
const LockWait = 2 * time.Second

// ErrLocked значит, что разговор держит соседний прогон: строки расходятся под
// замком, и вызвавшему остаётся повторить.
var ErrLocked = errors.New("разговор держит соседний прогон")

// Lock берёт flock файла замка разговора, тот же замок, каким собирается
// подхват (take_chat_lock в hooks/chat-in.py): без него отправка на пустом
// месте и доставка, убирающая вход, теряли бы строки друг друга.
func Lock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(LockWait)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, ErrLocked
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ReadLines читает лежащие реплики; пустой файл и отсутствие входа это одно и
// то же «сказать нечего».
func ReadLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// writeLines переписывает вход оставшимися строками, а пустой убирает: лежащая
// строка всегда непрочитанная, отметок доставки у входа нет. Временный файл с
// заменой ради того же, чего ради него у подхвата: читатель на половине записи
// видит либо старое, либо новое, но не огрызок.
func writeLines(path string, lines []string) error {
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Put кладёт готовую строку во вход разговора name дерева tree под тем же
// замком, каким собирается подхват. Непустой возврат lying это лежащая копия
// той же реплики: второй строки повтор не заводит, иначе сессия прочитала бы
// сказанное дважды. Писатель тут один на все ручки: правило адресности живёт в
// строке, а не в записи файла, и разойтись двум записям нельзя.
func Put(tree, name, text, line string) (lying string, err error) {
	dir := Root(tree)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("каталог разговоров не создался: %v", err)
	}
	lock, err := Lock(filepath.Join(dir, name+LockSuffix))
	if err != nil {
		return "", err
	}
	defer lock.Close()
	src := Path(tree, name)
	for _, l := range ReadLines(src) {
		if strings.HasSuffix(l, ": "+text) || l == text {
			return l, nil
		}
	}
	f, err := os.OpenFile(src, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("вход разговора не записался: %v", err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return "", fmt.Errorf("вход разговора не записался: %v", err)
	}
	f.Close()
	return "", nil
}

// Drop снимает из входа разговора строки с этой репликой. Человек отменил
// недоставленное, и лежать ему в очереди больше незачем: отмена в панели без
// этого убирала пузырь с экрана, а строка оставалась во входе и уезжала агенту
// первым же ходом. Сверка та же, что у Put: своя копия узнаётся по хвосту
// строки. Возвращает число снятых строк, ноль значит, что реплику уже забрал
// подхват.
func Drop(tree, name, text string) (int, error) {
	src := Path(tree, name)
	if len(ReadLines(src)) == 0 {
		return 0, nil
	}
	lock, err := Lock(filepath.Join(Root(tree), name+LockSuffix))
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	lines := ReadLines(src)
	keep := make([]string, 0, len(lines))
	gone := 0
	for _, l := range lines {
		if strings.HasSuffix(l, ": "+text) || l == text {
			gone++
			continue
		}
		keep = append(keep, l)
	}
	if gone == 0 {
		return 0, nil
	}
	if err := writeLines(src, keep); err != nil {
		return 0, fmt.Errorf("вход разговора не переписался: %v", err)
	}
	return gone, nil
}

// Take забирает из входа строки, которые причитаются ждущей сессии sid:
// безадресные и адресованные ей самой. Остальные остаются лежать, их разнесёт
// подхват своим ходом. Пустой возврат значит, что говорить пока нечего.
func Take(tree, name, sid string) ([]string, error) {
	src := Path(tree, name)
	if len(ReadLines(src)) == 0 {
		// Пустой разговор не стоит замка: ожидание опрашивает вход раз в
		// секунду, и брать flock на каждый холостой опрос значило бы толкаться
		// с подхватом там, где брать нечего.
		return nil, nil
	}
	lock, err := Lock(filepath.Join(Root(tree), name+LockSuffix))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	lines := ReadLines(src)
	var mine, rest []string
	for _, l := range lines {
		if ForMe(l, sid) {
			mine = append(mine, l)
			continue
		}
		rest = append(rest, l)
	}
	if len(mine) == 0 {
		return nil, nil
	}
	if err := writeLines(src, rest); err != nil {
		return nil, fmt.Errorf("забранное из разговора не убрать: %v", err)
	}
	return mine, nil
}

// Question это один вопрос человеку: текст, признак множественного выбора и
// варианты. Формат общий с черновиком (LLD DK-354): пачка едет на stdin
// инструмента ожидания и оттуда же читается экраном, и разбора разметки
// финального слова нет вовсе.
type Question struct {
	Text    string   `json:"text"`
	Multi   bool     `json:"multi,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// Option это вариант ответа: подпись, пояснение и пометка рекомендации.
type Option struct {
	Label       string `json:"label"`
	Note        string `json:"note,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Pack это пачка вопросов, как её принимает stdin.
type Pack struct {
	Questions []Question `json:"questions"`
}

// PackLimit это потолок пачки (LLD DK-354): четыре вопроса человек закрывает
// одним заходом, пятый это уже простыня.
const PackLimit = 4

// ParsePack разбирает пачку вопросов из JSON. Одиночный вопрос принимается и
// голым списком, и объектом с полем questions: писать пачку будет агент, и
// отбивать его на форме обёртки дороже, чем принять обе.
func ParsePack(data []byte) ([]Question, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, errors.New("на stdin пусто: жду пачку вопросов JSON {\"questions\": [{\"text\": \"...\"}]}")
	}
	var pack Pack
	if strings.HasPrefix(text, "[") {
		if err := json.Unmarshal([]byte(text), &pack.Questions); err != nil {
			return nil, fmt.Errorf("пачка вопросов не разобралась: %v", err)
		}
	} else if err := json.Unmarshal([]byte(text), &pack); err != nil {
		return nil, fmt.Errorf("пачка вопросов не разобралась: %v", err)
	}
	var out []Question
	for _, q := range pack.Questions {
		if strings.TrimSpace(q.Text) == "" {
			return nil, errors.New("у вопроса пустой текст: человеку показывать нечего")
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, errors.New("в пачке ни одного вопроса")
	}
	if len(out) > PackLimit {
		return nil, fmt.Errorf("вопросов в пачке %d, потолок %d: остальное спрашивается следующим заходом",
			len(out), PackLimit)
	}
	return out, nil
}

// Ask это признак ожидания целиком: срок первой строкой, ниже ждущая сессия,
// задача и пачка вопросов. Срок стоит первой строкой не для красоты: по нему
// подхват и сторожок узнают живое ожидание, читая одну строку, и старый
// однострочный признак цели читается тем же разбором.
//
// Нулевой Until значит «без срока» (DK-715): вопрос агента, перехваченный
// хуком PreToolUse, живёт до ответа, а не до часов, и панель прячет обратный
// отсчёт. Признак цели и старые записи с настоящим сроком читаются тем же
// полем: срок либо есть, либо признан вечным, третьего не дано.
type Ask struct {
	Until     time.Time
	Session   string
	Task      string
	Questions []Question
}

// AskForever это метка «без срока» на месте стамп-строки: время нулевого
// time.Time форматом Stamp читалось бы в разных часовых поясах по-разному
// (год 1 в Local не всегда год 1 в UTC), а словесная метка разбирается
// однозначно везде.
const AskForever = "-"

// Live отвечает, актуален ли признак ожидания на момент now. Без срока
// ожидание живёт до ответа: часы ему не указ, а обратный отсчёт панель не
// рисует вовсе.
func (a Ask) Live(now time.Time) bool {
	return a.Until.IsZero() || now.Before(a.Until)
}

// UnixUntil отдаёт срок в unix-секундах для экрана. Ноль значит «без срока»:
// unix-секунды нулевого time.Time лежат в далёком прошлом и меткой не годятся,
// а ноль клиент уже умеет читать как пропущенное поле (omitempty).
func (a Ask) UnixUntil() int64 {
	if a.Until.IsZero() {
		return 0
	}
	return a.Until.Unix()
}

// Ключевые слова полей признака. Слова русские по тому же образцу, что у
// реестра чатов: файл читают глазами, когда разбираются с зависшим ожиданием.
const (
	askSessionKey = "сессия "
	askTaskKey    = "задача "
)

// Text собирает тело признака.
func (a Ask) Text() string {
	until := AskForever
	if !a.Until.IsZero() {
		until = a.Until.Format(Stamp)
	}
	out := []string{until}
	if a.Session != "" {
		out = append(out, askSessionKey+a.Session)
	}
	if a.Task != "" {
		out = append(out, askTaskKey+a.Task)
	}
	if len(a.Questions) > 0 {
		if data, err := json.Marshal(Pack{Questions: a.Questions}); err == nil {
			out = append(out, string(data))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// ParseAsk разбирает признак. Второй возврат false значит, что признака нет
// или срок не разобрался, то есть никто не ждёт: отсутствие файла и пустое поле
// это именно отсутствие, а не нулевое время.
func ParseAsk(text string) (Ask, bool) {
	var a Ask
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return a, false
	}
	first := strings.TrimSpace(lines[0])
	if first == AskForever {
		a.Until = time.Time{}
	} else {
		until, err := time.ParseInLocation(Stamp, first, time.Local)
		if err != nil {
			return a, false
		}
		a.Until = until
	}
	var body []string
	for _, ln := range lines[1:] {
		s := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(s, askSessionKey):
			a.Session = strings.TrimSpace(strings.TrimPrefix(s, askSessionKey))
		case strings.HasPrefix(s, askTaskKey):
			a.Task = strings.TrimSpace(strings.TrimPrefix(s, askTaskKey))
		case s != "":
			body = append(body, s)
		}
	}
	if len(body) > 0 {
		var pack Pack
		if err := json.Unmarshal([]byte(strings.Join(body, "\n")), &pack); err == nil {
			a.Questions = pack.Questions
		}
	}
	return a, true
}

// ReadAsk читает признак ожидания разговора.
func ReadAsk(path string) (Ask, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Ask{}, false
	}
	return ParseAsk(string(data))
}

// WriteAsk кладёт признак ожидания заменой: читатель на половине записи видит
// либо старый срок, либо новый, но не огрызок.
func WriteAsk(tree, name string, a Ask) error {
	if err := os.MkdirAll(Root(tree), 0o755); err != nil {
		return fmt.Errorf("каталог разговоров не создался: %v", err)
	}
	path := AskPath(tree, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(a.Text()), 0o644); err != nil {
		return fmt.Errorf("признак ожидания не записался: %v", err)
	}
	return os.Rename(tmp, path)
}

// DropAsk снимает признак ожидания. Отсутствие файла это тот же снятый признак,
// а не ошибка: снимают его на любом выходе, включая падение, и второй заход
// уборки штатен.
func DropAsk(tree, name string) error {
	if err := os.Remove(AskPath(tree, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

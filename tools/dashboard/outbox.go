package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Журнал отправленных реплик (пункт 1 второго круга POC DK-397). Реплика
// человека уезжает тремя дорогами, и в транскрипт сессии сама собой попадает
// не каждая: сокет живого клиента пишет её своим ходом, резюм пишет уже в
// новый транскрипт, а безадресная строка во входе задачи достаётся агенту
// вставкой хука и в журнале разговора не оседает вовсе. Отправитель видел свою
// реплику местным пузырём, а второе открытое устройство не видело её никогда.
//
// Поэтому отправленное дашборд пишет себе сам: одна строка JSON на реплику в
// <дом>/said/<разговор>.jsonl. Лента подмешивает журнал к транскрипту по
// времени, стрим дочитывает его тем же порядком, что боковые журналы
// субагентов, и открытые экраны получают реплику все разом. Пришедшее из
// транскрипта эхо ту же реплику вытесняет: сверка по тексту, своего
// идентификатора у записи журнала в транскрипте нет.

// saidRec это строка журнала: время отправки, слова человека и дорога, которой
// реплика ушла.
type saidRec struct {
	Time string `json:"time"`
	Text string `json:"text"`
	// Kind отличает запись-пометку от реплики человека. Пустое значит реплику,
	// saidKindMark значит разделитель ленты: им отмечена смена модели диалога.
	// Пометка едет той же дорогой, что и реплика, нарочно: разделитель обязан
	// пережить перерисовку панели и перезагрузку страницы, а всё, что живёт
	// одной памятью панели, их не переживает.
	Kind    string `json:"kind,omitempty"`
	Way     string `json:"way,omitempty"`
	Sel     string `json:"sel,omitempty"`
	SelFile string `json:"selFile,omitempty"`
	Shot    string `json:"shot,omitempty"`
}

// saidSrc это приставка источника в ключах записей журнала: ключ выходит
// «said-<разговор>:<номер строки>», и по нему лента отсеивает повторы.
const saidSrc = "said-"

// saidKindMark это разряд записи-пометки, а roleMark её роль в ленте: панель
// рисует такую запись разделителем, как разделитель дня, а не пузырём.
const (
	saidKindMark = "mark"
	roleMark     = "mark"
)

// saidKeyRe оставляет в имени разговора только то, из чего собирается имя
// файла: разговор зовётся по сессии или по задаче, и чужого там взяться не
// должно.
var saidKeyRe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func saidDir(home string) string {
	return filepath.Join(home, "said")
}

func saidFile(home, key string) string {
	return filepath.Join(saidDir(home), saidKeyRe.ReplaceAllString(key, "-")+".jsonl")
}

// saidSessionKey это разговор одной сессии: свой журнал у каждого транскрипта.
func saidSessionKey(sid string) string { return "sess-" + sid }

// saidTaskKey это разговор задачи: реплика уходит туда безадресной строкой, и
// достаётся она той сессии, что задачу продолжит.
func saidTaskKey(id string) string { return "task-" + id }

// saidPut дописывает реплику в журнал разговора. Отказ записи не отменяет
// отправки: реплика уже уехала, и ронять из-за журнала ответ ручке нельзя,
// поэтому зовущий пишет отказ в лог и идёт дальше.
func (s *server) saidPut(key string, rec saidRec) error {
	if rec.Time == "" {
		rec.Time = s.now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(saidDir(s.cfg.Home), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(saidFile(s.cfg.Home, key), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// saidLoad читает журнал разговора записями ленты. Номер строки в файле и есть
// её устойчивый ключ: файл только дописывается.
func saidLoad(home, key string) []reply {
	data, err := os.ReadFile(saidFile(home, key))
	if err != nil {
		return nil
	}
	out, _ := saidReplies(strings.Split(string(lastComplete(data)), "\n"), saidSrc+key, 0)
	return out
}

// saidReplies разбирает строки журнала. Битая строка пропускается, но номер за
// ней не сдвигается: ключ обязан совпадать с номером строки в файле, иначе
// стрим и лента назвали бы одну запись по-разному.
func saidReplies(lines []string, src string, from int) ([]reply, int) {
	out := []reply{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		idx := from
		from++
		var rec saidRec
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Text == "" {
			continue
		}
		role := "user"
		if rec.Kind == saidKindMark {
			role = roleMark
		}
		out = append(out, reply{
			Role: role, Time: rec.Time, Text: rec.Text,
			Sel: rec.Sel, SelFile: rec.SelFile, Shot: rec.Shot,
			Key: src + ":" + strconv.Itoa(idx),
		})
	}
	return out, from
}

// saidLines считает записи журнала, уже лежащие в файле: столько же номеров
// раздал им разбор, и с этого номера продолжает счёт дочитывание в потоке.
// Считать выжившие после слияния записи (так было раньше) нельзя: эхо
// выбрасывает часть из них, счёт уезжает назад, и одна и та же реплика
// приезжает в ленту под двумя разными ключами, то есть двумя пузырями.
func saidLines(home, key string) int {
	data, err := os.ReadFile(saidFile(home, key))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(lastComplete(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// saidKeys называет разговоры, чей журнал показывается в ленте сессии: свой
// журнал сессии и журнал задачи, которую эта сессия ведёт. Второй нужен ровно
// затем, зачем заведён: ответ припаркованной задаче уходит безадресной строкой
// и в транскрипт не попадает, а прочитать его человек должен на любом экране,
// откуда эта задача видна.
func saidKeys(sid, task string, bound string) []string {
	keys := []string{saidSessionKey(sid)}
	if task != "" && bound == boundLead {
		keys = append(keys, saidTaskKey(task))
	}
	return keys
}

// saidCut оставляет от журнала разговора только то, что попадает в открытое
// окно ленты. Лента собирается хвостом файла, а журнал лежит целиком, и всё,
// что старше окна, слияние сваливало одной кучей перед первой записью окна:
// человек, листавший историю вверх, видел свои реплики за неделю подряд, а не
// разговор («сгруппировал все сообщения, и теперь мои я вижу одной пачкой»).
// Записи старше окна принадлежат страницам глубже и приезжают вместе с ними.
func saidCut(said []reply, from time.Time) []reply {
	if from.IsZero() || len(said) == 0 {
		return said
	}
	out := make([]reply, 0, len(said))
	for _, it := range said {
		t, err := time.Parse(time.RFC3339, it.Time)
		if err != nil || !t.Before(from) {
			out = append(out, it)
		}
	}
	return out
}

// feedFrom это нижняя граница окна ленты: время самой ранней записи с меткой.
// У ленты, собранной целиком, границы нет вовсе, и журнал вплетается весь.
func feedFrom(items []reply, whole bool) time.Time {
	if whole {
		return time.Time{}
	}
	for _, it := range items {
		if t, err := time.Parse(time.RFC3339, it.Time); err == nil {
			return t
		}
	}
	return time.Time{}
}

// saidMerge вплетает журнал в ленту по времени. Эхо из транскрипта старше
// записи журнала: одну и ту же реплику видно один раз, и показывает её тот
// источник, который агент правда прочитал.
func saidMerge(items []reply, said []reply) []reply {
	if len(said) == 0 {
		return items
	}
	fresh := make([]reply, 0, len(said))
	for _, it := range said {
		// Эхом вытесняется только реплика человека: пометка в транскрипт не
		// попадает вовсе, и сверять её там не с чем, а совпадение текста с
		// чьей-то репликой стёрло бы разделитель из ленты.
		if it.Role != "user" || !saidEcho(items, it.Text) {
			fresh = append(fresh, it)
		}
	}
	if len(fresh) == 0 {
		return items
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Time < fresh[j].Time })
	// Метка времени есть не у каждой записи транскрипта, поэтому место
	// вставки ищется по времени, протянутому от предыдущей записи, тем же
	// правилом, каким слиты боковые журналы (expandSubs).
	eff := effTimes(items)
	out := make([]reply, 0, len(items)+len(fresh))
	at := 0
	for _, it := range fresh {
		t, err := time.Parse(time.RFC3339, it.Time)
		if err != nil {
			continue
		}
		for at < len(items) && !eff[at].After(t) {
			out = append(out, items[at])
			at++
		}
		out = append(out, it)
	}
	out = append(out, items[at:]...)
	for i := range out {
		out[i].Seq = i
	}
	return out
}

// saidEcho говорит, лежит ли та же реплика человека уже в ленте. Сверка по
// тексту: своего идентификатора у реплики в транскрипте нет вовсе, и другой
// приметы у эха тоже нет.
func saidEcho(items []reply, text string) bool {
	want := strings.TrimSpace(text)
	if want == "" {
		return true
	}
	for _, it := range items {
		if it.Role != "user" {
			continue
		}
		if strings.TrimSpace(it.Text) == want || strings.HasSuffix(strings.TrimSpace(it.Text), want) {
			return true
		}
	}
	return false
}

// effTimes протягивает метку времени по ленте: у записи без своей метки время
// предыдущей, назад время не ходит.
func effTimes(items []reply) []time.Time {
	out := make([]time.Time, len(items))
	var prev time.Time
	for i, it := range items {
		if t, err := time.Parse(time.RFC3339, it.Time); err == nil && t.After(prev) {
			prev = t
		}
		out[i] = prev
	}
	return out
}

// saidOf собирает запись журнала из того, что уехало агенту. Префиксы картинки
// и выделения отрезаются тем же разбором, каким их читает лента из транскрипта:
// в пузыре видны слова человека, а приложенное стоит при них отдельно.
func saidOf(wire, way string) saidRec {
	shot, rest := cutShot(wire)
	sel, file, text := cutSelection(rest)
	return saidRec{Text: text, Way: way, Sel: sel, SelFile: file, Shot: shot}
}

// saidMark пишет в журнал разговора пометку-разделитель. Отказ записи стоит
// строки в логе: смена модели уже случилась, и ронять из-за журнала ответ
// ручке нельзя.
func (s *server) saidMark(key, text string) {
	if err := s.saidPut(key, saidRec{Kind: saidKindMark, Text: text}); err != nil {
		s.logf("пометка не легла в журнал разговора %s: %v", key, err)
	}
}

// saidSay пишет отправленное в журнал разговора и не роняет ручку из-за отказа
// записи: реплика уже уехала, и отказ тут стоит строки в логе, а не ошибки
// человеку.
func (s *server) saidSay(key, wire, way string) {
	// Служебный заказ (перезапуск со сменой модели) в журнал разговора не
	// едет: журнал это сказанное человеком, а тут говорит сам дашборд.
	if said, _ := splitService(wire); strings.TrimSpace(said) == "" {
		return
	}
	if err := s.saidPut(key, saidOf(wire, way)); err != nil {
		s.logf("реплика не легла в журнал разговора %s: %v", key, err)
	}
}

// Повторы отправителя. Запись очереди исходящих панели везёт свой ключ в
// каждой попытке, и по этому ключу дашборд отличает повтор от новой реплики.
// Прежде такого ключа не было вовсе: каждая попытка приезжала отдельной
// репликой, и живой случай пользователя это пять одинаковых копий подряд в
// одной сессии, посланных с разницей в минуты. Правило из ленты («повтором
// считать строку, лежащую во входе») тут не работает: копии уходят раньше, чем
// первая ляжет в транскрипт, и каждая выглядит недоставленной.

const (
	// sayKeepFor это память о доставленных записях: дольше часа повторов не
	// бывает, а вечная память росла бы вместе с разговором.
	sayKeepFor = time.Hour
	// sayKeepMax это потолок записей: память чистится по нему, чтобы длинный
	// день переписки не копился в процессе без края.
	sayKeepMax = 512
)

// sayClaim это судьба записи отправителя. Пустая дорога значит, что попытка
// ещё идёт: второй такой же отправке ехать некуда, первая сама всё расскажет.
type sayClaim struct {
	at  time.Time
	way string
}

func sayClaimKey(sid, msg string) string { return sid + "/" + msg }

// prevWord называет судьбу занятой записи словами человека, а не полем.
func prevWord(prev sayClaim) string {
	if prev.way == "" {
		return "отправляется"
	}
	return "доставлена дорогой " + prev.way
}

// chatSayStart занимает запись отправителя на время попытки. Второй ответ
// значит повтор: вместе с ним приезжает судьба первой попытки.
func (s *server) chatSayStart(sid, msg string) (sayClaim, bool) {
	key := sayClaimKey(sid, msg)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.says == nil {
		s.says = map[string]sayClaim{}
	}
	if prev, ok := s.says[key]; ok && now.Sub(prev.at) < sayKeepFor {
		return prev, false
	}
	s.sayPrune(now)
	s.says[key] = sayClaim{at: now}
	return sayClaim{}, true
}

// chatSayDone помнит доставленную запись: повтор такой уже никуда не поедет.
func (s *server) chatSayDone(sid, msg, way string) {
	if msg == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.says == nil {
		s.says = map[string]sayClaim{}
	}
	s.says[sayClaimKey(sid, msg)] = sayClaim{at: s.now(), way: way}
}

// chatSayRelease отпускает запись, чья попытка кончилась ничем: отказ доставки
// это повод повторить, и держать ключ занятым после него значило бы хоронить
// реплику молча.
func (s *server) chatSayRelease(sid, msg string) {
	if msg == "" {
		return
	}
	key := sayClaimKey(sid, msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.says[key]; ok && prev.way == "" {
		delete(s.says, key)
	}
}

// sayPrune чистит память о повторах. Зовётся под замком.
func (s *server) sayPrune(now time.Time) {
	if len(s.says) < sayKeepMax {
		return
	}
	for key, rec := range s.says {
		if now.Sub(rec.at) >= sayKeepFor {
			delete(s.says, key)
		}
	}
	// Не помогло, значит записи свежие: память сбрасывается целиком, и худшее,
	// что случится, это повтор, приехавший вторым разом.
	if len(s.says) >= sayKeepMax {
		s.says = map[string]sayClaim{}
	}
}

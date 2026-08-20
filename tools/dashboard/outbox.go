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
	Time    string `json:"time"`
	Text    string `json:"text"`
	Way     string `json:"way,omitempty"`
	Sel     string `json:"sel,omitempty"`
	SelFile string `json:"selFile,omitempty"`
	Shot    string `json:"shot,omitempty"`
}

// saidSrc это приставка источника в ключах записей журнала: ключ выходит
// «said-<разговор>:<номер строки>», и по нему лента отсеивает повторы.
const saidSrc = "said-"

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
		out = append(out, reply{
			Role: "user", Time: rec.Time, Text: rec.Text,
			Sel: rec.Sel, SelFile: rec.SelFile, Shot: rec.Shot,
			Key: src + ":" + strconv.Itoa(idx),
		})
	}
	return out, from
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

// saidMerge вплетает журнал в ленту по времени. Эхо из транскрипта старше
// записи журнала: одну и ту же реплику видно один раз, и показывает её тот
// источник, который агент правда прочитал.
func saidMerge(items []reply, said []reply) []reply {
	if len(said) == 0 {
		return items
	}
	fresh := make([]reply, 0, len(said))
	for _, it := range said {
		if !saidEcho(items, it.Text) {
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

// saidSay пишет отправленное в журнал разговора и не роняет ручку из-за отказа
// записи: реплика уже уехала, и отказ тут стоит строки в логе, а не ошибки
// человеку.
func (s *server) saidSay(key, wire, way string) {
	if err := s.saidPut(key, saidOf(wire, way)); err != nil {
		s.logf("реплика не легла в журнал разговора %s: %v", key, err)
	}
}

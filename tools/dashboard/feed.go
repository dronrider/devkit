package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Лента разговора собирается из многих файлов: транскрипт сессии и боковой
// журнал на каждый вызов субагента. У долгой сессии это десятки мегабайт
// транскрипта и сотня журналов под сотню мегабайт общим весом, а на экран
// уезжает четыре десятка последних записей. Прежде каждый заход за лентой читал
// и разбирал всё это целиком, и живой разговор отвечал две секунды, причём
// повторный заход стоил ровно столько же: памяти на разбор не было (жалоба
// пользователя «дашборд стал ужасно тормозить»).
//
// Здесь лента собирается хвостами. У каждого файла читается кусок с конца,
// куски сливаются по времени, и, если слитого хвоста на запрошенное окно не
// хватает либо чей-то кусок начинается позже границы окна, дочитывается только
// он. Разобранный кусок лежит в памяти процесса под отпечатком файла (время
// правки и размер), тем же приёмом, каким запомнена шапка сессии: файлы
// журналов дописываются, а не переписываются, и старому куску верить можно,
// пока отпечаток тот же.
//
// Ключ записи при этом считается от смещения её строки в файле, а не от номера
// записи в разборе: номер знает только тот, кто прочитал файл с начала, а
// смещение известно прямо из куска. Ключ остаётся устойчивым (файл только
// дописывается), и пагинация назад по нему режет там же, где резала.

const (
	// feedChunk это первый кусок хвоста, который читается у каждого файла.
	// Столько же читает пульс кольца: на десяток последних записей журнала
	// хватает, а сотня файлов складывается в единицы мегабайт.
	feedChunk = 64 << 10
	// feedGrow это во сколько раз растёт кусок того файла, чьих записей на
	// окно не хватило.
	feedGrow = 16
	// feedRounds это потолок дочитываний: журнал с гигантскими записями иначе
	// тянул бы ленту назад бесконечно.
	feedRounds = 4
	// feedSlack это запас записей сверх запрошенного окна: слияние выбрасывает
	// пары «весть о конце работы плюс отчёт», и без запаса окно оказывалось бы
	// короче запрошенного.
	feedSlack = 50
	// feedKeep это потолок числа файлов в памяти: сессий на машине десятки, и
	// без потолка память процесса росла бы весь его век.
	feedKeep = 600
	// feedTTL это потолок доверия к куску. Отпечаток ловит правку сам, а
	// переписанный файл того же размера и возраста не поймать ничем.
	feedTTL = 5 * time.Minute
	// feedMost это потолок окна, шире которого страница истории не просит:
	// дальше дешевле собрать ленту целиком, чем гадать, где стоит курсор.
	feedMost = 20000
)

// txEntry это то, что процесс помнит о транскрипте помимо самой ленты:
// последний план из TodoWrite и закрытые вызовы инструментов, по которым видно,
// вернулась ли работа субагента. Оба ответа складываются из файла по частям
// (план это последний вызов, закрытие вызова не отменяется), поэтому дописанный
// транскрипт дочитывается с прошлого места, а не читается заново: у кольца
// сессия живая, метка файла меняется каждым ходом, и полное чтение двадцати
// мегабайт на каждый тик стоило бы дороже самой ленты.
type txEntry struct {
	stamp  string
	end    int64
	plan   []planItem
	planAt time.Time
	closed map[string]bool
	born   time.Time
}

var txs struct {
	sync.Mutex
	m map[string]txEntry
}

func transcriptDigest(path string) txEntry {
	fi, err := os.Stat(path)
	if err != nil {
		return txEntry{closed: map[string]bool{}}
	}
	stamp := fileStamp(fi)
	now := time.Now()
	txs.Lock()
	e, hit := txs.m[path]
	txs.Unlock()
	if hit && e.stamp == stamp && now.Sub(e.born) < feedTTL {
		return e
	}
	// Файл усох или память протухла: разбор начинается с начала, дописанному
	// продолжению верить больше нечему.
	if !hit || fi.Size() < e.end || now.Sub(e.born) >= feedTTL {
		e = txEntry{}
	}
	closed := make(map[string]bool, len(e.closed))
	for id := range e.closed {
		closed[id] = true
	}
	if data := lastComplete(readFrom(path, e.end)); len(data) > 0 {
		if plan, at := sessionPlan(data); plan != nil {
			e.plan, e.planAt = plan, at
		}
		for id := range subClosed(data) {
			closed[id] = true
		}
		e.end += int64(len(data))
	}
	e.closed, e.stamp, e.born = closed, stamp, now
	txs.Lock()
	if txs.m == nil || len(txs.m) > feedKeep {
		txs.m = map[string]txEntry{}
	}
	txs.m[path] = e
	txs.Unlock()
	return e
}

// forgetDigests снимает память на разбор транскриптов: нужна стендам, живому
// дашборду хватает отпечатка файла.
func forgetDigests() {
	txs.Lock()
	txs.m = nil
	txs.Unlock()
}

// hasKey говорит, попала ли названная запись в собранное окно ленты.
func hasKey(items []reply, key string) bool {
	for _, it := range items {
		if it.Key == key {
			return true
		}
	}
	return false
}

// feedPart это прочитанный хвост одного файла ленты: разобранные записи,
// признак того, что кусок это весь файл, и место, до которого файл прочитан
// целыми строками (с него дочитывает стрим).
type feedPart struct {
	src   string
	file  string
	items []reply
	whole bool
	end   int64
	size  int64
}

// sessionFeed это собранная лента: записи хвоста и признак того, что хвост это
// весь разговор. Точное число записей известно только у целой ленты: у окна
// сервер не читал файлы с начала и считать в них нечего.
type sessionFeed struct {
	items []reply
	whole bool
	ends  map[string]int64
}

type chunkEntry struct {
	part feedPart
	born time.Time
}

var chunks struct {
	sync.Mutex
	stamp map[string]string
	m     map[string]chunkEntry
}

// forgetChunks снимает память на куски: стендам нужен чистый процесс, а живому
// дашборду эта команда не нужна вовсе, там всё решает отпечаток файла.
func forgetChunks() {
	chunks.Lock()
	chunks.m, chunks.stamp = nil, nil
	chunks.Unlock()
}

func fileStamp(fi os.FileInfo) string {
	return fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
}

// readTail читает хвост файла в size байт и разбирает его. Кусок начинается с
// целой строки: обрезок строки на краю куска разбору не поддаётся, а его начало
// осталось за краем.
func readTail(file, src, label string, size int64, side bool) feedPart {
	fi, err := os.Stat(file)
	if err != nil {
		return feedPart{src: src, file: file, whole: true}
	}
	stamp := fileStamp(fi)
	now := time.Now()
	chunks.Lock()
	e, hit := chunks.m[file]
	same := chunks.stamp[file] == stamp
	chunks.Unlock()
	if hit && same && now.Sub(e.born) < feedTTL && (e.part.whole || e.part.size >= size) {
		return e.part
	}
	from := fi.Size() - size
	if from < 0 {
		from = 0
	}
	data := readFrom(file, from)
	if from > 0 {
		if cut := bytes.IndexByte(data, '\n'); cut >= 0 {
			from += int64(cut) + 1
			data = data[cut+1:]
		} else {
			data = nil
		}
	}
	data = lastComplete(data)
	part := feedPart{src: src, file: file, whole: from == 0, end: from + int64(len(data)), size: size}
	part.items = parseRepliesSpan(data, 0, parseSpan{src: src, off: from, side: side})
	if side {
		part.items = sideItems(part.items, label, part.whole)
	}
	chunks.Lock()
	if chunks.m == nil || len(chunks.m) > feedKeep {
		chunks.m, chunks.stamp = map[string]chunkEntry{}, map[string]string{}
	}
	chunks.m[file] = chunkEntry{part: part, born: now}
	chunks.stamp[file] = stamp
	chunks.Unlock()
	return part
}

// newChunk дочитывает файл от offset целыми строками и разбирает его с теми же
// устойчивыми ключами, что и лента: стрим продолжает ту же нумерацию по
// смещению, и дописанная запись приходит под тем ключом, под каким её отдал бы
// обычный заход. Усечение файла (размер меньше offset) начинает чтение заново.
func newChunk(file, src string, off int64, side bool) ([]reply, []byte, int64) {
	fi, err := os.Stat(file)
	if err != nil {
		return nil, nil, off
	}
	if fi.Size() < off {
		off = 0
	}
	if fi.Size() == off {
		return nil, nil, off
	}
	data := lastComplete(readFrom(file, off))
	if len(data) == 0 {
		return nil, nil, off
	}
	items := parseRepliesSpan(data, 0, parseSpan{src: src, off: off, side: side})
	return items, data, off + int64(len(data))
}

func readFrom(file string, from int64) []byte {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return data
}

// sideItems доводит записи бокового журнала до вида, в котором они стоят в
// ленте: подпись субагента, служебная роль вместо человеческой, отметка отчёта
// и отсев дублей. Разбор тут же, а не в слиянии, потому что кусок лежит в
// памяти уже готовым и правится один раз на чтение, а не на каждый заход.
func sideItems(side []reply, label string, whole bool) []reply {
	// Человек в боковой журнал не пишет, и пузыря человека там быть не может.
	// Первая запись это заказ субагенту, тот же текст уже стоит карточкой
	// вызова Agent, и вторым разом жёлтой простынёй он читался как реплика
	// человека (жалоба пользователя). Остальные записи роли user это
	// служебное: рамки диспетчера свои правила уже разобрали, а что осталось,
	// идёт служебной строкой, а не пузырём.
	if whole && len(side) > 0 && side[0].Role == "user" {
		side = side[1:]
	}
	// Финальный ответ субагента это последняя его запись с текстом: она и есть
	// отчёт, который харнес пересказывает сводкой в своей вести.
	for i := len(side) - 1; i >= 0; i-- {
		if side[i].Role == "assistant" && strings.TrimSpace(side[i].Text) != "" {
			side[i].Report = true
			break
		}
	}
	kept := side[:0]
	for i := range side {
		side[i].Sub = label
		if side[i].Role == "user" {
			side[i].Role = roleNote
		}
		// Реплика диспетчера субагенту в слитой ленте стоит дважды: своей
		// карточкой SendMessage в транскрипте сессии и рамкой в боковом
		// журнале. Пара у неё есть всегда, и рамка тут чистый дубль (жалоба
		// пользователя по снимку). Встречные рамки остаются: у реплики
		// человека и чужой сессии карточки в ленте нет.
		if side[i].Role == roleNote && side[i].Note == dispatchWord("") {
			continue
		}
		kept = append(kept, side[i])
	}
	return kept
}

// sessionFeedOf собирает ленту разговора: хвост транскрипта, хвосты боковых
// журналов и слияние их по времени. want это сколько записей нужно с конца;
// ноль и меньше значит «всю ленту целиком», и так за ней ходит пагинация,
// добравшаяся до начала разговора.
func sessionFeedOf(path string, want int) sessionFeed {
	logs := subLogs(path)
	files := []struct {
		file  string
		src   string
		label string
		side  bool
	}{{file: path, src: mainSrc}}
	// Порядок файлов у os.ReadDir свой, а лента должна собираться одинаково от
	// захода к заходу, поэтому журналы обходятся по имени файла.
	ids := make([]string, 0, len(logs))
	for id := range logs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return logs[ids[i]].File < logs[ids[j]].File })
	for _, id := range ids {
		log := logs[id]
		files = append(files, struct {
			file  string
			src   string
			label string
			side  bool
		}{file: log.File, src: srcName(log.File), label: log.Label, side: true})
	}
	size := map[string]int64{}
	for _, f := range files {
		size[f.src] = feedChunk
		if want <= 0 {
			// Целая лента: кусок заведомо больше любого файла, и первый же
			// заход читает всё.
			size[f.src] = 1 << 62
		}
	}
	var out sessionFeed
	for round := 0; ; round++ {
		parts := make([]feedPart, 0, len(files))
		for _, f := range files {
			p := readTail(f.file, f.src, f.label, size[f.src], f.side)
			if f.src == mainSrc {
				p.items = markLogout(markLead(p.items, len(logs) > 0))
			}
			parts = append(parts, p)
		}
		items, edge, cut := mergeParts(parts, want)
		out = sessionFeed{items: items, ends: map[string]int64{}}
		whole := !cut
		for _, p := range parts {
			out.ends[p.src] = p.end
			if !p.whole {
				whole = false
			}
		}
		out.whole = whole
		if round+1 >= feedRounds {
			return out
		}
		// Дочитывается только тот файл, чей кусок начинается позже границы
		// окна: между началом его куска и границей могли остаться записи, и
		// без них лента соврала бы порядком. Целиком прочитанный файл и файл,
		// чьи записи начинаются раньше границы, не трогаются.
		grow := false
		for _, p := range parts {
			if p.whole {
				continue
			}
			if !cut || len(p.items) == 0 || !partStart(p).Before(edge) {
				size[p.src] *= feedGrow
				grow = true
			}
		}
		if !grow {
			return out
		}
	}
}

// partStart это время первой записи куска: по нему видно, накрывает ли кусок
// границу окна ленты.
func partStart(p feedPart) time.Time {
	for _, it := range p.items {
		if at, err := time.Parse(time.RFC3339, it.Time); err == nil {
			return at
		}
	}
	return time.Time{}
}

// mergeParts сливает куски по времени и отдаёт хвост в want записей. Вторым
// ответом идёт время первой записи хвоста (граница окна), третьим признак того,
// что до хвоста что-то отрезано.
//
// Прежде записи боковых журналов вставлялись за своим вызовом Task, а журнал,
// который пишется прямо сейчас, целиком уезжал в хвост, и хвостовое окно ленты
// состояло из него одного: у субагента, которого продолжают через SendMessage
// не первый день, записей тысячи, а реплики человека и ответы сессии, шедшие с
// ними вперемешку, оказывались за этой тысячей вверху. Слияние по времени
// ставит каждый кусок работы туда, где он и шёл, а идущая сейчас работа сама
// оказывается в хвосте.
func mergeParts(parts []feedPart, want int) ([]reply, time.Time, bool) {
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
	for _, p := range parts {
		var prev time.Time
		for i, it := range p.items {
			at := prev
			if t, err := time.Parse(time.RFC3339, it.Time); err == nil && t.After(prev) {
				at = t
			}
			prev = at
			all = append(all, keyed{it: it, at: at, src: p.src, idx: i})
		}
	}
	// Порядок полный и не зависит от того, что ещё лежит в ленте: время, потом
	// источник, потом место записи в файле. Иначе один и тот же заезд собирал
	// бы ленту по-разному, и ключ «раньше» указывал бы в разные места.
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
	// Время слияния держится рядом с записями: у границы окна метки может не
	// быть своей вовсе, а протянутую от предыдущей записи взять больше неоткуда.
	when := make([]time.Time, 0, len(all))
	for i, k := range all {
		if drop[i] {
			continue
		}
		k.it.Seq = len(out)
		out = append(out, k.it)
		when = append(when, k.at)
	}
	if want <= 0 || len(out) <= want {
		return out, time.Time{}, false
	}
	return out[len(out)-want:], when[len(when)-want], true
}

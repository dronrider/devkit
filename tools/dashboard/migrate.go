package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/works"
)

// Миграция подключает старые разговоры к основному чату записи (LLD DK-1422,
// решение 7). Кандидаты записи собираются из всей доступной истории без окна
// свежих чатов и порога живости, подтверждение стоит на трёх пунктах П1-П3,
// а признаки, которые роли не доказывают, перечислены исключениями и в
// подтверждение не складываются. Классификатор это чистая функция над снимком
// источников, и таблица тестов проверяет правила по перечню LLD. Применение
// пишет те же строки журнала, что и живая модель, источником «миграция», откат
// дописывает обратные строки, не переписывая журнал.

// migSource это снимок источников для классификатора: реестр сессий, память
// разговоров, входы записей, замки и записи работы. Данные чистые, без IO, и
// фикстуры тестов ложатся сюда же. Сборщик наполняет снимок одним обходом
// живых файлов, а разговоры, касания и подтверждения выводятся из него
// целиком.
type migSource struct {
	// Sessions это записи реестра по сессиям порядком журнала: строки реестра
	// служат и касанием, и подтверждением П1/П2.
	Sessions map[string][]sessionBind
	// Store это память разговоров chatStore по ключу: поле Task незачатых
	// записей и цепочки продолжений From/Grown.
	Store map[string]chatStore
	// Inbox называет записи с непустым входом task-<R>.in в их дереве.
	Inbox map[string]bool
	// HeadLock называет записи, чей замок головы держит живой процесс.
	HeadLock map[string]bool
	// Runs называет записи, у которых есть файл работы ~/.devkit/runs.
	Runs map[string]bool
	// GoalTurns называет сессии витков цели: замок цели и носитель «цикл
	// цели» в записях реестра говорят ими одно и то же, чьей работе служит
	// сессия.
	GoalTurns map[string][]string
	// Running называет записи с идущим циклом цели: живой замок оболочки сам
	// ведёт носителя записи, и автоназначение идущей цели не соперник
	// (DK-1009).
	Running map[string]bool
	// Born называет разговор, из которого запись оформлена (П3, источник
	// «оформление» в журнале назначений).
	Born map[string]string
	// Drafts называет черновики: черновику для П2 достаточно непустого входа,
	// строке доски нужен замок головы либо файл работы, цели записей витков.
	Drafts map[string]bool
	// Feeds называет состояние ленты сессии словом: «лента» у разговора
	// с репликами, «пустой» у транскрипта без реплик, отсутствующий ключ
	// у сессии без файла. Разговор без ленты кандидатом не становится,
	// состояние называется словами.
	Feeds map[string]string
	// Busy называет сессии живых носителей действующей работы: перенос
	// назначения с живого носителя ждёт его стопа, и автоназначение на такой
	// разговор не претендует.
	Busy map[string]bool
	// Heads это шапки транскриптов, прочитанные тем же кешем, что и список
	// разговоров: даты и реплики нужны экрану выбора, а повторного чтения
	// классификатор не делает.
	Heads map[string]sessionHead
}

// Слова состояния ленты в снимке.
const (
	migFeedLive  = "лента"
	migFeedBlank = "пустой"
)

// migTalk это разговор, собранный из заходов: сессии одного окна одного
// проекта плюс цепочки продолжений From и выросшие незачатые записи. Key это
// ключ носителя: tmux-<имя окна> у конвейера и цикла цели, свежая сессия у
// прочих разговоров, ключ записи у не начатого разговора.
type migTalk struct {
	Key     string
	Window  string
	Sids    []string
	Blanks  []string
	Touches map[string]bool
}

// Разделы отчёта миграции: вид записи после разбора. Слова видны человеку
// с экрана и в печати команды, одни и те же в обоих местах.
const (
	migClear    = "однозначно"
	migDisputed = "спорно"
	migShared   = "общий"
	migNoFeed   = "без транскрипта"
	migNoTalks  = "без разговоров"
)

// migCand это один кандидат записи: ключ разговора, доказанные пункты
// и признаки, которые решают его судьбу в споре.
type migCand struct {
	Key string `json:"key"`
	// Proof называет доказанные пункты П1-П3 их словами.
	Proof []string `json:"proof,omitempty"`
	// Shared говорит, что разговор касается нескольких записей: общего
	// разговора автоназначение не получает ни для одной.
	Shared bool `json:"shared,omitempty"`
	// Feed называет состояние ленты разговора: пустое слово значит ленту,
	// прочие слова состояния видны человеку в строке кандидата.
	Feed string `json:"feed,omitempty"`
	// Date и Last это дата разговора и его реплика для экрана выбора: человек
	// узнаёт разговор по моменту и по словам, а не по ключу носителя.
	Date string `json:"date,omitempty"`
	Last string `json:"last,omitempty"`
}

// migCase это разбор одной записи: раздел отчёта, кандидаты и ключ
// автоназначения, пустой у всякого ручного пути.
type migCase struct {
	Record string
	Kind   string
	Cands  []migCand
	Auto   string
	// Note называет словами причину ручного пути: что именно помешало
	// автоматике, если раздел не «однозначно».
	Note string
}

// migWinKey это разбор имени окна на запись: task-<R> и goal-<R> называют
// свою запись самим именем, прочие имена окна записи не называют.
var migWinKey = regexp.MustCompile(`^(?:task|goal)-([A-Za-z]+-[0-9]+)$`)

// migTalks собирает разговоры из заходов. Сессии одного окна одного проекта
// сходятся в один разговор, как в склейке списка, и без деления по жизням
// имени: миграция читает всю историю, а не окно списка. Цепочки продолжений
// From и выросшие незачатые записи доклеиваются к тем же разговорам, потому
// что продолжение это та же история. Ключом носителя конвейер и цикл цели
// называют своё окно, прочие разговоры свежую сессию, а не начатый разговор
// саму запись.
func migTalks(src migSource) []migTalk {
	// Сущность это сессия либо незачатая запись, группы сливаются союзами
	// через представителя.
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p, ok := parent[x]; ok && p != x {
			parent[x] = find(p)
			return parent[x]
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for sid := range src.Sessions {
		parent[sid] = sid
	}
	for key, st := range src.Store {
		if st.Blank {
			parent[key] = key
		}
	}
	// Заходы одного окна: ключ склейки это проект и имя, как у списка, иначе
	// разговоры двух проектов с одинаковым номером окна слипались бы в одну
	// историю.
	byWin := map[string]string{}
	for sid, recs := range src.Sessions {
		last := sessions.Last(recs)
		if last.Tmux == "" || last.Project == "" {
			continue
		}
		win := last.Project + "\x00" + strings.SplitN(last.Tmux, ":", 2)[0]
		if had, ok := byWin[win]; ok {
			union(had, sid)
		} else {
			byWin[win] = sid
		}
	}
	// Цепочки продолжений и выросшие записи: продолжение поднимается новой
	// сессией со своим транскриптом, и без склейки история рвалась бы на
	// отдельные разговоры. Чужой ключ, которого нет среди сущностей, склейки
	// не делает: ссылки в памяти бывают и на убранные разговоры.
	for key, st := range src.Store {
		if _, known := parent[key]; !known {
			continue
		}
		for _, link := range []string{st.From, st.Grown} {
			if link == "" {
				continue
			}
			if _, ok := parent[link]; ok {
				union(key, link)
			}
		}
	}
	groups := map[string][]string{}
	for x := range parent {
		root := find(x)
		groups[root] = append(groups[root], x)
	}
	// Голова группы это свежий член: у сессии время меряет последняя запись
	// реестра, у незачатой записи момент заведения.
	headAt := func(x string) string {
		if recs, ok := src.Sessions[x]; ok {
			return sessions.Last(recs).Time
		}
		return time.Unix(src.Store[x].Born, 0).Format(sessions.Stamp)
	}
	isSid := func(x string) bool {
		_, ok := src.Sessions[x]
		return ok
	}
	var talks []migTalk
	for _, members := range groups {
		sort.Slice(members, func(i, j int) bool {
			ai, aj := headAt(members[i]), headAt(members[j])
			if ai != aj {
				return ai > aj
			}
			return members[i] < members[j]
		})
		t := migTalk{Touches: map[string]bool{}}
		for _, m := range members {
			if isSid(m) {
				t.Sids = append(t.Sids, m)
			} else {
				t.Blanks = append(t.Blanks, m)
			}
		}
		// Свежий член называет ключ носителя: у начатого разговора это живая
		// сессия, чей транскрипт откроет панель, у не начатого сама запись.
		if len(t.Sids) > 0 {
			t.Key = t.Sids[0]
		} else {
			t.Key = t.Blanks[0]
		}
		// Окно группы носит её свежая сессия, у которой имя есть. Конвейер
		// и цикл цели узнаются по имени окна: их носитель это само окно,
		// ключом tmux-<имя> его же называет и журнал назначений.
		for _, sid := range t.Sids {
			if name := strings.SplitN(sessions.Last(src.Sessions[sid]).Tmux, ":", 2)[0]; name != "" {
				t.Window = name
				break
			}
		}
		if migWinKey.MatchString(t.Window) {
			t.Key = "tmux-" + t.Window
		}
		migTouches(src, &t)
		talks = append(talks, t)
	}
	sort.Slice(talks, func(i, j int) bool { return talks[i].Key < talks[j].Key })
	return talks
}

// migTouches называет записи, которых разговор касается: строки реестра его
// сессий, поле Task незачатых записей, носитель витка цели и имена окон
// с ID. Касание шире подтверждения: оно лишь собирает кандидатов, а роль
// доказывают пункты П1-П3, и подтверждение требует касания ровно одной
// записи.
func migTouches(src migSource, t *migTalk) {
	for _, sid := range t.Sids {
		for _, id := range sessions.Touched(src.Sessions[sid]) {
			t.Touches[id] = true
		}
		if carrier := sessions.Last(src.Sessions[sid]).Carrier; carrier != "" {
			if rest, ok := strings.CutPrefix(carrier, "цикл цели "); ok {
				t.Touches[strings.ToUpper(rest)] = true
			}
		}
	}
	for _, b := range t.Blanks {
		if id := strings.ToUpper(strings.TrimSpace(src.Store[b].Task)); id != "" {
			t.Touches[id] = true
		}
	}
	if m := migWinKey.FindStringSubmatch(t.Window); m != nil {
		t.Touches[strings.ToUpper(m[1])] = true
	}
}

// migPinnedWords это слова источника, которыми строка реестра П1 называет
// явную привязку. Оба ставятся действием по строке записи: хук подъёма по
// кнопке и привязка человеком, а «дерево» и касания свёртки Touched сюда
// не входят.
var migPinnedWords = map[string]bool{sessions.ByOrder: true, sessions.ByHand: true}

// migPinned отвечает, ведёт ли сессия запись task словом источника из words.
// Снятие гасит привязку, как у свёртки lead: «снята» с названной записью
// снимает одну её, с пустой всё.
func migPinned(recs []sessionBind, task string, words map[string]bool) bool {
	off := false
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Source == sessions.ByOff {
			if r.Task == "" {
				return false
			}
			if r.Task == task {
				off = true
			}
			continue
		}
		if r.Task == task && words[r.Source] {
			return !off
		}
	}
	return false
}

// Слова подтверждения: одно на пункт, человек читает их в отчёте и на экране
// выбора.
const (
	migProofBind  = "привязка"
	migProofWork  = "работа"
	migProofBirth = "рождение"
)

// migProofs называет пункты, которыми разговор доказан диспетчерским для
// записи. Признаки-исключения сюда не входят: свежесть транскрипта, имя окна
// без строки реестра и записи работы, упоминание ID в тексте, единственность
// найденного транскрипта и цепочка продолжений без подтверждённого члена.
func migProofs(src migSource, t migTalk, rec string) []string {
	var out []string
	for _, sid := range t.Sids {
		if migPinned(src.Sessions[sid], rec, migPinnedWords) {
			out = append(out, migProofBind)
			break
		}
	}
	if migWorks(src, t, rec) {
		out = append(out, migProofWork)
	}
	if src.Born[rec] != "" && migMemberOf(t, src.Born[rec]) {
		out = append(out, migProofBirth)
	}
	sort.Strings(out)
	return out
}

// migMemberOf отвечает, входит ли ключ носителя в разговор. Строка рождения
// называет ключ на момент оформления, а незачатая запись потом вырастает
// в сессию, и родство сверяется по членству, а не по равенству ключей.
func migMemberOf(t migTalk, key string) bool {
	return t.Key == key || hasRow(t.Sids, key) || hasRow(t.Blanks, key)
}

// migWorks это пункт П2: работа. Заходы под именем task-<R> при записи
// работы R, записи работы с источником «работа», витки цели. Черновику
// достаточно непустого входа либо цепочки разговоров с Task=R: замка головы
// и файла работы у него не бывает.
func migWorks(src migSource, t migTalk, rec string) bool {
	if t.Window == "task-"+rec && (src.HeadLock[rec] || src.Runs[rec]) {
		return true
	}
	for _, sid := range t.Sids {
		if sessions.WorksOn(src.Sessions[sid], rec) {
			return true
		}
		if hasRow(src.GoalTurns[rec], sid) {
			return true
		}
		// Носитель «цикл цели <R>» в самой записи реестра называет виток
		// той же строкой, что и снимок GoalTurns: записи сессии довольно,
		// и фикстура теста обходится без снимка.
		if rest, ok := strings.CutPrefix(sessions.Last(src.Sessions[sid]).Carrier, "цикл цели "); ok &&
			strings.EqualFold(rest, rec) {
			return true
		}
	}
	for _, b := range t.Blanks {
		if strings.EqualFold(src.Store[b].Task, rec) {
			return true
		}
	}
	if src.Drafts[rec] && src.Inbox[rec] && t.Touches[rec] {
		return true
	}
	return false
}

// migFeedState называет состояние ленты разговора: пустое слово значит
// ленту, «пустой» разговор без реплик, «без транскрипта» разговор, чьего
// файла нет. Отсутствующий транскрипт это явное состояние, а не кандидат.
func migFeedState(src migSource, t migTalk) string {
	state := migNoFeed
	for _, sid := range t.Sids {
		switch src.Feeds[sid] {
		case migFeedLive:
			return ""
		case migFeedBlank:
			state = migFeedBlank
		}
	}
	return state
}

// migTalkHead называет дату и реплику разговора для экрана выбора. Дату несёт
// свежая сессия: метка последней реплики, а без реплик момент заведения
// транскрипта. Реплика человека узнаёт разговор по словам, и у незачатой
// записи его место занимает набранный черновик первой реплики.
func migTalkHead(src migSource, t migTalk) (string, string) {
	for _, sid := range t.Sids {
		if h, ok := src.Heads[sid]; ok {
			date := h.Said
			if date == "" {
				date = h.Born
			}
			return date, migClip(h.First)
		}
	}
	for _, b := range t.Blanks {
		st := src.Store[b]
		return time.Unix(st.Born, 0).Format(sessions.Stamp), migClip(st.Draft)
	}
	return "", ""
}

// migClip ужимает реплику для строки кандидата: экрану выбора хватает зачина,
// а полный текст панель покажет самой лентой после выбора.
func migClip(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= 200 {
		return string(runes)
	}
	return string(runes[:200]) + "..."
}

// migBusy отвечает, живой ли носитель действующей работы: сессия разговора
// жива и работу ведёт. Перенос назначения с такого носителя ждёт стопа либо
// конца хода, и автоназначение на него не претендует.
func migBusy(src migSource, t migTalk) bool {
	for _, sid := range t.Sids {
		if src.Busy[sid] {
			return true
		}
	}
	return false
}

// migCaseFor считает разбор одной записи: кандидаты по касаниям, подтверждения
// по пунктам и решение автоматики. Кандидат без подтверждений остаётся в
// споре, разговор нескольких записей и разговор без ленты автоматикой не
// назначаются вовсе.
func migCaseFor(src migSource, talks []migTalk, rec string) migCase {
	c := migCase{Record: rec}
	var proven []migCand
	for _, t := range talks {
		if !t.Touches[rec] {
			continue
		}
		cand := migCand{Key: t.Key, Proof: migProofs(src, t, rec), Shared: len(t.Touches) > 1}
		cand.Feed = migFeedState(src, t)
		cand.Date, cand.Last = migTalkHead(src, t)
		c.Cands = append(c.Cands, cand)
		if len(cand.Proof) > 0 && !cand.Shared && cand.Feed == "" {
			proven = append(proven, cand)
		}
	}
	if len(c.Cands) == 0 {
		c.Kind = migNoTalks
		return c
	}
	// Автоназначение: единственный подтверждённый, не живой носитель
	// действующей работы и не идущая цель. Занятость другой записью видна
	// только всем разбором сразу, её достраивает migScan.
	switch {
	case len(proven) > 1:
		c.Note = fmt.Sprintf("подтверждённых кандидатов %d", len(proven))
	case len(proven) == 1 && src.Running[rec]:
		c.Note = "идущий цикл цели ведёт носителя сам"
	case len(proven) == 1 && migBusy(src, migTalkOf(talks, proven[0].Key)):
		c.Note = "кандидат это живой носитель действующей работы"
	case len(proven) == 1:
		c.Kind, c.Auto = migClear, proven[0].Key
		return c
	default:
		c.Note = "подтверждений не набралось"
	}
	c.Kind = migDisputed
	// Раздел, когда весь спор в самом разговоре: общий либо без ленты.
	// Причину этих разделов называют сам раздел и строка кандидата, а примечание
	// о неподтверждённости спорило бы с доказательствами той же строки.
	onlyShared, onlyNoFeed := true, true
	for _, cand := range c.Cands {
		onlyShared = onlyShared && cand.Shared
		onlyNoFeed = onlyNoFeed && cand.Feed != ""
	}
	if onlyShared {
		c.Kind, c.Note = migShared, ""
	} else if onlyNoFeed {
		c.Kind, c.Note = migNoFeed, ""
	}
	return c
}

// migTalkOf находит разговор по ключу носителя.
func migTalkOf(talks []migTalk, key string) migTalk {
	for _, t := range talks {
		if t.Key == key {
			return t
		}
	}
	return migTalk{}
}

// migScan считает разбор всех записей сразу. Классификатор остаётся чистым
// над снимком, а здесь достраиваются проверки, которым нужно видеть все
// записи разом: разговор, подтверждённый двум записям, спорным делает обе,
// ведь инвариант журнала говорит «разговор основной не больше чем для одной
// записи», и автоматике тут решать нечего.
func migScan(src migSource, records []string) []migCase {
	talks := migTalks(src)
	cases := make([]migCase, 0, len(records))
	for _, rec := range records {
		cases = append(cases, migCaseFor(src, talks, rec))
	}
	byTalk := map[string][]int{}
	for i, c := range cases {
		if c.Auto != "" {
			byTalk[c.Auto] = append(byTalk[c.Auto], i)
		}
	}
	for _, idx := range byTalk {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			cases[i].Kind, cases[i].Auto = migDisputed, ""
			cases[i].Note = "разговор подтверждён ещё одной записи"
		}
	}
	return cases
}

// migRecord это запись, которой касается миграция: строка доски, закрытая
// строка архива, черновик накопителя либо цель. Закрытая запись в применении
// не участвует и подключается при обращении ручкой адреса, а в отчёте стоит
// наравне: человек видит всю историю, до которой дотянутся кнопки записей.
type migRecord struct {
	ID      string
	Project string
	Path    string
	Closed  bool
	Draft   bool
}

// migCollect обходит проекты и собирает записи. Цели узнаются живым реестром
// целей и строками с заголовком «Цель:», тем же признаком, каким кнопку чата
// узнаёт цель.
func (s *server) migCollect() []migRecord {
	var out []migRecord
	projects, _ := s.projects()
	for _, p := range projects {
		seen := map[string]bool{}
		add := func(r migRecord) {
			r.ID = strings.ToUpper(strings.TrimSpace(r.ID))
			if r.ID == "" || seen[r.ID] || !goalIDRe.MatchString(r.ID) {
				return
			}
			r.Project, r.Path = p.Name, p.Path
			seen[r.ID] = true
			out = append(out, r)
		}
		for _, id := range works.RegistryGoals(s.cfg.Home, p.Path) {
			add(migRecord{ID: id})
		}
		var rows map[string]boardRow
		if raw, err := s.projectBoard(p.Path); err == nil {
			rows, _ = parseBoardRows(raw)
		}
		for id := range rows {
			add(migRecord{ID: id})
		}
		for id := range s.archiveRows(p.Path) {
			add(migRecord{ID: id, Closed: true})
		}
		for _, id := range draftIDs(p.Path) {
			add(migRecord{ID: id, Draft: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// draftIDs называет черновики накопителя проекта обходом каталога: правда о
// накопителе лежит у taskctl, но миграции нужен только состав ID, и файлы
// читаются дешевле, чем поднимается утилита.
func draftIDs(projectPath string) []string {
	dir := filepath.Join(projectPath, "docs", "tasks", "drafts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if id, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			out = append(out, id)
		}
	}
	return out
}

// migSnapshot собирает снимок источников. Читается то, что перечислено
// решением 7: реестр, память разговоров, входы, замки, файлы работы, витки
// целей и ленты. Снимок один на прогон, и повторного чтения классификатор
// не делает.
func (s *server) migSnapshot(recs []migRecord) migSource {
	src := migSource{
		Sessions:  s.bindsAll(),
		Store:     map[string]chatStore{},
		Inbox:     map[string]bool{},
		HeadLock:  map[string]bool{},
		Runs:      map[string]bool{},
		GoalTurns: map[string][]string{},
		Running:   map[string]bool{},
		Born:      map[string]string{},
		Drafts:    map[string]bool{},
		Feeds:     map[string]string{},
		Busy:      map[string]bool{},
		Heads:     map[string]sessionHead{},
	}
	// Память разговоров целиком: цепочки продолжений тянутся и через записи,
	// которых нет среди сессий реестра.
	if entries, err := os.ReadDir(chatStoreDir(s.cfg.Home)); err == nil {
		for _, e := range entries {
			key, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || e.IsDir() {
				continue
			}
			src.Store[key] = s.chatStoreRead(key)
		}
	}
	// Витки цели: сессия цикла называет цель носителем своей записи реестра,
	// а замок цели называет виток по имени, и оба говорят одно.
	for sid, recs := range src.Sessions {
		for _, r := range recs {
			if rest, ok := strings.CutPrefix(r.Carrier, "цикл цели "); ok {
				id := strings.ToUpper(rest)
				if !hasRow(src.GoalTurns[id], sid) {
					src.GoalTurns[id] = append(src.GoalTurns[id], sid)
				}
			}
		}
	}
	// Ленты: сессии с транскриптом, тем же кешем, что и список разговоров.
	// Пустой транскрипт без реплик и сессия без файла различаются словами
	// состояния.
	for _, f := range sessionFiles(s.transcriptRoots(), "") {
		head := s.sessionHeadCached(f.path, f.stamp)
		src.Heads[f.ID] = head
		if chatBlank(head) {
			src.Feeds[f.ID] = migFeedBlank
		} else {
			src.Feeds[f.ID] = migFeedLive
		}
	}
	live := s.peers()
	for sid, recs := range src.Sessions {
		if _, alive := live[sid]; !alive {
			continue
		}
		if len(sessions.Works(recs)) > 0 || sessions.Last(recs).Carrier != "" {
			src.Busy[sid] = true
		}
	}
	// Замок головы, вход, файлы работы и замок цели спрашиваются по записи.
	home := realHomeFn()
	if home == "" {
		home = s.cfg.Home
	}
	for _, r := range recs {
		if r.Draft {
			src.Drafts[r.ID] = true
		}
		src.Inbox[r.ID] = inboxNonEmpty(r.Path, r.ID)
		if pid := taskhead.Owner(taskhead.LockPath(home, r.ID)); taskhead.Alive(pid) {
			src.HeadLock[r.ID] = true
		}
		if paths, _ := filepath.Glob(filepath.Join(stage.Dir(s.cfg.Home), r.ID+"-*.run")); len(paths) > 0 {
			src.Runs[r.ID] = true
		}
		if turn := goalTurn(r.Path, r.ID); turn != "" {
			src.Running[r.ID] = true
			if !hasRow(src.GoalTurns[r.ID], turn) {
				src.GoalTurns[r.ID] = append(src.GoalTurns[r.ID], turn)
			}
		}
	}
	// Рождение записи: журнал назначений знает разговоры, из которых записи
	// оформлены, и миграция узнаёт пункт П3 из него же.
	for rec, binds := range mainchat.All(s.mainChatData()) {
		for _, b := range binds {
			if b.Chat != "" && b.Source == mainchat.ByDecor {
				src.Born[rec] = b.Chat
			}
		}
	}
	return src
}

// inboxNonEmpty отвечает, лежит ли в дереве записи непустой вход: реплики,
// ждущие носителя. Пустой файл и отсутствующий отвечают одинаково, входа нет.
func inboxNonEmpty(tree, id string) bool {
	data, err := os.ReadFile(chat.Path(tree, chat.TaskName(id)))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) != ""
}

// migReport печатает отчёт по четырём разделам: однозначные, спорные, общие,
// без транскрипта. До применения человек сверяет разбор, и те же слова стоят
// в строках применения. Записи без единого разговора посчитаны шапкой: им
// выбирать не из чего, и экран выбора скажет то же словами.
func migReport(out *strings.Builder, cases []migCase, closed map[string]bool) {
	counts := map[string]int{}
	for _, c := range cases {
		counts[c.Kind]++
	}
	fmt.Fprintf(out, "записей в разборе %d: однозначных %d, спорных %d, общих %d, без транскрипта %d, без разговоров %d\n",
		len(cases), counts[migClear], counts[migDisputed], counts[migShared], counts[migNoFeed], counts[migNoTalks])
	section := func(word, kind string) {
		fmt.Fprintf(out, "\n%s:\n", word)
		if counts[kind] == 0 {
			fmt.Fprintln(out, "  (пусто)")
			return
		}
		for _, c := range cases {
			if c.Kind != kind {
				continue
			}
			if kind == migClear {
				mark := ""
				if closed[c.Record] {
					mark = " (закрытая, подключится при обращении)"
				}
				fmt.Fprintf(out, "  %s -> %s (%s)%s\n", c.Record, c.Auto,
					strings.Join(migProofWords(c), ", "), mark)
				continue
			}
			fmt.Fprintf(out, "  %s: %s\n", c.Record, migCandWords(c))
			if c.Note != "" {
				fmt.Fprintf(out, "    %s\n", c.Note)
			}
		}
	}
	section("однозначные", migClear)
	section("спорные", migDisputed)
	section("общие", migShared)
	section("без транскрипта", migNoFeed)
}

// migProofWords называет пункты подтверждения автоназначения.
func migProofWords(c migCase) []string {
	for _, cand := range c.Cands {
		if cand.Key == c.Auto {
			return cand.Proof
		}
	}
	return nil
}

// migCandWords подписывает кандидатов строки словами: ключ и доказательства,
// а у неподтверждённых причина спора.
func migCandWords(c migCase) string {
	var parts []string
	for _, cand := range c.Cands {
		word := cand.Key
		switch {
		case len(cand.Proof) > 0:
			word += " (" + strings.Join(cand.Proof, ", ") + ")"
		case cand.Shared:
			word += " (общий)"
		case cand.Feed != "":
			word += " (" + cand.Feed + ")"
		default:
			word += " (без подтверждений)"
		}
		parts = append(parts, word)
	}
	return strings.Join(parts, "; ")
}

// migApply назначает однозначные разговоры активным записям. Строки журнала
// те же, что у живой модели, источником «миграция». Запись с действующим
// назначением пропускается: повторный прогон назначенного не трогает, снятое
// рукой автоматика не возвращает, и ручной выбор старше автоматики. Закрытая
// запись подключается при обращении, здесь она остаётся ручному пути.
// Возвращаются строки итога для печати.
func (s *server) migApply(cases []migCase, projectOf map[string]string, closed map[string]bool) []string {
	var out []string
	binds := mainchat.Parse(s.mainChatData())
	for _, c := range cases {
		if c.Kind != migClear || c.Auto == "" || closed[c.Record] {
			continue
		}
		if cur, seen := binds[c.Record]; seen {
			word := "назначение уже стоит"
			if cur.Chat == "" {
				word = "назначение снято, автоматика его не возвращает"
			}
			out = append(out, fmt.Sprintf("%s: %s, пропущена", c.Record, word))
			continue
		}
		err := mainchat.Put(s.cfg.Home, mainchat.Bind{
			Record: c.Record, Chat: c.Auto, Project: projectOf[c.Record], Source: mainchat.ByMigrate})
		if err != nil {
			out = append(out, fmt.Sprintf("%s: назначение не записалось, %v", c.Record, err))
			continue
		}
		out = append(out, fmt.Sprintf("%s -> %s (%s)", c.Record, c.Auto, strings.Join(migProofWords(c), ", ")))
	}
	return out
}

// migRollback снимает назначения, поставленные миграцией: дописываются
// обратные строки, журнал не переписывается, переписка и носители остаются на
// месте. Назначение, смещённое после миграции рукой либо передачей, откат
// не трогает: слово источника в свёртке уже не «миграция».
func migRollback(home string, data []byte) []string {
	var out []string
	for rec, bind := range mainchat.Parse(data) {
		if bind.Source != mainchat.ByMigrate || bind.Chat == "" {
			continue
		}
		if err := mainchat.Put(home, mainchat.Bind{
			Record: rec, Chat: "", Source: mainchat.ByOff}); err != nil {
			out = append(out, fmt.Sprintf("%s: снятие не записалось, %v", rec, err))
			continue
		}
		out = append(out, fmt.Sprintf("%s: назначение %s снято", rec, bind.Chat))
	}
	return out
}

// migTriedEntry это память ленивого разбора: спорный итог держится минуту,
// чтобы опрос панели не поднимал разбор всей истории на каждом круге.
// Назначение, поставленное разбором либо рукой, видит сам журнал, и кеш его
// не переживает.
type migTriedEntry struct {
	at time.Time
	c  migCase
}

// migResolve делает ленивый разбор записи при обращении ручки адреса.
// Активные записи переводит прогон команды, закрытые и опоздавшие подключает
// само обращение: однозначное назначение пишется тут же строкой «миграция»,
// спорный итог возвращается кандидатом экрана выбора и кешируется на минуту.
// Ручной выбор старше автоматики, и запись со строкой в журнале сюда
// не попадает вовсе.
func (s *server) migResolve(found *Project, id string) (migCase, bool) {
	now := s.now()
	s.migMu.Lock()
	if tried, ok := s.migTried[id]; ok && now.Sub(tried.at) < time.Minute {
		s.migMu.Unlock()
		return tried.c, false
	}
	s.migMu.Unlock()
	rec := migRecord{ID: id, Project: found.Name, Path: found.Path,
		Draft: draftHere(found.Path, id)}
	src := s.migSnapshot([]migRecord{rec})
	c := migCaseFor(src, migTalks(src), id)
	if c.Kind == migClear && c.Auto != "" {
		err := mainchat.Put(s.cfg.Home, mainchat.Bind{
			Record: id, Chat: c.Auto, Project: found.Name, Source: mainchat.ByMigrate})
		if err == nil {
			s.logf("миграция %s: разговор %s подключён при обращении", id, c.Auto)
			return c, true
		}
		var held *mainchat.HeldError
		if errors.As(err, &held) {
			c.Kind, c.Auto = migDisputed, ""
			c.Note = fmt.Sprintf("разговор уже основной у записи %s", held.Record)
		} else {
			c.Kind, c.Auto = migDisputed, ""
			c.Note = fmt.Sprintf("назначение не записалось: %v", err)
		}
	}
	s.migMu.Lock()
	s.migTried[id] = migTriedEntry{at: now, c: c}
	s.migMu.Unlock()
	return c, false
}

// cmdMigrate это подкоманда разовой миграции: без флагов печатает отчёт
// по четырём разделам, --apply назначает однозначные разговоры активным
// записям, --rollback снимает поставленное миграцией, не трогая переписку.
func cmdMigrate(home string, apply, rollback bool, out io.Writer) error {
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	if rollback {
		lines := migRollback(s.cfg.Home, s.mainChatData())
		if len(lines) == 0 {
			fmt.Fprintln(out, "назначений миграции не нашлось")
			return nil
		}
		for _, ln := range lines {
			fmt.Fprintln(out, ln)
		}
		return nil
	}
	recs := s.migCollect()
	closed := map[string]bool{}
	projectOf := map[string]string{}
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		ids = append(ids, r.ID)
		closed[r.ID] = r.Closed
		projectOf[r.ID] = r.Project
	}
	cases := migScan(s.migSnapshot(recs), ids)
	var rep strings.Builder
	migReport(&rep, cases, closed)
	fmt.Fprint(out, rep.String())
	if !apply {
		fmt.Fprintf(out, "\nприменяет `dashboard migrate --apply`\n")
		return nil
	}
	lines := s.migApply(cases, projectOf, closed)
	if len(lines) == 0 {
		fmt.Fprintln(out, "назначений не прибавилось")
		return nil
	}
	for _, ln := range lines {
		fmt.Fprintln(out, ln)
	}
	return nil
}

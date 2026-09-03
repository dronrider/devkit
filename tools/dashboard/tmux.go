package main

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/works"
)

// Живой статус агента, сторона tmux: список сессий машины и снимок пейна
// через capture-pane. tmux это подпроцесс со сроком (runProc), как все чужие
// программы сервера: зависший снимок не должен держать горутину запроса.
// Список общий на машину, а не на проект: сессии tmux к корням не привязаны,
// привязку к работе делает клиент по имени goal-<ID>/task-<ID>.

// tmuxSession это строка списка: имя, окна и время создания unix-секундами,
// как их отдаёт формат tmux.
type tmuxSession struct {
	Name    string `json:"name"`
	Windows int    `json:"windows"`
	Created int64  `json:"created"`
}

// tmuxList отдаёт сессии; разбор вывода живёт в общем каркасе
// (internal/works), потому что занятость задач по именам сессий читает и
// планировщик слота taskctl. Ненулевой код ls это штатное «сессий нет».
func tmuxList() []tmuxSession {
	sessions := []tmuxSession{}
	for _, s := range works.Sessions() {
		sessions = append(sessions, tmuxSession(s))
	}
	return sessions
}

func (s *server) handleTmuxList(w http.ResponseWriter, r *http.Request) {
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	sessions := tmuxList()
	if sessions == nil {
		sessions = []tmuxSession{}
	}
	resp := map[string]any{"sessions": sessions}
	if len(sessions) == 0 {
		resp["note"] = "tmux-сессий нет"
	}
	writeJSON(w, http.StatusOK, resp)
}

var tmuxNameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

func (s *server) handleTmuxPane(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !tmuxNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на имя tmux-сессии", name)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Знак = требует точного имени сессии, без него tmux берёт её по префиксу
	// и снимок пришёл бы от соседки; capture-pane ждёт цель-пейн, поэтому
	// после имени стоит двоеточие: активное окно точной сессии.
	out, err := runProc("tmux", "capture-pane", "-p", "-t", "="+name+":")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Ненулевой код это «сессии нет»: пустота различима, снимок без
			// сессии называется словами, а не пустым экраном.
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("tmux-сессия %s не найдена: %s", name, procErr(err))})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": procErr(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "text": string(out)})
}

// Вопрос клиента в панели tmux (замечание пользователя: «не хочу каждый раз
// чинить что-то через тебя»). Клиент, поднятый в непривычном каталоге, встаёт
// на вопросе о доверии («Yes, I trust this folder»), а следом на вопросе про
// внешние импорты правил, и до ответа он не делает ни хода. Человек этих
// вопросов не видит вовсе: панель дашборда показывает пустую ленту, реплика
// висит недоставленной, а ответить можно было только руками в tmux. Тут снимок
// панели разбирается на вопрос и варианты, и панель показывает их кнопками.

// frameRunes это знаки рамки клиента: ими он отбивает свой блок, и текст
// вопроса выше рамки уже не идёт. Знаки чужие, дашборд их только узнаёт.
const frameRunes = "\u2500\u2014-\u2550"

// tmuxAsk это разобранный вопрос: сам текст, варианты по порядку, номер того,
// на котором стоит курсор клиента, чем виджет отвечает и полоса шагов опроса.
type tmuxAsk struct {
	Text    string     `json:"text,omitempty"`
	Options []tmuxPick `json:"options,omitempty"`
	At      int        `json:"at,omitempty"`
	// Keys называет способ ответа: askKeysDigit это выбор номером (так устроен
	// вопрос доверия каталогу и сводка опроса), askKeysArrows это ход стрелками
	// и Enter (так устроен сам опрос). Способ читается с самой панели, а не
	// угадывается: клиент печатает подсказку навигации под виджетом.
	Keys string `json:"keys,omitempty"`
	// Steps это шаги опроса. У клиента они табы: между ними ходят стрелками
	// влево-вправо, ответа на текущий шаг для перехода не требуется, а ответы
	// копятся (проверено на живой панели).
	Steps []tmuxStep `json:"steps,omitempty"`
	// Kind называет вид экрана: askKindReview это сводка ответов, которой
	// виджет кончает опрос. Пусто у обычного вопроса.
	Kind string `json:"kind,omitempty"`
	// Said это сводка: вопрос и ответ на него, как их печатает сам виджет.
	Said []tmuxSaid `json:"said,omitempty"`
	// Warn это предупреждение сводки («не на все вопросы отвечено»): по нему
	// видно, что отправлять рано.
	Warn string `json:"warn,omitempty"`
}

// tmuxPick это одна остановка курсора в виджете. Кроме самих вариантов ими
// бывают кнопки самого виджета и вариант со свободным ответом: курсор встаёт на
// них наравне с вариантами, и счёт шагов до цели обязан их учитывать
// (проверено на живой панели).
type tmuxPick struct {
	Text string `json:"text"`
	// Desc это пояснение под вариантом, как его печатает клиент. Экран
	// показывает его второй строкой мельче: без пояснений выбор делается
	// вслепую, а в панели они прежде терялись вовсе.
	Desc string `json:"desc,omitempty"`
	// Mark это состояние флажка: on либо off у вопроса с множественным
	// выбором, пусто там, где флажков нет вовсе.
	Mark string `json:"mark,omitempty"`
	// Kind это служебный вид пункта: pickNext и pickSubmit это кнопки самого
	// виджета, pickFree свободный ответ, pickChat выход в разговор. Пусто у
	// обычного варианта. Слова этих пунктов клиент печатает по-английски, и
	// показывает их экран своими словами, а не пересказом: вид называется тут,
	// перевод живёт на экране.
	Kind string `json:"kind,omitempty"`
}

// tmuxSaid это строка сводки: вопрос и данный на него ответ.
type tmuxSaid struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// tmuxStep это шаг опроса: имя, признак отвеченного и признак того, что
// открыт сейчас именно он.
type tmuxStep struct {
	Name string `json:"name"`
	Done bool   `json:"done,omitempty"`
	Now  bool   `json:"now,omitempty"`
}

const (
	askKeysDigit  = "digit"
	askKeysArrows = "arrows"
	askKindReview = "review"

	pickNext   = "next"
	pickSubmit = "submit"
	pickFree   = "free"
	pickChat   = "chat"
)

// askOptionRe ловит строку варианта клиента: номер с точкой и текст, а перед
// выбранным пунктом стоит знак курсора. Знаки тут чужие, их печатает клиент, и
// сверяются они как есть.
var askOptionRe = regexp.MustCompile("^\\s*(\u276f\\s*)?(\\d+)\\.\\s+(\\S.*?)\\s*$")

// askCursorRe ловит строку варианта без номера: свежий клиент печатает вопрос
// доверия каталогу одним знаком курсора, а пункты отбивает отступом, и номеров
// у них нет вовсе (живой снимок 2026-08-28: «\u276f No, exit» и следом «  Yes, I
// trust this folder»). Прежний разбор такой виджет не видел, и человек снова
// оставался перед тишиной: живой случай двух застрявших чатов xr-proxy.
var askCursorRe = regexp.MustCompile("^(\\s*)\u276f (\\S.*?)\\s*$")

// askMarkRe отрезает флажок множественного выбора от текста варианта: сам
// флажок это состояние, а не часть слов. Отмеченный флажок клиент печатает и
// буквой, и знаком галочки (живая проверка: пустой это «[ ]», отмеченный
// «[\u2714]»), и знаки эти чужие, сверяются как есть.
var askMarkRe = regexp.MustCompile("^\\[( |x|X|\\*|\u2714|\u2713)\\]\\s*(.*)$")

// askTailRe отрезает галочку выбранного варианта у вопроса с одиночным
// выбором: там клиент ставит её в конце строки, а не флажком в начале.
var askTailRe = regexp.MustCompile("^(.*?)\\s+[\u2714\u2713]$")

// askSubmitRe узнаёт кнопки самого виджета. Слова тут чужие, их печатает
// клиент, и список закрытый нарочно: всякая безымянная строка в кнопки не
// годится, иначе ими станут пояснения под вариантами.
var askSubmitRe = regexp.MustCompile("^\\s*(\u276f\\s*)?(Next|Submit|Done|Continue)\\s*$")

// askHintRe узнаёт подсказку под виджетом: ею клиент кончает свой блок, и
// дальше идут уже строки разговора.
var askHintRe = regexp.MustCompile(`(Enter to select|Enter to confirm|to navigate|Esc to cancel|Arrow keys)`)

// askArrowsRe отличает подсказку опроса от подсказки вопроса доверия. У первого
// клиент пишет про ход стрелками и Tab, и номер пункта там не работает вовсе; у
// второго сказано только «Enter to confirm», и пункт выбирается номером
// (проверено на живых панелях обоих видов).
var askArrowsRe = regexp.MustCompile(`(Arrow keys|to navigate|Tab/)`)

// askStepRe узнаёт полосу шагов опроса: клиент печатает её значками флажка и
// галочки, обрамляя стрелками перехода между шагами.
var askStepRe = regexp.MustCompile("[\u2610\u2611\u2612\u2714\u2713]")

// askNowRe находит подсвеченный кусок строки: открытый шаг клиент красит
// фоном, и в простом снимке (без -e) он неотличим от остальных. Оттого снимок
// и снимается с раскраской: без неё панель не знала бы, какой шаг открыт, и
// переход по табам считать было бы не от чего.
var askNowRe = regexp.MustCompile("\u001b\\[48;5;\\d+m(.*?)\u001b\\[49m")

// askEscRe вычищает раскраску из строки: дальше разбору нужны одни слова.
var askEscRe = regexp.MustCompile("\u001b\\[[0-9;]*[A-Za-z]")

// askSaidQRe и askSaidARe разбирают сводку ответов: вопрос помечен кружком,
// ответ стрелкой. Знаки чужие, печатает их клиент.
var askSaidQRe = regexp.MustCompile("^\\s*\u25cf\\s*(.+?)\\s*$")
var askSaidARe = regexp.MustCompile("^\\s*\u2192\\s*(.+?)\\s*$")

// askWarnRe узнаёт предупреждение сводки.
var askWarnRe = regexp.MustCompile("^\\s*\u26a0\\s*(.+?)\\s*$")

// askReviewRe узнаёт саму сводку: ею виджет кончает опрос.
var askReviewRe = regexp.MustCompile(`(Review your answers|Ready to submit)`)

// askFreeWords это слова варианта со свободным ответом. Их печатает клиент, и
// сверяются они как есть.
var askFreeWords = []string{"type something", "type your own"}

// askPromptRe узнаёт строку ввода клиента: знак курсора и следом слова, а не
// номер варианта. Ею клиент отбивает свой блок от разговора, и блок вариантов
// через неё не тянется: выше строки ввода лежит уже прочитанный текст.
var askPromptRe = regexp.MustCompile("^\\s*\u276f\\s+\\S")

// askGapMax это сколько строк подряд внутри блока вариантов не быть вариантом.
// Пояснение под вариантом занимает строку, а кнопку отправки от последнего
// варианта отделяет ещё и рамка: блок рвётся не всякой чужой строкой, иначе
// опрос агента разбирался в один вариант и до человека не доезжал вовсе
// (живой случай: панель не показывала вопрос, а человек писал реплики в
// клиента, который ждал выбора).
const askGapMax = 3

// parseTmuxAsk разбирает снимок панели. Вопросом считается блок вариантов, а
// текстом вопроса непустые строки над ним: клиент печатает вопрос абзацем, а не
// одной строкой. Нет вариантов, значит и вопроса нет: молчащий или работающий
// клиент сюда не попадает.
func parseTmuxAsk(text string) tmuxAsk {
	raw := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	lines := make([]string, len(raw))
	for i, ln := range raw {
		lines[i] = askEscRe.ReplaceAllString(ln, "")
	}
	// Блоков вариантов на панели бывает несколько: над виджетом стоит и вывод
	// клиента, и эхо реплики человека, и оба они бывают нумерованными. Берётся
	// нижний блок со следом виджета: на нём клиент и стоит, а верхние это
	// прочитанный текст.
	blocks := askBlocks(lines)
	if bare := askBareBlocks(lines); len(bare) > 0 {
		blocks = append(blocks, bare...)
		sort.Slice(blocks, func(i, j int) bool { return blocks[i].first < blocks[j].first })
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if len(b.ask.Options) < 2 {
			continue
		}
		ask := b.ask
		said := askReadAbove(&ask, lines, raw, b.first)
		if !askOnWidget(b, ask) {
			continue
		}
		ask.Text = truncate(strings.Join(said, " "), 400)
		if ask.Kind == askKindReview {
			// У сводки свой заголовок: пересказ английских строк виджета
			// человеку ни к чему, а сами ответы стоят полем Said.
			ask.Text = ""
		}
		return ask
	}
	return tmuxAsk{}
}

// askBlock это один блок вариантов панели: номер строки, с которой он начался,
// сами остановки курсора и признак того, что кончился он подсказкой клиента.
type askBlock struct {
	first int
	ask   tmuxAsk
	hint  bool
}

// askBlocks режет панель на блоки вариантов. Блок рвётся чужими строками сверх
// askGapMax и кончается подсказкой навигации; пустая строка и рамка его не
// рвут, ими клиент отбивает кнопку отправки от последнего варианта.
func askBlocks(lines []string) []askBlock {
	var out []askBlock
	cur := askBlock{first: -1, ask: tmuxAsk{Keys: askKeysDigit}}
	gap := 0
	shut := func(hint bool) {
		if cur.first >= 0 {
			cur.hint = hint
			out = append(out, cur)
		}
		cur = askBlock{first: -1, ask: tmuxAsk{Keys: askKeysDigit}}
		gap = 0
	}
	for i, ln := range lines {
		if cur.first >= 0 && askHintRe.MatchString(ln) {
			// Подсказка это конец виджета и заодно ответ на вопрос, чем в нём
			// отвечают: про стрелки клиент пишет там, где номер не работает.
			if askArrowsRe.MatchString(ln) {
				cur.ask.Keys = askKeysArrows
			}
			shut(true)
			continue
		}
		pick, ok := parseAskLine(ln)
		if !ok {
			if cur.first < 0 {
				continue
			}
			if askPromptRe.MatchString(ln) {
				// Строка ввода клиента: блок кончился на ней, что бы ни
				// стояло ниже.
				shut(false)
				continue
			}
			bare := strings.TrimSpace(ln)
			if bare == "" || strings.Trim(bare, frameRunes+" ") == "" {
				continue
			}
			// Всё прочее внутри блока это пояснение под последним вариантом:
			// клиент печатает его строкой ниже и с отступом.
			gap++
			if gap > askGapMax {
				shut(false)
				continue
			}
			if len(cur.ask.Options) > 0 {
				last := &cur.ask.Options[len(cur.ask.Options)-1]
				last.Desc = strings.TrimSpace(last.Desc + " " + bare)
			}
			continue
		}
		gap = 0
		if cur.first < 0 {
			cur.first = i
		}
		if pick.cursor {
			cur.ask.At = len(cur.ask.Options) + 1
		}
		cur.ask.Options = append(cur.ask.Options, pick.pick)
	}
	shut(false)
	return out
}

// askBareBlocks находит блок вариантов без номеров. Такой виджет клиент рисует
// одним знаком курсора: выбранный пункт помечен «\u276f », остальные стоят под ним
// с тем же отступом. Опорой служит сам курсор, а соседями считаются только
// строки, чей текст начинается ровно в том же столбце: без этого блоком стал бы
// всякий абзац под строкой ввода клиента. Рубеж виджета тот же, что и у
// нумерованного блока, и держит его askOnWidget: без подсказки навигации под
// блоком показывать нечего.
func askBareBlocks(lines []string) []askBlock {
	var out []askBlock
	for i, ln := range lines {
		m := askCursorRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		// Нумерованный вариант и кнопку виджета читает свой разбор: тут только
		// то, что ему не далось.
		if _, ok := parseAskLine(ln); ok {
			continue
		}
		col := len(m[1]) + 2
		first, last := i, i
		for j := i - 1; j >= 0 && askBareSame(lines[j], col); j-- {
			first = j
		}
		for j := i + 1; j < len(lines) && askBareSame(lines[j], col); j++ {
			last = j
		}
		if last-first < 1 {
			continue
		}
		b := askBlock{first: first, ask: tmuxAsk{Keys: askKeysArrows}, hint: askHintBelow(lines, last)}
		for j := first; j <= last; j++ {
			// У строки с курсором текст уже отобран разбором: столбец тут
			// общий, а знак курсора шире пробела, и резать её по столбцу
			// значило бы резать посреди знака.
			text := m[2]
			if j != i {
				text = strings.TrimSpace(lines[j][col:])
			}
			b.ask.Options = append(b.ask.Options, tmuxPick{Text: text, Kind: pickKindOf(text)})
		}
		b.ask.At = i - first + 1
		out = append(out, b)
	}
	return out
}

// askBareSame отвечает, стоит ли строка соседним пунктом того же блока: текст
// начинается ровно в столбце col, а сама строка ни курсором, ни номером, ни
// подсказкой не помечена.
func askBareSame(ln string, col int) bool {
	if len(ln) <= col || strings.TrimSpace(ln) == "" {
		return false
	}
	if strings.TrimLeft(ln[:col], " ") != "" || ln[col] == ' ' {
		return false
	}
	if askHintRe.MatchString(ln) {
		return false
	}
	_, ok := parseAskLine(ln)
	return !ok
}

// askHintBelow ищет подсказку навигации под блоком: ею клиент кончает виджет,
// и пустые строки между ними бывают. Первая непустая строка не подсказка
// значит, что блок кончился чем-то другим, и виджетом он не считается.
func askHintBelow(lines []string, last int) bool {
	for j := last + 1; j < len(lines) && j <= last+askGapMax+1; j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		return askHintRe.MatchString(lines[j])
	}
	return false
}

// askReadAbove читает то, что клиент написал над блоком: полосу шагов, сводку
// ответов и сам текст вопроса. Текст это весь абзац до рамки, а не последняя
// его строка: обрывать сбор на первой пустой строке значило бы оставить от
// вопроса одно «Security guide» вместо самого вопроса и каталога (живая
// проверка на застрявшей сессии).
func askReadAbove(ask *tmuxAsk, lines, raw []string, first int) []string {
	var said []string
	for i := first - 1; i >= 0 && len(said) < 10; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		if strings.Trim(ln, frameRunes+" ") == "" {
			break
		}
		// Полоса шагов это не слова вопроса: она едет своим полем, иначе в
		// тексте вопроса оказывались бы значки флажков. Открытый шаг виден
		// только в раскрашенном снимке, поэтому берётся исходная строка.
		if steps := parseAskSteps(ln, raw[i]); len(steps) > 0 {
			ask.Steps = steps
			continue
		}
		if m := askWarnRe.FindStringSubmatch(ln); m != nil {
			ask.Warn = m[1]
			ask.Kind = askKindReview
			continue
		}
		if m := askSaidARe.FindStringSubmatch(ln); m != nil {
			ask.Said = append([]tmuxSaid{{A: m[1]}}, ask.Said...)
			continue
		}
		if m := askSaidQRe.FindStringSubmatch(ln); m != nil {
			if len(ask.Said) > 0 && ask.Said[0].Q == "" {
				ask.Said[0].Q = m[1]
			} else {
				ask.Said = append([]tmuxSaid{{Q: m[1]}}, ask.Said...)
			}
			continue
		}
		if askReviewRe.MatchString(ln) {
			ask.Kind = askKindReview
			continue
		}
		said = append([]string{ln}, said...)
	}
	return said
}

// askOnWidget отвечает на вопрос, стоит ли клиент правда на своём виджете и
// ждёт ли ввода. Никакой эвристики по форме строк тут нет и быть не может:
// пронумерованным или маркированным списком клиент печатает и собственный
// ответ, и эхо реплики человека, а строка ввода начинается с того же знака
// курсора, что и выбранный вариант. Живых случая два: реплика человека из трёх
// пунктов приехала в панель блоком «Клиент ждёт ответа», а следом тем же блоком
// приехал список задач из ответа агента, обрезанный по ширине панели.
//
// Опора только на то, что печатает сам виджет и чего в выводе агента не
// бывает. Родов опоры два. Первый и обычный это подсказка навигации под
// блоком («Enter to confirm», «Tab/Arrow keys to navigate»). Второй это сводка
// ответов, которой виджет кончает опрос: своей подсказки она не печатает вовсе
// (живой снимок сессии chat-98), а узнаётся не хуже, потому что несёт и свой
// заголовок («Review your answers»), и пары «вопрос-ответ» значками виджета.
// Без того и другого блока нет: показать чужой текст кнопками хуже, чем
// промолчать.
func askOnWidget(b askBlock, ask tmuxAsk) bool {
	if b.hint {
		return true
	}
	return ask.Kind == askKindReview && (len(ask.Said) > 0 || ask.Warn != "")
}

// askLine это разобранная строка виджета: сама остановка и признак курсора.
type askLine struct {
	pick   tmuxPick
	cursor bool
}

// parseAskLine разбирает строку панели в остановку курсора: нумерованный
// вариант либо кнопку самого виджета.
func parseAskLine(ln string) (askLine, bool) {
	if m := askOptionRe.FindStringSubmatch(ln); m != nil {
		out := askLine{cursor: m[1] != "", pick: tmuxPick{Text: m[3]}}
		if mark := askMarkRe.FindStringSubmatch(out.pick.Text); mark != nil {
			out.pick.Mark = "off"
			if mark[1] != " " {
				out.pick.Mark = "on"
			}
			out.pick.Text = strings.TrimSpace(mark[2])
		} else if tail := askTailRe.FindStringSubmatch(out.pick.Text); tail != nil {
			// У вопроса с одиночным выбором флажков нет вовсе, а выбранный
			// вариант клиент помечает галочкой в конце строки (живая проверка).
			// Знак этот состояние, а не часть слов.
			out.pick.Mark = "on"
			out.pick.Text = strings.TrimSpace(tail[1])
		}
		out.pick.Kind = pickKindOf(out.pick.Text)
		return out, true
	}
	if m := askSubmitRe.FindStringSubmatch(ln); m != nil {
		kind := pickNext
		if m[2] != "Next" {
			kind = pickSubmit
		}
		return askLine{cursor: m[1] != "", pick: tmuxPick{Text: m[2], Kind: kind}}, true
	}
	return askLine{}, false
}

// pickKindOf называет служебный вид пункта по словам клиента.
func pickKindOf(text string) string {
	low := strings.ToLower(strings.TrimRight(strings.TrimSpace(text), "."))
	for _, word := range askFreeWords {
		if strings.Contains(low, word) {
			return pickFree
		}
	}
	switch {
	case strings.Contains(low, "chat about this"):
		return pickChat
	case low == "submit answers":
		return pickSubmit
	case low == "cancel":
		return ""
	}
	return ""
}

// parseAskSteps разбирает полосу шагов. Пустой список значит, что строка это не
// полоса, а обычные слова вопроса. raw это та же строка с раскраской: открытый
// шаг клиент помечает только фоном.
func parseAskSteps(ln, raw string) []tmuxStep {
	if !askStepRe.MatchString(ln) {
		return nil
	}
	now := ""
	if m := askNowRe.FindStringSubmatch(raw); m != nil {
		now = strings.TrimSpace(askEscRe.ReplaceAllString(m[1], ""))
	}
	var out []tmuxStep
	for _, part := range strings.Split(ln, "  ") {
		part = strings.TrimSpace(part)
		if part == "" || part == "\u2190" || part == "\u2192" {
			continue
		}
		// Шагом считается только кусок, начатый значком флажка: значок в
		// середине строки это слова, а не полоса шагов, и без этой проверки
		// полосой оказывался бы всякий абзац с галочкой.
		if !strings.ContainsAny(string([]rune(part)[:1]), "\u2610\u2611\u2612\u2714\u2713") {
			continue
		}
		done := strings.ContainsAny(part, "\u2611\u2612\u2714\u2713")
		name := strings.TrimSpace(strings.TrimLeft(part, "\u2610\u2611\u2612\u2714\u2713 "))
		if name == "" {
			continue
		}
		out = append(out, tmuxStep{Name: name, Done: done, Now: now != "" && part == now})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// tmuxAskOf снимает панель сессии и разбирает её на вопрос. Ошибка тут не
// поломка: сессии может уже не быть, и вопроса тогда нет.
func tmuxAskOf(name string) tmuxAsk {
	// Снимок берётся с раскраской (-e): открытый шаг опроса клиент помечает
	// только фоном, и без раскраски панель не знала бы, на каком табе стоит
	// человек, а переход по табам считать было бы не от чего.
	out, err := runProc("tmux", "capture-pane", "-p", "-e", "-t", "="+name+":")
	if err != nil {
		return tmuxAsk{}
	}
	return parseTmuxAsk(string(out))
}

// tmuxStepTo переводит опрос на шаг step (счёт с единицы): шаги у клиента это
// табы, между которыми ходят стрелками влево-вправо, и ответа на текущий шаг
// для перехода не нужно (проверено на живой панели).
func tmuxStepTo(name string, ask tmuxAsk, step int) error {
	at := "=" + name + ":"
	now := 0
	for i, s := range ask.Steps {
		if s.Now {
			now = i + 1
		}
	}
	if now == 0 {
		return fmt.Errorf("открытый шаг опроса не виден: переходить не от чего")
	}
	move, key := step-now, "Right"
	if move < 0 {
		move, key = -move, "Left"
	}
	for i := 0; i < move; i++ {
		if _, err := runProc("tmux", "send-keys", "-t", at, key); err != nil {
			return err
		}
	}
	return nil
}

// errAskBlind это отказ посчитать ход по виджету: варианты на панели видны, а
// знака курсора у них не нашлось (кривая перерисовка клиента, съехавший
// снимок). Слепые стрелки промахиваются мимо пункта, и Enter подтверждает
// чужой выбор, поэтому дорога называет этот отказ отдельно и едет запасной.
var errAskBlind = errors.New("курсора в виджете не видно: ходить стрелками не от чего")

// tmuxAnswer отвечает на вопрос клиента. Способ ответа берётся у самого
// виджета, а не выбирается наугад.
//
// У вопроса доверия каталогу пункт выбирается номером, и стрелками тут не
// ходим нарочно: номер выбирает пункт сам, а лишние нажатия уехали бы в чужой
// вопрос, если человек ответил быстрее.
//
// У опроса агента номера не работают вовсе: клиент под ним печатает «Enter to
// select, Tab/Arrow keys to navigate», и ход идёт стрелками от той остановки,
// на которой стоит курсор. Остановками там бывают и кнопка отправки, и вариант
// со свободным ответом, поэтому счёт ведётся по разобранному списку, а не по
// номерам пунктов (проверено на живой панели: Down от первого варианта ведёт
// ко второму, а перед «Chat about this» стоит ещё и «Next»).
//
// text это свободный ответ: он подаётся клиенту после выбора пункта, который
// открывает поле ввода.
func tmuxAnswer(name string, ask tmuxAsk, option int, text string) error {
	at := "=" + name + ":"
	if ask.Keys != askKeysArrows {
		if _, err := runProc("tmux", "send-keys", "-t", at, strconv.Itoa(option)); err != nil {
			return err
		}
		if _, err := runProc("tmux", "send-keys", "-t", at, "Enter"); err != nil {
			return err
		}
		return tmuxAnswerText(at, text)
	}
	if ask.At < 1 {
		return errAskBlind
	}
	step, key := option-ask.At, "Down"
	if step < 0 {
		step, key = -step, "Up"
	}
	for i := 0; i < step; i++ {
		if _, err := runProc("tmux", "send-keys", "-t", at, key); err != nil {
			return err
		}
	}
	if _, err := runProc("tmux", "send-keys", "-t", at, "Enter"); err != nil {
		return err
	}
	return tmuxAnswerText(at, text)
}

// tmuxAnswerText досылает свободный ответ: текст подаётся дословно (-l), иначе
// tmux прочитал бы слова как имена клавиш, и следом Enter.
func tmuxAnswerText(at, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := runProc("tmux", "send-keys", "-t", at, "-l", text); err != nil {
		return err
	}
	_, err := runProc("tmux", "send-keys", "-t", at, "Enter")
	return err
}

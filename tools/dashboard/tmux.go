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
// на котором стоит курсор клиента, и чем виджет отвечает.
type tmuxAsk struct {
	Text    string     `json:"text,omitempty"`
	Options []tmuxPick `json:"options,omitempty"`
	At      int        `json:"at,omitempty"`
	// Keys называет способ ответа: askKeysDigit это выбор номером, askKeysArrows
	// это ход стрелками и Enter. Вопрос доверия каталогу приходит и тем и
	// другим, смотря по возрасту клиента. Способ читается с самой панели, а не
	// угадывается: клиент печатает подсказку навигации под виджетом.
	Keys string `json:"keys,omitempty"`
}

// tmuxPick это одна остановка курсора в виджете клиента.
type tmuxPick struct {
	Text string `json:"text"`
	// Desc это пояснение под вариантом, как его печатает клиент. Экран
	// показывает его второй строкой мельче: без пояснений выбор делается
	// вслепую, а в панели они прежде терялись вовсе.
	Desc string `json:"desc,omitempty"`
}

const (
	askKeysDigit  = "digit"
	askKeysArrows = "arrows"
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

// askHintRe узнаёт подсказку под виджетом: ею клиент кончает свой блок, и
// дальше идут уже строки разговора.
var askHintRe = regexp.MustCompile(`(Enter to select|Enter to confirm|to navigate|Esc to cancel|Arrow keys)`)

// askArrowsRe отличает подсказку виджета со стрелками от подсказки виджета с
// номерами. У первого клиент пишет про ход стрелками и Tab, и номер пункта там
// не работает вовсе; у второго сказано только «Enter to confirm», и пункт
// выбирается номером (проверено на живых панелях обоих видов).
var askArrowsRe = regexp.MustCompile(`(Arrow keys|to navigate|Tab/)`)

// askEscRe вычищает раскраску из строки: дальше разбору нужны одни слова.
var askEscRe = regexp.MustCompile("\u001b\\[[0-9;]*[A-Za-z]")

// askPromptRe узнаёт строку ввода клиента: знак курсора и следом слова, а не
// номер варианта. Ею клиент отбивает свой блок от разговора, и блок вариантов
// через неё не тянется: выше строки ввода лежит уже прочитанный текст.
var askPromptRe = regexp.MustCompile("^\\s*\u276f\\s+\\S")

// askGapMax это сколько строк подряд внутри блока вариантов не быть вариантом.
// Пояснение под вариантом занимает строку, а кнопку отправки от последнего
// варианта отделяет ещё и рамка: блок рвётся не всякой чужой строкой, иначе
// вопрос с пояснениями разбирался бы в один вариант и до человека не доезжал.
const askGapMax = 3

// parseTmuxAsk разбирает снимок панели. Вопросом считается блок вариантов, а
// текстом вопроса непустые строки над ним: клиент печатает вопрос абзацем, а не
// одной строкой. Нет вариантов, значит и вопроса нет: молчащий или работающий
// клиент сюда не попадает.
func parseTmuxAsk(text string) tmuxAsk {
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	for i, ln := range lines {
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
		if !b.hint {
			// Рубеж виджета: без подсказки навигации под блоком показывать
			// нечего. Никакой догадки по форме строк тут нет и быть не может.
			// Пронумерованным списком клиент печатает и собственный ответ, и
			// эхо реплики человека, а строка ввода начинается с того же знака
			// курсора, что и выбранный вариант.
			continue
		}
		ask := b.ask
		ask.Text = truncate(strings.Join(askReadAbove(lines, b.first), " "), 400)
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
			b.ask.Options = append(b.ask.Options, tmuxPick{Text: text})
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

// askReadAbove читает то, что клиент написал над блоком. Текст это весь абзац
// до рамки, а не последняя его строка: обрывать сбор на первой пустой строке
// значило бы оставить от вопроса одно «Security guide» вместо самого вопроса и
// каталога (живая проверка на застрявшей сессии).
func askReadAbove(lines []string, first int) []string {
	var said []string
	for i := first - 1; i >= 0 && len(said) < 10; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		if strings.Trim(ln, frameRunes+" ") == "" {
			break
		}
		said = append([]string{ln}, said...)
	}
	return said
}

// askLine это разобранная строка виджета: сама остановка и признак курсора.
type askLine struct {
	pick   tmuxPick
	cursor bool
}

// parseAskLine разбирает строку панели в остановку курсора: нумерованный
// вариант клиента.
func parseAskLine(ln string) (askLine, bool) {
	if m := askOptionRe.FindStringSubmatch(ln); m != nil {
		return askLine{cursor: m[1] != "", pick: tmuxPick{Text: m[3]}}, true
	}
	return askLine{}, false
}

// tmuxAskOf снимает панель сессии и разбирает её на вопрос. Ошибка тут не
// поломка: сессии может уже не быть, и вопроса тогда нет.
func tmuxAskOf(name string) tmuxAsk {
	out, err := runProc("tmux", "capture-pane", "-p", "-t", "="+name+":")
	if err != nil {
		return tmuxAsk{}
	}
	return parseTmuxAsk(string(out))
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
// У виджета без номеров они не работают вовсе: клиент под ним печатает «Enter
// to select, Tab/Arrow keys to navigate», и ход идёт стрелками от той
// остановки, на которой стоит курсор. Счёт ведётся по разобранному списку, а не
// по номерам пунктов.
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

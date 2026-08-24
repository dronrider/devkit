package main

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
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
	// вопрос доверия каталогу), askKeysArrows это ход стрелками и Enter (так
	// устроен опрос агента с вариантами). Способ читается с самой панели, а не
	// угадывается: клиент печатает подсказку навигации под виджетом.
	Keys string `json:"keys,omitempty"`
	// Steps это полоса шагов многошагового опроса, как её печатает клиент:
	// ответ на шаг приводит следующий, и без полосы человек не понимает, почему
	// разговор не продолжился.
	Steps []tmuxStep `json:"steps,omitempty"`
}

// tmuxPick это одна остановка курсора в виджете. Кроме самих вариантов ими
// бывают кнопка отправки («Next» многошагового опроса) и вариант со свободным
// ответом («Type something»): курсор встаёт на них наравне с вариантами, и
// счёт шагов до цели обязан их учитывать (проверено на живой панели).
type tmuxPick struct {
	Text string `json:"text"`
	// Mark это состояние флажка: on либо off у виджета с множественным
	// выбором, пусто там, где флажков нет вовсе.
	Mark string `json:"mark,omitempty"`
	// Free значит свободный ответ: выбор такого пункта открывает поле, и текст
	// человека едет клиенту следом.
	Free bool `json:"free,omitempty"`
	// Submit значит кнопку отправки виджета: сама по себе она не вариант, но
	// курсор на ней останавливается, и без неё многошаговый опрос не сдвинуть.
	Submit bool `json:"submit,omitempty"`
}

// tmuxStep это шаг опроса: имя и признак пройденного.
type tmuxStep struct {
	Name string `json:"name"`
	Done bool   `json:"done,omitempty"`
}

const (
	askKeysDigit  = "digit"
	askKeysArrows = "arrows"
)

// askOptionRe ловит строку варианта клиента: номер с точкой и текст, а перед
// выбранным пунктом стоит знак курсора. Знаки тут чужие, их печатает клиент, и
// сверяются они как есть.
var askOptionRe = regexp.MustCompile("^\\s*(\u276f\\s*)?(\\d+)\\.\\s+(\\S.*?)\\s*$")

// askMarkRe отрезает флажок множественного выбора от текста варианта: сам
// флажок это состояние, а не часть слов. Отмеченный флажок клиент печатает и
// буквой, и знаком галочки (живая проверка: пустой это «[ ]», отмеченный
// «[\u2714]»), и знаки эти чужие, сверяются как есть.
var askMarkRe = regexp.MustCompile("^\\[( |x|X|\\*|\u2714|\u2713)\\]\\s*(.*)$")

// askSubmitRe узнаёт кнопку отправки виджета. Слова тут чужие, их печатает
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

// askFreeWords это слова варианта со свободным ответом. Их печатает клиент, и
// сверяются они как есть.
var askFreeWords = []string{"type something", "type your own", "other"}

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
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	first := -1
	gap := 0
	ask := tmuxAsk{Keys: askKeysDigit}
	for i, ln := range lines {
		if first >= 0 && askHintRe.MatchString(ln) {
			// Подсказка это конец виджета и заодно ответ на вопрос, чем в нём
			// отвечают: про стрелки клиент пишет там, где номер не работает.
			if askArrowsRe.MatchString(ln) {
				ask.Keys = askKeysArrows
			}
			break
		}
		pick, ok := parseAskLine(ln)
		if !ok {
			if first < 0 {
				continue
			}
			// Пустая строка и рамка блок не рвут: рамкой клиент отбивает
			// кнопку отправки от последнего варианта.
			bare := strings.TrimSpace(ln)
			if bare == "" || strings.Trim(bare, frameRunes+" ") == "" {
				continue
			}
			gap++
			if gap > askGapMax {
				break
			}
			continue
		}
		gap = 0
		if first < 0 {
			first = i
		}
		if pick.cursor {
			ask.At = len(ask.Options) + 1
		}
		ask.Options = append(ask.Options, pick.pick)
	}
	if len(ask.Options) < 2 {
		return tmuxAsk{}
	}
	// Текст вопроса это всё, что клиент написал над вариантами до своей рамки:
	// абзацы он разделяет пустыми строками, и обрывать сбор на первой из них
	// значило бы оставить от вопроса одну последнюю строку («Security guide»
	// вместо самого вопроса и каталога, живая проверка на застрявшей сессии).
	var said []string
	for i := first - 1; i >= 0 && len(said) < 8; i-- {
		ln := strings.TrimSpace(lines[i])
		if ln == "" {
			continue
		}
		if strings.Trim(ln, frameRunes+" ") == "" {
			break
		}
		// Полоса шагов это не слова вопроса: она едет своим полем, иначе в
		// тексте вопроса оказывались бы значки флажков.
		if steps := parseAskSteps(ln); len(steps) > 0 {
			ask.Steps = steps
			continue
		}
		said = append([]string{ln}, said...)
	}
	ask.Text = truncate(strings.Join(said, " "), 400)
	return ask
}

// askLine это разобранная строка виджета: сама остановка и признак курсора.
type askLine struct {
	pick   tmuxPick
	cursor bool
}

// parseAskLine разбирает строку панели в остановку курсора: нумерованный
// вариант либо кнопку отправки.
func parseAskLine(ln string) (askLine, bool) {
	if m := askOptionRe.FindStringSubmatch(ln); m != nil {
		out := askLine{cursor: m[1] != "", pick: tmuxPick{Text: m[3]}}
		if mark := askMarkRe.FindStringSubmatch(out.pick.Text); mark != nil {
			out.pick.Mark = "off"
			if mark[1] != " " {
				out.pick.Mark = "on"
			}
			out.pick.Text = strings.TrimSpace(mark[2])
		}
		low := strings.ToLower(out.pick.Text)
		for _, word := range askFreeWords {
			if strings.Contains(low, word) {
				out.pick.Free = true
				break
			}
		}
		return out, true
	}
	if m := askSubmitRe.FindStringSubmatch(ln); m != nil {
		return askLine{cursor: m[1] != "", pick: tmuxPick{Text: m[2], Submit: true}}, true
	}
	return askLine{}, false
}

// parseAskSteps разбирает полосу шагов. Пустой список значит, что строка это не
// полоса, а обычные слова вопроса.
func parseAskSteps(ln string) []tmuxStep {
	if !askStepRe.MatchString(ln) {
		return nil
	}
	var out []tmuxStep
	for _, part := range strings.Split(ln, "  ") {
		part = strings.TrimSpace(part)
		if part == "" || part == "\u2190" || part == "\u2192" {
			continue
		}
		done := strings.ContainsAny(part, "\u2611\u2612\u2714\u2713")
		name := strings.TrimSpace(strings.TrimLeft(part, "\u2610\u2611\u2612\u2714\u2713 "))
		if name == "" {
			continue
		}
		out = append(out, tmuxStep{Name: name, Done: done})
	}
	if len(out) < 2 {
		return nil
	}
	return out
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
		return fmt.Errorf("курсора в виджете не видно: ходить стрелками не от чего")
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

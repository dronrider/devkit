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

// tmuxAsk это разобранный вопрос: сам текст, варианты по порядку и номер того,
// на котором стоит курсор клиента.
type tmuxAsk struct {
	Text    string   `json:"text,omitempty"`
	Options []string `json:"options,omitempty"`
	At      int      `json:"at,omitempty"`
}

// askOptionRe ловит строку варианта клиента: номер с точкой и текст, а перед
// выбранным пунктом стоит знак курсора. Знаки тут чужие, их печатает клиент, и
// сверяются они как есть.
var askOptionRe = regexp.MustCompile("^\\s*(\u276f\\s*)?(\\d+)\\.\\s+(\\S.*?)\\s*$")

// parseTmuxAsk разбирает снимок панели. Вопросом считается блок нумерованных
// вариантов подряд, а текстом вопроса непустые строки над ним: клиент печатает
// вопрос абзацем, а не одной строкой. Нет вариантов, значит и вопроса нет:
// молчащий или работающий клиент сюда не попадает.
func parseTmuxAsk(text string) tmuxAsk {
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	first, last := -1, -1
	ask := tmuxAsk{}
	for i, ln := range lines {
		m := askOptionRe.FindStringSubmatch(ln)
		if m == nil {
			// Пустая строка внутри блока вариантов его не рвёт, а любая другая
			// рвёт: варианты идут подряд.
			if first >= 0 && strings.TrimSpace(ln) == "" {
				continue
			}
			if first >= 0 {
				break
			}
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		ask.Options = append(ask.Options, m[3])
		if m[1] != "" {
			ask.At = len(ask.Options)
		}
	}
	if len(ask.Options) < 2 {
		return tmuxAsk{}
	}
	_ = last
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
		said = append([]string{ln}, said...)
	}
	ask.Text = truncate(strings.Join(said, " "), 400)
	return ask
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

// tmuxAnswer отвечает на вопрос клиента: номер пункта и Enter. Стрелками тут
// не ходим нарочно, номер выбирает пункт сам, а лишние нажатия уехали бы в
// чужой вопрос, если человек ответил быстрее.
func tmuxAnswer(name string, option int) error {
	if _, err := runProc("tmux", "send-keys", "-t", "="+name+":", strconv.Itoa(option)); err != nil {
		return err
	}
	_, err := runProc("tmux", "send-keys", "-t", "="+name+":", "Enter")
	return err
}

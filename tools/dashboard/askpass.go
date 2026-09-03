package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Помощник ввода пароля (DK-772). Строка с `!` из чата доходит до терминала
// живой сессии, а sudo и ssh в ней сидят без tty: пароль показать некуда, и
// команда отказывает словами про отсутствующий терминал. Дашборд подставляет
// себя помощником (SUDO_ASKPASS/SSH_ASKPASS): sudo зовёт его текстом запроса
// первым аргументом и ждёт пароль в stdout. Помощник стучится в демон HTTP,
// вопрос встаёт в ленте разговора закрытым полем, человек отвечает, ответ
// уезжает в тело ответа помощнику, и тот отдаёт его sudo. Пароль живёт только
// в памяти демона на время ожидания и в этом ответе: в журнал, транскрипт
// сессии и ленту разговора он не попадает.

// askKindPass это вид вопроса помощника в ответе ручки /ask, тем же полем
// kind, каким размечен вопрос агента ("agent") и сводка опроса клиента
// ("review"): панель различает виды одним полем.
const askKindPass = "askpass"

// askpassTimeout это срок ожидания ответа человека. Помощник виснет долго, но
// не вечно: забытый вопрос не должен держать sudo подвешенным до конца хода
// агента. Var, а не const: тест срока укорачивает его, не трогая боевое
// значение.
var askpassTimeout = 120 * time.Second

// askpassDisplay это заглушка X11-дисплея. Без переменной DISPLAY sudo не
// зовёт askpass-помощника вовсе, даже когда SUDO_ASKPASS указан (проверено на
// живой машине). Побочный эффект один: программы с графикой под X11 сочтут
// дисплей живым, а таких в сессиях агента нет.
const askpassDisplay = ":0"

// askpassSecretHeader это заголовок, которым помощник предъявляет секрет
// локального входа. Кука панели тут не годится: помощник не браузер и её не
// носит, а демон узнаёт своего по секрету, который сам же положил на диск на
// старте (writeAskpassSecret).
const askpassSecretHeader = "X-Devkit-Askpass-Secret"

// askpassWait это один вопрос помощника: кому он адресован (ID разговора),
// каким именем звалась его tmux-сессия (резервная зацепка в логах), что
// спросил sudo или ssh, срок и канал, которым доедет ответ. Канал
// буферизован на один кадр: закрывает его ровно одна из трёх сторон, ответ
// панели, отмена или срок, и второй кадр никому не нужен.
type askpassWait struct {
	sid    string
	tmux   string
	prompt string
	since  time.Time
	until  time.Time
	answer chan askpassAnswer
}

// askpassAnswer это то, что помощник в итоге получает: пароль или отмену.
// Пустой Text при Canceled=false не бывает, его отсекает handleChatAskpassAnswer.
type askpassAnswer struct {
	text     string
	canceled bool
}

// askpassAsk это вопрос помощника в виде, который отдаёт GET .../ask: теми
// же именами полей, что tmuxAsk и agentAsk, чтобы панель узнавала вид одним
// полем kind, а не гадала по форме объекта.
type askpassAsk struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Text  string `json:"text"`
	Until int64  `json:"until,omitempty"`
}

// askpassSecretPath и askpassHelperPath это пути секрета и самого помощника
// на машине, оба под настоящим домом (realHome), а не домом самого демона:
// HOME поднятой сессии это именно настоящий дом (launchEnv), и помощник,
// зовущийся из-под sudo с окружением сессии, будет искать оба файла там же.
// Помощника раскладывает `devkitctl doctor --fix` (tools/askpass/askpass.py).
func askpassSecretPath(home string) string {
	return filepath.Join(home, ".devkit", "askpass.local")
}

func askpassHelperPath(home string) string {
	return filepath.Join(home, ".devkit", "askpass.py")
}

// askpassID рождает ID запроса: 8 байт случайности хватает, коллизия внутри
// живой карты ожиданий значения не имеет (запись просто перезапишет себя),
// а срок жизни записи секунды.
func askpassID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	// crypto/rand не должен отказывать, а не должен не значит не может: запасной
	// ID хуже случайного, но не пустой строкой.
	return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
}

// writeAskpassSecret кладёт секрет входа помощника под настоящий дом машины.
// Каталог и файл сужены сразу: там лежит секрет локального канала, который
// на срок ожидания пароля отдаёт разговор помощнику. Секрет рождается заново
// на каждом старте демона (решение DK-772): рестарт демона гасит все
// зависшие ожидания сам собой, и переживший его помощник узнаётся по
// неверному секрету, а не по протухшей записи в памяти.
func writeAskpassSecret(home, token string) error {
	dir := filepath.Join(home, ".devkit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := askpassSecretPath(home)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	// WriteFile не сужает права существующего файла.
	return os.Chmod(path, 0o600)
}

// askpassBegin заводит запись ожидания и возвращает её ID вместе с самой
// записью: демон под замком отдаёт id вызывающему, а дальше ждёт на канале
// уже без замка.
func (s *server) askpassBegin(sid, tmux, prompt string) (string, *askpassWait) {
	now := s.now()
	w := &askpassWait{sid: sid, tmux: tmux, prompt: prompt, since: now,
		until: now.Add(askpassTimeout), answer: make(chan askpassAnswer, 1)}
	s.askpassMu.Lock()
	if s.askpassWaits == nil {
		s.askpassWaits = map[string]*askpassWait{}
	}
	id := askpassID()
	for {
		if _, dup := s.askpassWaits[id]; !dup {
			break
		}
		id = askpassID()
	}
	s.askpassWaits[id] = w
	s.askpassMu.Unlock()
	return id, w
}

// askpassEnd снимает запись ожидания с карты: срок вышел, ответ пришёл, или
// сам запрос помощника оборвался (клиент разъединился).
func (s *server) askpassEnd(id string) {
	s.askpassMu.Lock()
	delete(s.askpassWaits, id)
	s.askpassMu.Unlock()
}

// askpassPending находит вопрос помощника, адресованный этому разговору:
// самый ранний, если их вдруг несколько. Раньше вопроса клиента (tmuxAsk) в
// handleChatAsk стоит именно эта проверка: askpass это не диалог виджета
// клиента, а отдельный канал, и путать их нельзя.
func (s *server) askpassPending(sid string) (askpassAsk, bool) {
	if sid == "" {
		return askpassAsk{}, false
	}
	s.askpassMu.Lock()
	defer s.askpassMu.Unlock()
	var bestID string
	var best *askpassWait
	for id, w := range s.askpassWaits {
		if w.sid != sid {
			continue
		}
		if best == nil || w.since.Before(best.since) {
			bestID, best = id, w
		}
	}
	if best == nil {
		return askpassAsk{}, false
	}
	return askpassAsk{Kind: askKindPass, ID: bestID, Text: best.prompt, Until: best.until.Unix()}, true
}

// askpassSidByTmux ищет разговор по имени его последней tmux-сессии. Это
// резервная дорога помощника: DEVKIT_CHAT пуст у только что поднятой сессии
// (сама она себя ещё не назвала на момент подъёма), а к моменту, когда агент
// доходит до sudo, хук старта уже дописал разговор в реестр под тем же
// именем tmux. Совпадений может быть несколько (имя пережившей себя записи),
// берётся самая свежая по времени последней привязки.
func (s *server) askpassSidByTmux(tmux string) string {
	if tmux == "" {
		return ""
	}
	best, bestTime := "", ""
	for sid, recs := range s.bindsAll() {
		last := sessions.Last(recs)
		if last.Tmux == tmux && last.Time > bestTime {
			best, bestTime = sid, last.Time
		}
	}
	return best
}

// askpassResolve отвечает на конкретный вопрос по ID: панель шлёт то, что
// человек ввёл, или отмену. Второй вызов с тем же ID (срок уже вышел и
// запись снята, либо панель продублировала ответ) отбивается: канал уже
// нашёл своего читателя или уже не имеет его.
func (s *server) askpassResolve(id, text string, canceled bool) bool {
	s.askpassMu.Lock()
	w, ok := s.askpassWaits[id]
	if ok {
		delete(s.askpassWaits, id)
	}
	s.askpassMu.Unlock()
	if !ok {
		return false
	}
	select {
	case w.answer <- askpassAnswer{text: text, canceled: canceled}:
	default:
	}
	return true
}

// askpassAuthorized сверяет секрет заголовка с тем, что демон положил на
// диск при своём старте. Кука тут ни при чём: помощник зовётся из-под sudo с
// окружением сессии, а не из браузера.
func (s *server) askpassAuthorized(r *http.Request) bool {
	got := r.Header.Get(askpassSecretHeader)
	return got != "" && s.askpassSecret != "" && tokenMatch(s.askpassSecret, got)
}

// handleAskpassRequest это вход помощника: он называет разговор (сообщением
// DEVKIT_CHAT либо запасным DEVKIT_TMUX) и текст запроса sudo/ssh, а дальше
// висит на этом ходе до ответа панели, отмены или срока. Тело ответа это
// голый пароль без ничего лишнего: помощник печатает его в stdout, каким
// получил.
func (s *server) handleAskpassRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "жду POST"})
		return
	}
	if !s.askpassAuthorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "секрет помощника неверный: демон перезапустился или файл секрета стал чужим"})
		return
	}
	var body struct {
		Chat   string `json:"chat"`
		Tmux   string `json:"tmux"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"tmux\",\"prompt\"}"})
		return
	}
	sid := strings.TrimSpace(body.Chat)
	tmux := strings.TrimSpace(body.Tmux)
	if sid == "" {
		sid = s.askpassSidByTmux(tmux)
	}
	if sid == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "разговор не назвался: ни DEVKIT_CHAT, ни живая запись реестра по DEVKIT_TMUX"})
		return
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "sudo просит пароль"
	}
	id, wait := s.askpassBegin(sid, tmux, prompt)
	select {
	case ans := <-wait.answer:
		if ans.canceled {
			writeJSON(w, http.StatusGone, map[string]string{"error": "отменено человеком"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"password": ans.text})
	case <-time.After(askpassTimeout):
		s.askpassEnd(id)
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "срок ожидания пароля вышел"})
	case <-r.Context().Done():
		s.askpassEnd(id)
	}
}

// handleChatAskpassAnswer отвечает на вопрос помощника: человек ввёл пароль
// в закрытое поле панели, или нажал «отменить». Дорога отдельная от общей
// handleChatAskAnswer нарочно: там разбор вариантов и слово ответа едет в
// журнал строкой (`s.logf("ответ на вопрос клиента ...: пункт %d (%s)")`), а
// пароль в такую строку попадать не должен.
func (s *server) handleChatAskpassAnswer(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, sid, ok := s.chatSidOf(w, r)
	if !ok {
		return
	}
	var body struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Cancel bool   `json:"cancel"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\",\"text\"}"})
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id вопроса пуст"})
		return
	}
	if !body.Cancel && body.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пароль пуст: отправлять нечего, для отказа есть отмена"})
		return
	}
	if !s.askpassResolve(id, body.Text, body.Cancel) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "вопрос помощника уже закрыт: срок вышел или ответ уже ушёл"})
		return
	}
	word := "принят"
	if body.Cancel {
		word = "отменено"
	}
	// Сам пароль (body.Text) в журнал не идёт, только слово исхода.
	s.logf("вопрос помощника пароля в %s (%s): %s", found.Name, sid, word)
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "canceled": body.Cancel})
}

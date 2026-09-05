package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Сторож поднятой сессии (DK-728). Подъём чата это `tmux new-session -d`, и
// удачей до сих пор считался код возврата tmux, а он говорит одно: сессия
// создана. Клиент, умерший через секунду (нет входа, кончилась квота, чужие
// флаги, не то дерево), уносил сессию с собой, и об этом не узнавал никто:
// ручка уже ответила 200, в журнале стояло «чат поднят», а панель до конца
// опроса обещала разговор, которого не будет.
//
// Тут у подъёма появляется исход. Имя поднятой сессии ложится в свою запись
// (chatStore при имени tmux) с временем подъёма, и дальше за ней смотрят два
// глаза одной парой: опрос панели, который и так спрашивает «родился ли
// диалог», и сторож демона, который ходит по всем поднятым сессиям сам. Пропажа
// имени раньше времени это смерть: она получает слова с причиной, снятой с
// самого терминала, ложится строкой в ленту разговора и кончает ожидание
// подъёма.
//
// Смерть сессии, уже начавшей ход, идёт тем же порядком и отличается только
// словами: разговор у неё свой, и строка встаёт в его ленте.

const (
	// chatWatchStep это шаг обхода сторожа. Секунды тут не нужны: ожидание
	// подъёма и так спрашивает сервер каждые две секунды, а сторож закрывает
	// случай, когда на разговор никто не смотрит.
	chatWatchStep = 5 * time.Second
	// chatTailStep это срок годности снимка панели. Мёртвая сессия панели уже
	// не отдаст, и причину смерти брать неоткуда, кроме снимка, сделанного при
	// жизни; чаще этого срока снимать незачем, дороже выйдет.
	chatTailStep = 20 * time.Second
	// chatTailLines это сколько строк терминала едет в ленту разговора.
	// Причина смерти пишется клиентом последними строками, а вся панель это
	// полсотни строк рамок и подсказок.
	chatTailLines = 12
	// chatTailWidth режет длинную строку: панель терминала бывает широкой, а
	// пузырь ленты узкий.
	chatTailWidth = 200
)

// chatTail это снимок панели живой сессии и время снятия. Живёт он в памяти
// процесса: на диск ему незачем, он нужен ровно до смерти сессии, а перезапуск
// демона снимет панель заново первым же обходом.
type chatTail struct {
	text string
	at   time.Time
}

// chatSecretRe затирает в снимке панели то, что похоже на секрет: длинный
// неразрывный кусок латиницы с цифрами это токен или ключ, а хвост терминала
// едет в журнал ленты и живёт там столько же, сколько разговор.
var chatSecretRe = regexp.MustCompile(`[A-Za-z0-9_-]{28,}`)

// chatRaised отмечает поднятую сессию под присмотр. Зовут её все дороги
// подъёма чата, сразу после удачного tmux new-session: до этой отметки сессии
// для сторожа не существует, и провалившийся подъём смертью не считается.
// Разговор sid называется, когда он уже есть (резюм, реплика в незачатую
// запись): в его ленту и приедет строка о смерти.
//
// Проект нужен смерти, а не подъёму: по нему она находит доску и зовёт
// человека к осиротевшей строке (nolead.go). Реестр сессий тут не помощник,
// умерший клиент в него мог и не успеть назваться.
func (s *server) chatRaised(sess, sid, task, proj string) {
	if sess == "" || !chatKeyRe.MatchString(sess) {
		return
	}
	key := "tmux-" + sess
	st := s.chatStoreRead(key)
	st.Raised = s.now().Unix()
	st.Dead, st.DeadWhy, st.Tail = 0, "", ""
	// Имена окон дашборд переиспользует (chat-1, chat-2, task-DK-100), и заказ
	// дожима, оставшийся от прошлого жильца имени, прервал бы новому разговору
	// первый же ход.
	st.StopAt, st.StopSid, st.StopTask, st.StopProject, st.StopPath = 0, "", "", "", ""
	if sid != "" && chatKeyRe.MatchString(sid) {
		st.From = sid
	}
	if task != "" {
		st.Task = task
	}
	if proj != "" {
		st.Project = proj
	}
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("подъём сессии %s не запомнился: %v", sess, err)
	}
	s.watchAdd(sess)
}

// chatWatchOff снимает сессию с присмотра. Снятие рукой (стоп под перезапуск,
// уборка в архив) это не смерть: человек сам закончил разговор, и строка о
// смерти в его ленте была бы враньём.
func (s *server) chatWatchOff(sess string) {
	if sess == "" || !chatKeyRe.MatchString(sess) {
		return
	}
	s.watchMu.Lock()
	delete(s.watch, sess)
	delete(s.tails, sess)
	s.watchMu.Unlock()
	key := "tmux-" + sess
	st := s.chatStoreRead(key)
	if st.Raised == 0 && st.StopAt == 0 {
		return
	}
	// Разговор снимают рукой, и дожимать в нём больше нечего: окна не будет, а
	// оставленный заказ ожил бы на следующем жильце того же имени.
	st.StopAt, st.StopSid, st.StopTask, st.StopProject, st.StopPath = 0, "", "", "", ""
	st.Raised = 0
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("снятие сессии %s не запомнилось: %v", sess, err)
	}
}

func (s *server) watchAdd(sess string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		s.watch = map[string]bool{}
	}
	s.watch[sess] = true
}

// chatWatchNames перечисляет сессии под присмотром.
func (s *server) chatWatchNames() []string {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	out := make([]string, 0, len(s.watch))
	for name := range s.watch {
		out = append(out, name)
	}
	return out
}

// chatWatchRestore возвращает присмотр после перезапуска демона: выкат меняет
// бинарь каждый день, и сессии, поднятые до него, иначе теряли бы сторожа
// молча. Записи читаются с диска один раз, на старте сторожа.
func (s *server) chatWatchRestore() {
	dir := chatStoreDir(s.cfg.Home)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || !strings.HasPrefix(name, "tmux-") || name == e.Name() {
			continue
		}
		sess := strings.TrimPrefix(name, "tmux-")
		st := s.chatStoreRead(name)
		if st.Raised > 0 && st.Dead == 0 {
			s.watchAdd(sess)
		}
		// Незаконченный дожим стопа переживает перезапуск демона так же, как
		// присмотр за подъёмом: выкат меняет бинарь каждый день, а работа,
		// которую человек остановил, ждать его не станет.
		if st.StopAt > 0 {
			s.watchAdd(sess)
		}
	}
}

// chatWatchKeeper это сам сторож: обход поднятых сессий шагом chatWatchStep.
// Без него смерть замечал бы только тот, кто стоит на панели разговора, а
// человек уходит с экрана и возвращается через час.
func (s *server) chatWatchKeeper(stop <-chan struct{}) {
	s.chatWatchRestore()
	t := time.NewTicker(chatWatchStep)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.chatWatchTick()
		}
	}
}

// chatWatchTick это один обход. Список tmux спрашивается один раз на обход:
// сессий под присмотром бывает десяток, и подпроцесс на каждую был бы дороже
// самого присмотра.
func (s *server) chatWatchTick() {
	if tmuxMissingCheck() != "" {
		return
	}
	alive := tmuxAliveFn()
	for _, name := range s.chatWatchNames() {
		s.chatWatchOne(name, alive)
		// Заказ дожима стопа смотрится тем же обходом: сессия у него та же, шаг
		// тот же, а второй сторож рядом с этим ходил бы по тому же списку tmux
		// (разбор в stopwait.go).
		s.stopWaitOne(name, alive)
	}
}

// chatWatchOne сверяет одну поднятую сессию. Жива значит снимок панели про
// запас, пропала значит смерть со словами. Второй ответ говорит, что сессии
// больше нет: им кончается ожидание подъёма в панели.
func (s *server) chatWatchOne(name string, alive func(string) bool) (chatStore, bool) {
	if name == "" || !chatKeyRe.MatchString(name) {
		return chatStore{}, false
	}
	st := s.chatStoreRead("tmux-" + name)
	if st.Dead != 0 {
		return st, true
	}
	if st.Raised == 0 {
		return st, false
	}
	if alive(name) {
		s.chatTailKeep(name)
		return st, false
	}
	return s.chatDeathSay(name, st), true
}

// chatTailKeep держит снимок панели свежим. Снимок берётся у живой сессии
// нарочно: у мёртвой панели нет вовсе, и причину смерти читать было бы негде.
func (s *server) chatTailKeep(name string) {
	now := s.now()
	s.watchMu.Lock()
	old, had := s.tails[name]
	s.watchMu.Unlock()
	if had && now.Sub(old.at) < chatTailStep {
		return
	}
	text := chatTailCut(tmuxPane(name))
	if text == "" {
		return
	}
	s.watchMu.Lock()
	if s.tails == nil {
		s.tails = map[string]chatTail{}
	}
	s.tails[name] = chatTail{text: text, at: now}
	s.watchMu.Unlock()
}

func (s *server) chatTailOf(name string) string {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	return s.tails[name].text
}

// chatDeathSay называет смерть словами и доносит их до человека: запись имени
// запоминает исход (по ней кончается ожидание подъёма в панели), лента
// разговора получает строку с причиной, журнал демона строку для разбора.
func (s *server) chatDeathSay(name string, st chatStore) chatStore {
	// Смерть называется один раз. Опрос панели и сторож демона ходят по одному
	// имени вразнобой, и без замка два захода записали бы в ленту две строки об
	// одной смерти.
	s.deathMu.Lock()
	defer s.deathMu.Unlock()
	if cur := s.chatStoreRead("tmux-" + name); cur.Dead != 0 {
		return cur
	}
	now := s.now()
	lived := time.Duration(0)
	if st.Raised > 0 {
		if d := now.Sub(time.Unix(st.Raised, 0)); d > 0 {
			lived = d
		}
	}
	// Кому смерть рассказывать: хозяину имени по реестру, а нет его, так тому
	// разговору, из которого подъём заказали. Первый случай это смерть сессии,
	// уже начавшей ход, второй немой подъём.
	sid := sessions.TmuxOwner(s.bindsAll(), name)
	named := sid != ""
	if sid == "" {
		sid = st.From
	}
	why := chatDeathWord(name, lived, named)
	tail := s.chatTailOf(name)
	st.Raised, st.Dead, st.DeadWhy, st.Tail = 0, now.Unix(), why, tail
	if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
		s.logf("смерть сессии %s не запомнилась: %v", name, err)
	}
	line := why
	if tail != "" {
		line += "\nПоследние строки терминала:\n" + tail
	}
	switch {
	case sid != "":
		s.saidMark(saidSessionKey(sid), line)
	case st.Task != "":
		s.saidMark(saidTaskKey(st.Task), line)
	}
	s.logf("%s", why)
	s.watchMu.Lock()
	delete(s.watch, name)
	delete(s.tails, name)
	s.watchMu.Unlock()
	// Строка в ленте разговора ждёт, пока человек откроет карточку, а
	// оборванная работа ждать не должна: задачу, оставшуюся без ведущей
	// сессии, уведомитель доносит до человека сам (nolead.go, DK-660).
	s.noLeadSay(st.Task, st.Project, why)
	return st
}

// chatDeathWord это слова смерти. Немой подъём и смерть после хода лечатся
// по-разному, и путать их нельзя: в первом случае реплика человека не доехала
// вовсе, во втором разговор просто кончился и продолжается резюмом.
func chatDeathWord(name string, lived time.Duration, named bool) string {
	if named {
		return fmt.Sprintf("сессия %s прожила %s и кончилась: терминала у разговора больше нет, "+
			"следующая реплика поднимет продолжение резюмом", name, chatLived(lived))
	}
	return fmt.Sprintf("сессия %s прожила %s и умерла, не начав хода: клиент вышел, не назвавшись "+
		"в реестре, и реплика до агента не доехала", name, chatLived(lived))
}

// chatLived говорит срок жизни человеческими словами: секунды у немого
// подъёма, часы у долгого разговора.
func chatLived(d time.Duration) string {
	switch {
	case d < time.Second:
		return "меньше секунды"
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	}
}

// tmuxPane снимает панель сессии. Отказ это штатное «сессии уже нет»: снимок
// берётся у живой, и гонку с её смертью выигрывает не всякий заход.
func tmuxPane(name string) string {
	out, err := runProc("tmux", "capture-pane", "-p", "-t", "="+name+":")
	if err != nil {
		return ""
	}
	return string(out)
}

// chatTailCut готовит хвост панели к ленте: пустые строки в конце снимаются,
// остаётся последняя дюжина строк, длинные строки режутся, а похожее на токен
// затирается. Журнал ленты живёт столько же, сколько разговор, и снимок панели
// с ключом подписки остался бы в нём навсегда.
func chatTailCut(pane string) string {
	lines := strings.Split(strings.ReplaceAll(pane, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > chatTailLines {
		lines = lines[len(lines)-chatTailLines:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		line = chatSecretRe.ReplaceAllStringFunc(line, func(m string) string {
			return m[:4] + "..."
		})
		if len([]rune(line)) > chatTailWidth {
			line = string([]rune(line)[:chatTailWidth]) + "..."
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return strings.Join(out, "\n")
}

// chatDeadResp это исход подъёма для панели: имя сессии, слова о смерти и
// хвост терминала. Панель кончает им ожидание и показывает причину человеку.
func chatDeadResp(name string, st chatStore) map[string]any {
	return map[string]any{"tmux": name, "why": st.DeadWhy, "tail": st.Tail, "at": st.Dead}
}

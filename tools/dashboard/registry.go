package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Реестр чатов задачи: машинный журнал ~/.devkit/sessions.log говорит, какую
// задачу ведёт сессия. Строки дописывают SessionStart-хук hooks/session-task.py
// и ручка привязки рукой, читает их дашборд и сворачивает в отображение
// «сессия -> задача», где выигрывает последняя строка: перепривязка и отвязка
// это обычные записи, а не правка файла (LLD DK-430, решение 1). Формат тот же,
// что у журнала уведомителя: время, дальше именованные поля, и разбор держится
// за ключевые слова, а не за одни позиции.

// Слова источника привязки в строке журнала. Первые два пишет хук старта,
// вторые два ручка привязки: «снята» это отвязка, и задача у неё пустая.
const (
	bindOrder = "заказ"
	bindTree  = "дерево"
	bindHand  = "рука"
	bindOff   = "снята"
)

// Разряды привязки. Машинное поле рядом с подписью нужно экрану, чтобы
// отличать разряд, не разбирая слов: «ведёт» это запись реестра, только она
// делает сессию работой задачи, «говорит о» это угадывание по транскрипту, и
// такая сессия стоит в списке чатов, но живой работой не горит (DK-360).
const (
	boundLead  = "lead"
	boundAbout = "about"
)

// Подписи источника словами: их видно на карточке сессии чипом.
const (
	orderNote  = "по заказу дашборда"
	treeNote   = "по дереву задачи"
	handNote   = "привязана рукой"
	replyNote  = "по первой реплике"
	branchNote = "по ветке"
	// offNote отличает снятую привязку от несостоявшейся: у первой человек
	// сказал «это не работа задачи», и возвращать её угадыванием нельзя, а
	// молчание про снятие читалось бы как несработавшая кнопка.
	offNote = "привязка снята рукой"
	// recordNote подписывает запись с незнакомым словом источника: реестр
	// пишут двое, и слово со стороны новее этого дашборда не повод забыть
	// саму привязку.
	recordNote = "по записи реестра"
)

// bindNotes сводят слово источника к подписи для экрана.
var bindNotes = map[string]string{
	bindOrder: orderNote,
	bindTree:  treeNote,
	bindHand:  handNote,
}

// Разбор реестра и его свёртка живут в общем пакете internal/sessions: читают
// журнал трое (дашборд, taskctl ask и сторожок), и разошедшиеся копии формата
// оставили бы каждого со своей половиной записей.
type sessionBind = sessions.Bind

type sessionBinds = sessions.Binds

const bindStamp = sessions.Stamp

func parseBindLine(line string) (string, sessionBind, bool) { return sessions.ParseLine(line) }

func parseBinds(data []byte) sessionBinds { return sessions.Parse(data) }

func (s *server) bindsPath() string {
	return sessions.Path(s.cfg.Home)
}

// binds читает реестр целиком. Файл капается по длине самим писателем
// (hookio.append_capped), и памяти процесса тут не заводится: строка ложится
// на каждом старте сессии, а устаревшая привязка врёт дороже, чем стоит чтение
// сотни килобайт.
func (s *server) binds() sessionBinds {
	return sessions.Load(s.cfg.Home)
}

// task называет задачу сессии, подпись источника и разряд привязки. Запись
// реестра старше угадывания и решает всё: у неё есть и снятая привязка, после
// которой угадывать заново нельзя, иначе кнопка отвязки ничего не меняла бы.
// Дальше идут те же три способа, что работали до реестра, и разряд у них
// разный: боковое дерево заводится ровно под одну задачу и врать не умеет, а
// первая реплика и ветка называют задачу, о которой говорят, а не ту, которую
// ведут.
func bindTask(b sessionBinds, sid, dirSuffix string, head sessionHead) (task, note, bound string) {
	if rec, ok := b[sid]; ok {
		if rec.Task != "" {
			note := bindNotes[rec.Source]
			if note == "" {
				note = recordNote
			}
			return rec.Task, note, boundLead
		}
		if rec.Source == bindOff {
			return "", offNote, ""
		}
	}
	if id := taskIDInName(dirSuffix); id != "" {
		return id, treeNote, boundLead
	}
	// Задачи у разговора нет, и это не поломка: свободный чат стоит рядом с
	// задачными на равных. Угадывание по первой реплике и по имени ветки снято
	// (POC ветки poc-chat):
	// именно оно разводило один разговор на четыре карточки, называя работой
	// всякое окно, где прозвучал ID задачи. Привязка теперь идёт по факту
	// работы и приезжает записью реестра.
	return "", unknownTaskNote, ""
}

// leadsTask отвечает на один вопрос: присваивает ли сессия строку задачи.
// Присваивает исполнительская: конвейер дашборда (tmux-сессии task-<ID> и
// goal-<ID>, их же поднимают кнопки «Выполнить» и «Продолжить») и работа в
// боковом дереве задачи. Разговорный чат строку не присваивает: chat-<ID>-<n>
// и свободный чат, привязанный рукой, это чтение задачи и разговор о ней, а не
// работа над ней, и второго исполнителя от них на строке не заводится (жалоба
// пользователя на DK-460).
//
// Критерий тут один на весь сервер: по нему строка получает признак работы, а
// Check подписку. Разъехавшись, он возвращает кнопки конвейера чужой задаче,
// ровно этим он и разъехался в прошлый раз.
func leadsTask(tmux, dirSuffix, note string) bool {
	// Имя tmux-сессии называет род работы прямо, и спрашивать больше нечего.
	if tmux != "" {
		return strings.HasPrefix(tmux, "task-") || strings.HasPrefix(tmux, "goal-")
	}
	// Имени в записи нет (старая запись либо окно человека): боковое дерево
	// заводится ровно под одну задачу и врать не умеет, а привязка рукой это
	// разговор. Остальное считается работой, как считалось до чатов.
	return taskIDInName(dirSuffix) != "" || note != handNote
}

// bindLine собирает строку реестра для ручки привязки. Поля и их порядок те
// же, что у хука: писателя два, а формат один, и разъехавшись, они оставили бы
// читателя с половиной записей.
func bindLine(now time.Time, sid string, b sessionBind) string {
	return fmt.Sprintf("%s сессия %s задача %s проект %s дерево %s транскрипт %s источник %s повод %s tmux %s\n",
		now.Format(bindStamp), dash(sid), dash(b.Task), dash(b.Project), dash(b.Tree),
		dash(b.Transcript), dash(b.Source), dash("рука"), dash(b.Tmux))
}

// dash это пустое поле дефисом, обратная сторона dashless.
func dash(v string) string {
	if v = strings.Join(strings.Fields(v), " "); v == "" {
		return "-"
	}
	return v
}

// bindLogLimit и bindLogKeep держат реестр в тех же берегах, что и писатель на
// python (hookio.LOG_LIMIT, LOG_KEEP): разросшийся файл режется до последних
// строк, а сворачивает читатель всё равно по последней записи сессии.
const (
	bindLogLimit = 100 * 1024
	bindLogKeep  = 500
)

// appendBind дописывает строку в реестр, обрезав разросшийся файл. Обрезка
// повторяет python-писателя нарочно: два писателя с разными правилами роста
// оставили бы файл, растущий до диска в половине случаев.
func appendBind(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > bindLogLimit {
		if data, err := os.ReadFile(path); err == nil {
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > bindLogKeep {
				lines = lines[len(lines)-bindLogKeep:]
			}
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				return err
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// handleSessionTaskPost привязывает разговор к задаче рукой и отвязывает его
// пустым значением. Это ответ на нераспознанную сессию: угадать её задачу
// нечем, а карточка без имени и без входа бесполезна (DK-291).
func (s *server) handleSessionTaskPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "привязка сессии")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	var body struct {
		Task string `json:"task"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON {\"task\": \"DK-431\"}, пустое значение снимает привязку"})
		return
	}
	task := strings.ToUpper(strings.TrimSpace(body.Task))
	if task != "" && !taskParamRe.MatchString(task) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", body.Task)})
		return
	}
	// Сессия сверяется с транскриптами проекта: привязка без разговора никому
	// не видна, а опечатка в id иначе легла бы в реестр молча.
	info, ok := findSession(s.transcriptRoots(), found.Path, sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf(
			"транскрипта %s нет среди сессий проекта %s: привязывать нечего", sid, found.Name)})
		return
	}
	b := sessionBind{Task: task, Source: bindHand, Project: found.Name, Transcript: info.path}
	if tree, ok := sessionTree(found.Path, info.suffix); ok {
		b.Tree = tree
	}
	// Прежнее имя tmux-сессии переезжает в новую запись: рука правит задачу, а
	// не то, чем сессия поднята, и потерянное имя оставило бы меру живости
	// разговора без опоры.
	if old, ok := s.binds()[sid]; ok {
		b.Tmux = old.Tmux
	}
	if task == "" {
		b.Source = bindOff
	}
	if err := appendBind(s.bindsPath(), bindLine(s.now(), sid, b)); err != nil {
		s.logf("привязка сессии %s в %s не удалась: реестр не записался 502: %v", sid, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("реестр чатов %s не записался: %v", s.bindsPath(), err)})
		return
	}
	msg := fmt.Sprintf("сессия %s привязана к задаче %s рукой", sid, task)
	if task == "" {
		msg = fmt.Sprintf("привязка сессии %s снята: работой задачи она больше не считается", sid)
	}
	s.logf("%s (проект %s)", msg, found.Name)
	writeJSON(w, http.StatusOK, map[string]string{"session": sid, "task": task, "message": msg})
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Разговор с целью со стороны дашборда: отметки доставки подхвата (hooks/chat-in.py)
// и живость витка, по которой пишется плашка чата. Стенды кладут те же файлы,
// какие на живой машине кладут подхват с оболочкой, настоящих сессий тут нет.

// mailBody собирает файл отметок тем же форматом, каким его пишет подхват:
// на запись две строки, «время сессия» и доставленная строка целиком.
func mailBody(marks ...mailMark) string {
	out := ""
	for _, m := range marks {
		session := m.Session
		if session == "" {
			session = "-"
		}
		out += m.At + " " + session + "\n" + m.Line + "\n"
	}
	return out
}

func writeMail(t *testing.T, e *testEnv, id, body string) {
	t.Helper()
	dir := filepath.Join(e.proj, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal-"+id+".mail"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// getMessage читает ручку чата и разбирает ответ: состояние доставки и живость
// витка клиент берёт полями, а не разбором русской фразы.
func getMessage(t *testing.T, c *http.Client, e *testEnv, id string) struct {
	Pending   []string   `json:"pending"`
	Delivered []mailMark `json:"delivered"`
	Live      bool       `json:"live"`
} {
	t.Helper()
	var v struct {
		Pending   []string   `json:"pending"`
		Delivered []mailMark `json:"delivered"`
		Live      bool       `json:"live"`
	}
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/"+id+"/message", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("чтение «Входящих»: %d %s", resp.StatusCode, text)
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ не разобрался: %v (%s)", err, text)
	}
	return v
}

func stampNow(back time.Duration) string {
	return time.Now().Add(-back).Format(mailStamp)
}

// Лежащая строка с отметкой это «доставлено агенту»: состояние берётся из
// .devkit/goal-<ID>.mail той же ручкой, своей ручки каналу не нужно. Кто
// поставил отметку, дашборду неважно: у подхвата и у ключа --ask вход один,
// а отметка без сессии (строку съел --ask в чате) это то же состояние, а не
// ошибка разбора.
func TestMessageDeliveredFromMarks(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	for _, text := range []string{"стой, не туда", "и журнал посмотри"} {
		postMessage(t, c, e, "XR-100", text).Body.Close()
	}
	lying := inboxLines(readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md")))
	if len(lying) != 2 {
		t.Fatalf("во «Входящих» %d строк, ждал две", len(lying))
	}
	writeMail(t, e, "XR-100", mailBody(
		mailMark{At: stampNow(time.Minute), Session: "8f2a1c30-1111", Line: lying[0]},
		mailMark{At: stampNow(30 * time.Second), Line: lying[1]}))

	got := getMessage(t, c, e, "XR-100")
	if len(got.Delivered) != 2 {
		t.Fatalf("доставленных строк %d, ждал две: %+v", len(got.Delivered), got.Delivered)
	}
	if got.Delivered[0].Line != lying[0] || got.Delivered[0].Session != "8f2a1c30-1111" {
		t.Errorf("отметка подхвата потеряла строку или сессию: %+v", got.Delivered[0])
	}
	if got.Delivered[0].At == "" {
		t.Errorf("отметка приехала без времени: %+v", got.Delivered[0])
	}
	if got.Delivered[1].Line != lying[1] || got.Delivered[1].Session != "" {
		t.Errorf("отметка без сессии не прочиталась как доставка: %+v", got.Delivered[1])
	}
	if got.Delivered[1].At == "" {
		t.Errorf("у отметки без сессии пропало время: %+v", got.Delivered[1])
	}
	if len(got.Pending) != 2 {
		t.Errorf("доставленная строка ушла из «Входящих»: %v", got.Pending)
	}
}

// Отставшая отметка на подхваченной строке не перебивает «прочитано агентом»:
// состояния считаются в том же порядке, в каком живут отметки, и строки во
// «Входящих» больше нет.
func TestMessageDeliveredMarkOfGoneLineIgnored(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	postMessage(t, c, e, "XR-100", "стой, не туда").Body.Close()
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")
	gone := inboxLines(readFile(t, path))[0]
	writeMail(t, e, "XR-100", mailBody(
		mailMark{At: stampNow(time.Minute), Session: "8f2a1c30-1111", Line: gone}))

	// Виток подхватил строку и убрал её записью витка.
	taken := strings.ReplaceAll(readFile(t, path), "- "+gone+"\n", "")
	if err := os.WriteFile(path, []byte(taken), 0o644); err != nil {
		t.Fatal(err)
	}
	got := getMessage(t, c, e, "XR-100")
	if len(got.Pending) != 0 {
		t.Fatalf("подхваченная строка осталась во «Входящих»: %v", got.Pending)
	}
	if len(got.Delivered) != 0 {
		t.Errorf("отставшая отметка выдала подхваченную строку за доставленную: %+v", got.Delivered)
	}
}

// Кривые отметки не роняют ручку и не выдумывают состояний: файл пишет чужой
// процесс на горячем пути, и оборванная на середине запись это рабочий случай.
// Отсутствие файла это отсутствие состояния, а не ошибка.
func TestMessageDeliveredJunkMarks(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	postMessage(t, c, e, "XR-100", "стой, не туда").Body.Close()
	lying := inboxLines(readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md")))[0]

	if got := getMessage(t, c, e, "XR-100"); len(got.Delivered) != 0 {
		t.Errorf("без файла отметок нашлась доставка: %+v", got.Delivered)
	}
	for _, junk := range []string{
		"",
		"\n\n\n",
		"мусор без пары\n",
		stampNow(time.Minute) + " 8f2a1c30\n",
		"\n" + lying + "\n",
	} {
		writeMail(t, e, "XR-100", junk)
		if got := getMessage(t, c, e, "XR-100"); len(got.Delivered) != 0 {
			t.Errorf("битые отметки %q дали доставку: %+v", junk, got.Delivered)
		}
	}

	// Целая запись после битого хвоста читается: обрыв в конце файла не должен
	// хоронить лежащие перед ним отметки.
	writeMail(t, e, "XR-100", mailBody(
		mailMark{At: stampNow(time.Minute), Session: "8f2a1c30", Line: lying})+"обрыв на середине")
	if got := getMessage(t, c, e, "XR-100"); len(got.Delivered) != 1 {
		t.Errorf("целая отметка перед обрывом потерялась: %+v", got.Delivered)
	}
}

// deadPID отдаёт номер заведомо мёртвого процесса: он отработал и снят, а
// номер остался. Выдуманное число тут не годится, оно может оказаться живым.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("стенд не поднял процесс: %v", err)
	}
	return cmd.Process.Pid
}

func writeLock(t *testing.T, e *testEnv, id string, pid int) {
	t.Helper()
	dir := filepath.Join(e.proj, ".devkit", "goal-"+id+".lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// watchEntry кладёт запись реестра целей: ею живёт liveWorks, и по ней же
// берётся метка движения seen.
func watchEntry(t *testing.T, e *testEnv, id, seen string) {
	t.Helper()
	dir := filepath.Join(e.home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := "goal = " + id + "\nroot = " + e.proj + "\nstopped = 1\n"
	if seen != "" {
		entry += "seen = " + seen + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".watch"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCycleLog(t *testing.T, e *testEnv, id, text string) {
	t.Helper()
	dir := filepath.Join(e.proj, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal-"+id+".log"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Живость витка меряется правилом подхвата, а не списком живых работ: замок
// с живым pid это идущая оболочка при любых метках, а мёртвый замок с
// протухшими метками это цель, до которой доставлять некому, даже когда
// запись реестра держит работу на экране.
func TestGoalLiveByLockNotByWorks(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	// Запись реестра на месте, и liveWorks по ней считает цель идущей работой.
	watchEntry(t, e, "XR-100", stampNow(5*time.Hour))
	writeCycleLog(t, e, "XR-100", stampNow(6*time.Hour)+" виток 3: конвейер закрыт\n")

	if e.s.goalIdle(e.proj, "XR-100") {
		t.Fatal("стенд не удержал цель живой работой: разницы между liveWorks и живостью доставки на нём не видно")
	}
	if got := getMessage(t, c, e, "XR-100"); got.Live {
		t.Error("мёртвый цикл назван живым: плашка пообещала бы минуты до доставки")
	}
	writeLock(t, e, "XR-100", deadPID(t))
	if got := getMessage(t, c, e, "XR-100"); got.Live {
		t.Error("замок с мёртвым pid прочитан как живая оболочка")
	}
	writeLock(t, e, "XR-100", os.Getpid())
	if got := getMessage(t, c, e, "XR-100"); !got.Live {
		t.Error("живой замок цели не дал живости: реплика идущему витку доезжает")
	}
}

// Без замка живость держит метка движения цели, и меток две: seen записи
// реестра и последняя чужая строка журнала цикла. Своя строка доставки
// движением не считается, иначе подхват держал бы цель живой сам за себя.
func TestGoalLiveByMovedMarks(t *testing.T) {
	e, c, _ := messagesEnv(t, "")

	watchEntry(t, e, "XR-100", stampNow(10*time.Minute))
	if got := getMessage(t, c, e, "XR-100"); !got.Live {
		t.Error("свежий seen не дал живости: цель, которую ведут в чате, осталась без доставки")
	}

	// seen протух на длинном витке, а журнал цикла двигался: живость держит он.
	watchEntry(t, e, "XR-100", stampNow(5*time.Hour))
	writeCycleLog(t, e, "XR-100", stampNow(6*time.Hour)+" виток 2: пачка ушла\n"+
		stampNow(20*time.Minute)+" виток 3: исполнители подняты\n")
	if got := getMessage(t, c, e, "XR-100"); !got.Live {
		t.Error("свежая строка журнала цикла не дала живости при протухшем seen")
	}

	// Журнала цикла нет вовсе, цель ведут в чате впервые: живость держит seen.
	if err := os.Remove(filepath.Join(e.proj, ".devkit", "goal-XR-100.log")); err != nil {
		t.Fatal(err)
	}
	watchEntry(t, e, "XR-100", stampNow(time.Minute))
	if got := getMessage(t, c, e, "XR-100"); !got.Live {
		t.Error("отсутствующий журнал прочитан как нулевое время и убил живость")
	}

	// Обе метки старше порога: доставлять некому.
	watchEntry(t, e, "XR-100", stampNow(mailMoved+time.Minute))
	writeCycleLog(t, e, "XR-100", stampNow(mailMoved+2*time.Minute)+" виток 3: конвейер закрыт\n")
	if got := getMessage(t, c, e, "XR-100"); got.Live {
		t.Error("метки старше порога сошли за живую цель")
	}
	// Порог берётся не на глаз: метка чуть свежее порога живость держит.
	watchEntry(t, e, "XR-100", stampNow(mailMoved-time.Minute))
	if got := getMessage(t, c, e, "XR-100"); !got.Live {
		t.Error("метка свежее порога не дала живости: порог разъехался с подхватом")
	}

	// Своя строка доставки не движение цели: с ней одной живости нет.
	watchEntry(t, e, "XR-100", "")
	writeCycleLog(t, e, "XR-100", stampNow(5*time.Hour)+" виток 3: конвейер закрыт\n"+
		stampNow(time.Minute)+" "+mailSayWord+": витку доставлена реплика «стой, не туда»\n")
	if got := getMessage(t, c, e, "XR-100"); got.Live {
		t.Error("строка доставки сошла за движение цели: подхват держит цель живой сам за себя")
	}
	// Прежнее слово той же строки лежит в журналах целей с прошлых витков, и
	// узнавать его надо по-прежнему: пока слова были разными, дашборд считал
	// чужую по виду строку движением и обещал минуты мёртвой цели.
	writeCycleLog(t, e, "XR-100", stampNow(5*time.Hour)+" виток 3: конвейер закрыт\n"+
		stampNow(time.Minute)+" "+mailSayWordOld+": витку доставлена реплика «стой, не туда»\n")
	if got := getMessage(t, c, e, "XR-100"); got.Live {
		t.Error("прежняя строка доставки сошла за движение цели")
	}
	// Записи реестра без метки хватает liveWorks, но не хватает доставке.
	if e.s.goalIdle(e.proj, "XR-100") {
		t.Error("цель ушла из живых работ: стенд перестал ловить подмену живости списком работ")
	}
}

// Порог живости и слово своей строки журнала живут в двух реализациях, питоньей
// и Go, и разъехаться им нельзя: подхват реплики перестанет доставлять раньше,
// чем плашка перестанет обещать минуты. Хука рядом нет это провал, а не
// пропуск: переименуй его кто-нибудь, и пропуск снял бы сторожа молча, ровно
// там, где он и нужен.
func TestGoalLiveRuleMatchesChatHook(t *testing.T) {
	path := filepath.Join("..", "..", "hooks", "chat-in.py")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("подхвата реплики рядом нет: %v", err)
	}
	text := string(data)
	if want := fmt.Sprintf("MOVED = %d * 3600", int(mailMoved.Hours())); !strings.Contains(text, want) {
		t.Errorf("порог метки движения разъехался с подхватом: в chat-in.py нет %q", want)
	}
	// Слово своей строки сверяется со стороной, которая эти строки и пишет:
	// разъедься они, дашборд считал бы строку доставки движением цели, и цель
	// горела бы живой сама за себя (так оно и было, пока Go говорил «чат», а
	// подхват «разговор»). Прежнее слово обе стороны узнают по-прежнему: в
	// журналах целей оно осталось.
	if want := fmt.Sprintf("SAY_WORD = %q", mailSayWord); !strings.Contains(text, want) {
		t.Errorf("слово строки доставки разъехалось с подхватом: в chat-in.py нет %q", want)
	}
	if want := fmt.Sprintf("SAY_WORD_OLD = %q", mailSayWordOld); !strings.Contains(text, want) {
		t.Errorf("прежнее слово строки доставки пропало из подхвата: в chat-in.py нет %q", want)
	}
	if want := fmt.Sprintf("STAMP = %q", "%Y-%m-%dT%H:%M:%S"); !strings.Contains(text, want) {
		t.Errorf("формат времени отметки разъехался с подхватом: в chat-in.py нет %q", want)
	}
}

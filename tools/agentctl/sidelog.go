package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Боковой журнал розданной работы. Подпроцесс режима cli это чужая сессия со
// своим каталогом конфигурации: журнал разговора он пишет туда, и раздавший
// разговор про эту работу не знает ничего, поэтому на дашборде она не видна ни
// строкой, ни ходом (DK-581, живой случай DK-577). Ход работы субагента
// дашборд собирает из каталога `subagents` при транскрипте разговора: на
// каждый вызов там лежит мета-файл с подписью и журнал с репликами. Туда же
// встаёт и делегирование: мету run пишет сам, а журналом делает ссылку на
// транскрипт подпроцесса. Копии нет сознательно: транскрипт пишется чужим
// процессом построчно, и копия отставала бы ровно на то время, ради которого
// всё и затевалось.

const (
	// parentSessionEnv это имя разговора, раздавшего работу. Своё имя сессия
	// Claude Code кладёт в окружение сама, и по нему находится её транскрипт.
	parentSessionEnv = "CLAUDE_CODE_SESSION_ID"
	// parentEnv называет раздавший разговор подпроцессу: у делегата своего
	// реестра нет, и сказать, чью работу он делает, может только тот, кто его
	// поднял.
	parentEnv = "DEVKIT_PARENT_SESSION"
	// taskEnv и tmuxEnv это заказ того, кто поднял сессию: по ним хук старта
	// пишет в реестр машины, чью задачу сессия ведёт и каким разговором её
	// зовут. Подпроцесс наследует окружение родителя вместе с ними, и без
	// правки делегат записывался бы чужой работой (живой случай 2026-08-29:
	// заход по DK-577 встал в реестр задачей XR-005 и разговором
	// chat-XR-005-1, то есть заказом раздавшей сессии).
	taskEnv = "DEVKIT_TASK"
	tmuxEnv = "DEVKIT_TMUX"
	// sessionMark это плейсхолдер имени сессии подпроцесса. Он же и признак:
	// журнал заводится там, где профиль назвал его в команде, то есть где
	// клиент назначения принимает имя сессии снаружи. Угадывать имя по
	// свежести файлов нельзя, параллельных запусков бывает несколько.
	sessionMark = "{session}"
	// sideLinkPoll это шаг ожидания транскрипта: клиент заводит файл не в
	// первую секунду жизни, а ссылку надо поставить, пока работа идёт, а не
	// когда она кончилась.
	sideLinkPoll = time.Second
)

// newSessionID это имя сессии подпроцесса: UUID четвёртой версии, как их ждёт
// клиент. Имя выбирает run, а не клиент, потому что по нему потом находится
// транскрипт: узнать его задним числом можно только гаданием по свежести.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// journalHomes перечисляет каталоги хозяйства, под которыми лежат журналы
// разговоров: своё ~/.claude плюс дом каждой подписки из машинного слоя.
// Порядок держится ровным ради повторяемости, хотя решает он мало: имя сессии
// уникально, и совпасть в двух домах оно не может.
func journalHomes(l *layers) []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude"))
	}
	if l == nil {
		return out
	}
	names := make([]string, 0, len(l.Setup))
	for name := range l.Setup {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if h := l.Setup[name].homeOf(); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// harnessHome это дом подписки, под которым лежат журналы её разговоров.
// Харнес без своего каталога конфигурации работает на подписке по умолчанию, и
// журналы его сессий лежат там же, где свои.
func harnessHome(l *layers, name string) string {
	if l != nil {
		if h := l.Setup[name].homeOf(); h != "" {
			return h
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// transcriptOf ищет транскрипт разговора по его имени. Каталог журналов
// считается от рабочей директории, в которой разговор подняли, а знать её тут
// неоткуда, поэтому поиск идёт по всем каталогам хозяйства сразу: файл
// называется именем сессии, и оно уникально.
func transcriptOf(homes []string, sid string) string {
	if sid == "" {
		return ""
	}
	for _, home := range homes {
		if home == "" {
			continue
		}
		hits, err := filepath.Glob(filepath.Join(home, "projects", "*", sid+".jsonl"))
		if err != nil || len(hits) == 0 {
			continue
		}
		sort.Strings(hits)
		return hits[0]
	}
	return ""
}

// workMeta это то, что боковой журнал говорит о себе дашборду: чей это вызов,
// как назвать работу человеку и когда она кончилась. Первые три имени полей
// чужие, их пишет сам харнес журналам своих субагентов, и совпадать они
// обязаны, иначе работа не опознается вовсе. Ended своё: у субагента работу
// закрывает ответ на вызов в транскрипте разговора, а у делегата такого ответа
// нет, и сказать, что он вернулся, может только тот, кто его ждал.
type workMeta struct {
	Agent  string `json:"agentType"`
	About  string `json:"description"`
	ToolID string `json:"toolUseId"`
	Ended  string `json:"ended,omitempty"`
}

// sideLog это заведённая работа: мета-файл при транскрипте раздавшего
// разговора, место будущей ссылки на журнал подпроцесса и маска, по которой
// этот журнал ищется.
type sideLog struct {
	meta workMeta
	dir  string
	// path это сам файл журнала, ссылкой на который встаёт транскрипт
	// подпроцесса.
	path string
	// glob это маска транскрипта подпроцесса под домом его подписки: каталог
	// проекта клиент называет по рабочей директории своими правилами, и
	// повторять их тут значило бы держать вторую копию чужого соглашения.
	glob string
}

// openSideLog заводит работу при транскрипте разговора: каталог боковых
// журналов и мета-файл в нём. Журнала на месте ещё нет, и это нормально:
// дашборд читает только те меты, у которых журнал уже лежит рядом, поэтому
// работа встаёт в ленту ровно тогда, когда подпроцессу есть что показать.
func openSideLog(transcript, home, sid, agent, about string) (*sideLog, error) {
	dir := filepath.Join(trimJSONL(transcript), "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &sideLog{
		meta: workMeta{Agent: agent, About: about, ToolID: sid},
		dir:  dir,
		path: filepath.Join(dir, "agent-"+sid+".jsonl"),
		glob: filepath.Join(home, "projects", "*", sid+".jsonl"),
	}
	if err := w.writeMeta(); err != nil {
		return nil, err
	}
	return w, nil
}

// trimJSONL отрезает расширение транскрипта: каталог боковых журналов зовётся
// именем разговора без него.
func trimJSONL(path string) string {
	return path[:len(path)-len(filepath.Ext(path))]
}

func (w *sideLog) writeMeta() error {
	data, err := json.Marshal(w.meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, "agent-"+w.meta.ToolID+".meta.json"), append(data, '\n'), 0o644)
}

// link ставит ссылку на транскрипт подпроцесса, как только тот появился.
// Повторный вызов ничего не делает: ссылка уже стоит, и переставлять её не на
// что.
func (w *sideLog) link() bool {
	if _, err := os.Lstat(w.path); err == nil {
		return true
	}
	hits, err := filepath.Glob(w.glob)
	if err != nil || len(hits) == 0 {
		return false
	}
	sort.Strings(hits)
	return os.Symlink(hits[0], w.path) == nil
}

// follow ждёт транскрипт подпроцесса, пока тот работает. Ждать приходится
// именно так: клиент заводит файл сам и не сразу, а сказать про это некому.
func (w *sideLog) follow(stop <-chan struct{}) {
	t := time.NewTicker(sideLinkPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if w.link() {
				return
			}
		}
	}
}

// finish закрывает работу: ссылка ставится последний раз (подпроцесс мог
// уложиться между тиками) и в мету пишется время возврата. Без него работа
// висела бы идущей до тех пор, пока дашборд не спишет её по молчанию журнала.
func (w *sideLog) finish(now time.Time) {
	w.link()
	w.meta.Ended = now.Format(time.RFC3339)
	w.writeMeta()
}

// sideAbout это подпись работы в ленте: по ней человек читает, чью задачу и
// чьей подпиской делают. Заказ целиком сюда не идёт, это промпт на десятки
// строк.
func sideAbout(id, role, harness string) string {
	return fmt.Sprintf("%s, %s на подписке %s", id, roleWord(role), harness)
}

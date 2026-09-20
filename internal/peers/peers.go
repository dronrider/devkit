// Пакет peers читает реестр живых сессий клиента, ~/.claude/sessions/<pid>.json,
// и по нему отвечает, стоит ли за задачей живая сессия. Реестр пишет сам
// клиент: запись на процесс, в ней ID сессии, состояние и время последнего
// касания. Запись переживает падение клиента, поэтому живость это не наличие
// файла, а живой процесс за ним, проверенный сигналом ноль.
//
// Читателей у реестра двое, дашборд и taskctl, и рубеж молчания у них один
// (IdleAfter): разойдись он, кружок строки на экране и слова под строкой в
// списке мерили бы простой по-разному (DK-910). До DK-910 разбор лежал
// приватными методами сервера дашборда.
package peers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// IdleAfter это рубеж простоя: сессия, чьё последнее касание реестра старше
// него, считается молчащей, как бы жив ни был её процесс. Слову «busy» верят
// только у свежей записи: клиент, упавший посреди хода, оставляет «busy» в
// реестре навсегда, и по нему семичасовой разговор выходил активным.
const IdleAfter = 20 * time.Minute

// SockDir это каталог сокетов сессий: запись без пути сокета получает его по
// pid. Путь машинный, своей настройки у него нет, его знает сам клиент.
const SockDir = "/tmp/cc-socks"

// Dir это каталог реестра внутри дома пользователя.
func Dir(home string) string { return filepath.Join(home, ".claude", "sessions") }

// Peer это живая сессия машины из реестра клиента.
type Peer struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
	Sock       string `json:"messagingSocketPath"`
	Name       string `json:"name"`
	Tmux       string `json:"tmux"`
	Status     string `json:"status"`
	Version    string `json:"version"`
	Protocol   int    `json:"peerProtocol"`
	Updated    int64  `json:"updatedAt"`
	// StatusAt это время последней смены состояния сессии, то есть начало
	// нынешнего хода у занятой. Клиент пишет его миллисекундами рядом со
	// status и по ходу самого хода больше не трогает: пока агент думает, метка
	// стоит на месте, и разница с ней это возраст хода (DK-893).
	StatusAt int64 `json:"statusUpdatedAt"`
}

// Alive проверяет, что процесс сессии жив: реестр переживает падение клиента,
// и запись без процесса это мёртвый сокет, а не собеседник. Сигнал 0 не трогает
// процесс, а только спрашивает, есть ли он.
func (p Peer) Alive() bool {
	if p.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		return false
	}
	// nil тут не годится, пакет os принимает лишь syscall.Signal.
	return proc.Signal(syscall.Signal(0)) == nil
}

// TurnAge это возраст нынешнего хода сессии. Считается только у занятой
// записи: у простаивающей метка говорит, когда сессия освободилась, и время с
// неё ходом не является. Нулевая метка это клиент, который её не пишет, и
// врать про возраст тогда нечем.
func (p Peer) TurnAge(now time.Time) (time.Duration, bool) {
	if p.Status != "busy" || p.StatusAt <= 0 {
		return 0, false
	}
	age := now.Sub(time.UnixMilli(p.StatusAt))
	if age < 0 {
		age = 0
	}
	return age, true
}

// Touched это время последнего касания записи клиентом. Время в записи лежит
// в миллисекундах, нулевое значит «времени нет», и такой записи не верят.
func (p Peer) Touched() (time.Time, bool) {
	if p.Updated <= 0 {
		return time.Time{}, false
	}
	return time.Unix(p.Updated/1000, 0), true
}

// Fresh отвечает, свежа ли запись: касание не старше IdleAfter.
func (p Peer) Fresh(now time.Time) bool {
	at, ok := p.Touched()
	return ok && now.Sub(at) <= IdleAfter
}

// Load читает реестр целиком, живые записи или все. Ключ это ID сессии.
// Мёртвые записи нужны одному месту, состоянию чата дашборда: остальным они
// врали бы живой работой. Нет каталога, значит нет и сессий: пустая карта, а
// не ошибка.
func Load(home string, onlyAlive bool) map[string]Peer {
	out := map[string]Peer{}
	entries, err := os.ReadDir(Dir(home))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(Dir(home), e.Name()))
		if err != nil {
			continue
		}
		var p Peer
		if json.Unmarshal(data, &p) != nil || p.SessionID == "" {
			continue
		}
		if p.Sock == "" {
			p.Sock = filepath.Join(SockDir, fmt.Sprintf("%d.sock", p.PID))
		}
		if onlyAlive && !p.Alive() {
			continue
		}
		// Одна сессия бывает записана дважды (перезапуск клиента с тем же ID):
		// выигрывает свежая запись, у неё живой сокет.
		if old, ok := out[p.SessionID]; ok && old.Updated > p.Updated {
			continue
		}
		out[p.SessionID] = p
	}
	return out
}

// State это живость сессий задачи одним словом.
type State string

// Три состояния. Gone это «сессии нет вовсе»: процесс мёртв или записи о нём
// не осталось, и задача брошена. Silent это живой процесс, молчащий дольше
// IdleAfter. Различать их велено человеком (DK-910, развилка «порог»): под одним
// словом упавший процесс не отличить от думающего. Запись без времени касания
// читается тем же правилом, что у Fresh: свежей её не считают, и живой процесс
// за ней это Silent с нулевой длительностью, а не Alive. Правило одно на
// список и на экран, ради этого пакет и заведён.
const (
	Alive  State = "жива"
	Silent State = "молчит"
	Gone   State = "нет"
)

// Life это ответ о живости задачи: состояние, длительность молчания у
// молчащей и сессия, по которой это сказано.
type Life struct {
	State   State
	Silence time.Duration
	Session string
}

// Judge сворачивает живость по множеству сессий задачи (LLD DK-430, решение
// 8): у строки бывает и чат поверх работы конвейера, и судит самая свежая из
// живых. Нет ни одной живой, значит Gone. Записи проверяются на живой процесс
// здесь же, какой бы картой их ни дали.
func Judge(all map[string]Peer, sids []string, now time.Time) Life {
	best, found := Peer{}, false
	for _, sid := range sids {
		p, ok := all[sid]
		if !ok || !p.Alive() {
			continue
		}
		if !found || p.Updated > best.Updated {
			best, found = p, true
		}
	}
	if !found {
		return Life{State: Gone}
	}
	at, ok := best.Touched()
	if !ok {
		return Life{State: Silent, Session: best.SessionID}
	}
	if now.Sub(at) > IdleAfter {
		return Life{State: Silent, Silence: now.Sub(at), Session: best.SessionID}
	}
	return Life{State: Alive, Session: best.SessionID}
}

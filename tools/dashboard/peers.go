package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// msgID это идентификатор кадра в форме uuid4. Пакета ради одной строки тут не
// заводится: получателю важна только неповторимость.
func msgID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Канал самого Claude Code: живые сессии машины держат реестр
// ~/.claude/sessions/<pid>.json и слушают UDS /tmp/cc-socks/<pid>.sock. Реплика,
// написанная в этот сокет, будит простаивающую сессию за секунды, включая окно
// vscode, которому дашборд до сих пор не мог сказать ничего вовсе.
//
// Кадр разобран живой пробой (фальшивый пир слушал сокет, настоящая сессия ему
// написала): это JSON-строка с переводом строки на конце, по одной на сообщение.
// Отправителя получатель проверяет сам, по учётным данным сокета, и никакого
// токена в кадре нет: peerToken из ~/.claude/sessions/<pid>.<hex>.key нужен не
// отправке. Класс разрешений отправителя едет прямо в тексте атрибутом
// from-mode, и на нём стоит единственный барьер: разойдись он с классом
// получателя, тот придержит сообщение до ответа человека.

// peerDir и sockDir это реестр живых сессий и каталог их сокетов. Пути машинные
// и своей настройки не имеют: их знает сам клиент.
func peerDir(home string) string { return filepath.Join(home, ".claude", "sessions") }

const sockDir = "/tmp/cc-socks"

// peer это живая сессия машины из реестра клиента.
type peer struct {
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
}

// alive проверяет, что процесс сессии жив: реестр переживает падение клиента, и
// запись без процесса это мёртвый сокет, а не собеседник. Сигнал 0 не трогает
// процесс, а только спрашивает, есть ли он.
func (p peer) alive() bool {
	if p.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		return false
	}
	return proc.Signal(nil) == nil
}

// peers читает реестр живых сессий, ключ это ID сессии. Мёртвые записи
// отсеиваются тут же: до сокета такой сессии дело всё равно не дойдёт, а в
// списке диалогов она горела бы живой работой.
func (s *server) peers() map[string]peer {
	out := map[string]peer{}
	entries, err := os.ReadDir(peerDir(s.cfg.Home))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(peerDir(s.cfg.Home), e.Name()))
		if err != nil {
			continue
		}
		var p peer
		if json.Unmarshal(data, &p) != nil || p.SessionID == "" {
			continue
		}
		if p.Sock == "" {
			p.Sock = filepath.Join(sockDir, fmt.Sprintf("%d.sock", p.PID))
		}
		if !p.alive() {
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

// peerFrame собирает кадр канала. Класс разрешений называется prompting
// нарочно: это тот же класс, в котором работает окно человека, и на нём
// сообщение доходит без придержания. Соврать тут нечем, дашборд не сессия
// клиента вовсе, и любой другой класс он назвал бы с тем же основанием.
func peerFrame(text, from string) ([]byte, error) {
	body := fmt.Sprintf(
		"<cross-session-message from=%q from-name=%q from-mode=%q>\n%s\n</cross-session-message>",
		from, "dashboard", "prompting", text)
	frame := map[string]any{
		"msgV":    1,
		"msg_id":  msgID(),
		"type":    "user",
		"message": map[string]string{"role": "user", "content": body},
		// next кладёт реплику в ближайшую очередь ввода: сессия берёт её
		// следующим ходом, а не после всей своей работы.
		"priority": "next",
		"from":     from,
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// peerSendTimeout держит попытку короткой: сокет мёртвой сессии подвисает на
// подключении, и реплика не должна ждать его дольше, чем человек смотрит на
// кнопку.
const peerSendTimeout = 3 * time.Second

// peerSay пишет реплику в сокет живой сессии. Ответа канал не даёт вовсе, и
// удача тут это записанный кадр: подтверждение приходит следующей репликой в
// ленте, а не по проводу.
func peerSay(sock, text string) error {
	conn, err := net.DialTimeout("unix", sock, peerSendTimeout)
	if err != nil {
		return fmt.Errorf("сокет %s не отозвался: %v", sock, err)
	}
	defer conn.Close()
	from := "uds:" + filepath.Join(sockDir, fmt.Sprintf("%d.sock", os.Getpid()))
	frame, err := peerFrame(text, from)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(peerSendTimeout)); err != nil {
		return err
	}
	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("кадр не записался в %s: %v", sock, err)
	}
	return nil
}

// peerWord называет сессию словами для экрана: окно vscode отличается от
// tmux-сессии и от простого окна терминала, и человеку это видно.
func peerWord(p peer) string {
	switch p.Entrypoint {
	case "claude-vscode":
		return "окно vscode"
	case "cli":
		if p.Tmux != "" {
			return "tmux " + strings.SplitN(p.Tmux, ":", 2)[0]
		}
		return "окно терминала"
	}
	if p.Entrypoint != "" {
		return p.Entrypoint
	}
	return "живая сессия"
}

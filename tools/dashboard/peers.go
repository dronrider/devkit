package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
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
	// Сигнал 0 не трогает процесс, а только спрашивает, есть ли он; nil тут не
	// годится, пакет os принимает лишь syscall.Signal.
	return proc.Signal(syscall.Signal(0)) == nil
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

// errPeerNoAck это отказ подтверждения: байты ушли в ядро, а клиент так и не
// дочитал соединение до конца. У живого клиента событийный цикл принимает
// соединение, дочитывает кадр до полузакрытия и закрывает его сам, за
// миллисекунды (живая проба по всем сессиям машины). Молчание за целый таймаут
// значит, что цикл мёртв: connect и write проходят силами ядра (очередь
// прослушивания и буфер сокета), и до этой сверки такая доставка выглядела
// успехом (клин клиента 69975: три «реплика ушла в сокет» подряд без единого
// хода).
var errPeerNoAck = fmt.Errorf("клиент не подтвердил доставку")

// peerAwaitClose ждёт подтверждения от клиента: полузакрытие говорит ему, что
// кадр дочитан, а закрытие соединения с его стороны (EOF у нас) и есть ответ
// живого событийного цикла. Пришедшие байты вычитываются: сам ответ не нужен,
// нужен факт, что его было кому написать.
func peerAwaitClose(conn net.Conn, wait time.Duration) error {
	if u, ok := conn.(*net.UnixConn); ok {
		if err := u.CloseWrite(); err != nil {
			return fmt.Errorf("полузакрытие не прошло: %v", err)
		}
	}
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		_, err := conn.Read(buf)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: тишина в сокете за %v (%v)", errPeerNoAck, wait, err)
		}
	}
}

// peerSay пишет реплику в сокет живой сессии и ждёт подтверждения. Записанные
// байты успехом не считаются: их принимает ядро и у клиента с мёртвым
// событийным циклом, и реплика тогда не доходит никуда. Подтверждение это
// закрытие соединения клиентом после полузакрытия с нашей стороны; без него
// peerSay отвечает errPeerNoAck, и реплика остаётся недоставленной.
func peerSay(sock, text string) error {
	conn, err := net.DialTimeout("unix", sock, peerSendTimeout)
	if err != nil {
		return fmt.Errorf("сокет %s не отозвался: %v", sock, err)
	}
	defer conn.Close()
	from := peerSelfAddr()
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
	return peerAwaitClose(conn, peerSendTimeout)
}

// peerProbe это пустая проба живости событийного цикла: соединение без единого
// байта с немедленным полузакрытием. Живой клиент отвечает закрытием за
// миллисекунды, кадра он при этом не получает, и в разговор ничего не едет.
// Этим зондом детектор отличает живую сессию от клина с живым pty.
func peerProbe(sock string, wait time.Duration) error {
	conn, err := net.DialTimeout("unix", sock, wait)
	if err != nil {
		return fmt.Errorf("сокет %s не отозвался: %v", sock, err)
	}
	defer conn.Close()
	return peerAwaitClose(conn, wait)
}

// peerTmux это имя tmux-сессии, которое живой клиент называет о себе сам:
// реестр клиента пишет его вместе с окном и панелью ("chat-DK-161-1:@997.%997"),
// а нужна тут только сессия. Пусто значит, что клиент идёт не в tmux.
func peerTmux(p peer) string {
	if p.Tmux == "" {
		return ""
	}
	return strings.SplitN(p.Tmux, ":", 2)[0]
}

// tmuxHeld называет живой разговор, который сейчас идёт в окне name. Слово тут
// за самими клиентами, а не за реестром чатов: запись реестра кладёт хук
// старта из унаследованной переменной, и промахнуться она может (DK-673), а
// клиент называет своё окно о себе и только пока жив.
//
// Пустой ответ значит, что живого хозяина у имени нет: окно ведёт клиент
// старой версии, кончившийся разговор или не наш процесс вовсе.
func tmuxHeld(live map[string]peer, name string) string {
	if name == "" {
		return ""
	}
	for sid, p := range live {
		if peerTmux(p) == name {
			return sid
		}
	}
	return ""
}

// peerWord называет сессию словами для экрана: окно vscode отличается от
// tmux-сессии и от простого окна терминала, и человеку это видно.
func peerWord(p peer) string {
	switch p.Entrypoint {
	case "claude-vscode":
		return "окно vscode"
	case "cli":
		if n := peerTmux(p); n != "" {
			return "tmux " + n
		}
		return "окно терминала"
	}
	if p.Entrypoint != "" {
		return p.Entrypoint
	}
	return "живая сессия"
}

// peerListen поднимает свой конец канала: сессия, получившая реплику, отвечает
// на адрес отправителя, и без слушателя её ответ падает с ENOENT, а ход
// уходит впустую на пересылку. Ответ дашборду не нужен вовсе (он и так придёт
// в ленту транскриптом), поэтому соединение просто вычитывается досуха.
// peerSelfPath это путь своего конца канала, а peerSelfAddr тот же путь
// адресом канала, каким его видит агент. Считается он из номера процесса, и
// точка тут одна на всех: слушателя канала, подпись отправителя и разбор
// транскрипта, где по этому адресу узнаётся отправка человеку в панель.
func peerSelfPath() string {
	return filepath.Join(sockDir, fmt.Sprintf("%d.sock", os.Getpid()))
}

func peerSelfAddr() string { return "uds:" + peerSelfPath() }

// Сокет живёт, пока живёт процесс, и снимается на выходе.
func peerListen(logf func(string, ...any)) func() {
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		logf("свой сокет канала не поднялся: каталог %s не создался: %v", sockDir, err)
		return func() {}
	}
	path := peerSelfPath()
	// Остаток от процесса с тем же номером мешает слушать: bind по занятому
	// пути отказывает, а номера в системе переиспользуются.
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		logf("свой сокет канала %s не поднялся: %v", path, err)
		return func() {}
	}
	os.Chmod(path, 0o600)
	logf("свой конец канала сессий слушает %s", path)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetReadDeadline(time.Now().Add(peerSendTimeout))
				buf := make([]byte, 4096)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	return func() {
		ln.Close()
		os.Remove(path)
	}
}

// Настоящий дом пользователя, а не дом процесса. Демон дашборда живёт под
// launchd с подложным HOME (свой каталог конфигурации), и всякий claude,
// поднятый оттуда, считает домом его: хуки харнеса раскрывают в нём тильду и
// не находят себя, а состояние логина ищется там же, и клиент отвечает
// «Not logged in». Дом берётся от uid через getpwuid, поэтому подложный HOME на
// него не влияет вовсе.
func realHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return ""
}

// silentEnv это маркер служебного вызова клиента: хуки devkit по нему молчат.
// Без него каждая суммаризация заголовка писала в ленту уведомлений «ход
// закончен» и строку в реестр чатов, потому что claude -p это полноценная
// сессия харнеса со всеми хуками (баг девятого круга POC). Флаг --bare клиента
// тут не годится: он и правда пропускает хуки, но заодно теряет логин, и вызов
// отвечает «Not logged in».
const silentEnv = "DEVKIT_SILENT=1"

// homeEnv собирает окружение подпроцесса с настоящим домом: остальное
// наследуется как было. silent помечает вызов служебным.
func homeEnv(silent bool) []string {
	home := realHome()
	out := []string{}
	for _, kv := range os.Environ() {
		// Свои ключи переставляются заново, чтобы унаследованные не спорили с
		// поставленными тут.
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "DEVKIT_SILENT=") ||
			strings.HasPrefix(kv, "DEVKIT_NO_FOCUS=") {
			continue
		}
		out = append(out, kv)
	}
	if home != "" {
		out = append(out, "HOME="+home)
	}
	if silent {
		out = append(out, silentEnv)
	}
	// Опрос фокуса гасится всякому подпроцессу дашборда: он ходит в System
	// Events, а разрешение на это macOS просит у дашборда и заново после каждой
	// пересборки (находка одиннадцатого круга POC).
	return append(out, noFocusEnv)
}

// runProcHome это runProc с настоящим домом пользователя. Им зовётся всё, что
// поднимает клиента харнеса: под чужим домом он не найдёт ни своих хуков, ни
// своего логина.
func runProcHome(name string, args ...string) ([]byte, error) {
	return runProcQuiet("", false, name, args...)
}

// runProcQuiet это тот же запуск, помеченный служебным: хуки devkit на нём
// молчат, и лента уведомлений не наполняется ходами, которых человек не делал.
// dir задаёт рабочую директорию: клиент кладёт транскрипт в каталог по ней, и
// служебный вызов из каталога проекта всплыл бы в его списке чатов отдельной
// сессией. Пустой dir оставляет директорию процесса.
func runProcQuiet(dir string, silent bool, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), procTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env := homeEnv(silent); env != nil {
		cmd.Env = env
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s не ответил за %s и снят по сроку", name, procTimeout)
	}
	return out, err
}

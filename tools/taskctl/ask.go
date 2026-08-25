package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// Инструмент ожидания: исполнитель спрашивает человека посреди захода, а не
// последней репликой захода. Последняя реплика это возврат диспетчеру, и
// человек её в дашборде не видит вовсе; вопрос, заданный отсюда, доезжает
// уведомлением, лентой и панелью, а ответ возвращается прямо в идущий ход.
// Решение целиком в docs/lld/DK-430-task-chat.md, решение 3.
//
// Дождавшись, команда печатает ответ и продолжает заход. Не дождавшись, сама
// паркует задачу причиной «вопрос: ...»: точка парковки одна, там же, где
// вопрос (LLD DK-400, решение 2), и рук диспетчера это не ждёт.

// AskWait это срок ожидания по умолчанию. Потолок хода Bash 600 секунд, и
// запас нужен на сам вызов и на парковку. Потолок хода это не умолчание хода:
// Bash убивает команду через 120 секунд, поэтому вызов идёт с явным сроком
// хода на минуту длиннее ожидания (--wait 480 при timeout 540000), и правило
// это живёт в скиллах exec-* рядом с самим вызовом.
const AskWait = 480 * time.Second

// askPoll это шаг опроса входа. Секунда тут не цена: ждущий процесс спит, и
// модельных токенов ожидание не тратит.
const askPoll = time.Second

// askSessionEnv это переменная харнеса с ID внешней сессии. У субагента своего
// ID нет, и адрес у него общий с сессией, которая его подняла.
const askSessionEnv = "CLAUDE_CODE_SESSION_ID"

// reasonAsk это повод уведомителя у заданного вопроса. Парковке остаётся её
// task_blocked, и два события про один вопрос человек читает как «спросили» и
// «встали, ответа не дождавшись».
const reasonAsk = "task_ask"

// AskParams это разобранные аргументы команды.
type AskParams struct {
	ID       string
	Question string
	Session  string
	Wait     time.Duration
	Draft    bool
	Stdin    io.Reader
}

// askDeps это внешний мир ожидания: часы, сон, уведомитель, отметка этапа и
// парковка. Своим полем тут каждый, кого тест обязан подменить: ждать по живым
// часам и звать настоящий уведомитель прогон тестов не может, а проверять надо
// именно порядок шагов и исход по сроку.
type askDeps struct {
	Now    func() time.Time
	Sleep  func(time.Duration)
	Notify func(reason, id, title, body string) string
	Stage  func(id, note string)
	Park   func(id, reason string) (string, error)
	// Main это основной чекаут. Поле тут потому, что считает его git по
	// рабочей директории, а тест гоняет ожидание на временных корнях, где
	// git-дерева нет вовсе.
	Main string
	Home string
	// IsDraft отвечает, стоит ли за ID запись накопителя, а не строка доски.
	// Полем тут потому же, почему и остальные: тест гоняет ожидание на
	// временных корнях, где накопителя может не быть вовсе.
	IsDraft func(id string) bool
}

// liveDeps собирает боевой набор: часы машины, настоящий сон, уведомитель
// taskctl, запись этапа и парковка через тот же cmdMove, каким паркуют руками.
func liveDeps(root string) askDeps {
	main := stage.MainRoot(root)
	return askDeps{
		Now:   time.Now,
		Sleep: time.Sleep,
		Notify: func(reason, id, title, body string) string {
			return notify(main, reason, id, title, body)
		},
		Stage: func(id, note string) {
			stage.Open(stage.Home(), main, id, stage.Ask, note, time.Now())
		},
		Park: func(id, reason string) (string, error) {
			// Доска коммитится и пушится тут же, как её пушит будящий тик
			// сторожка: за паркующим никого нет, а правка доски, оставленная
			// грязной в основном чекауте, отбила бы следующий merge.
			return cmdMove(main, id, SectBlocked, reason, CommitOpts{
				Msg:  fmt.Sprintf("docs(tasks): %s припаркована вопросом", id),
				Push: true,
			})
		},
		Main: main,
		Home: stage.Home(),
		// Вид записи спрашивается у самого дерева: файл черновика лежит на
		// известном месте, и читать ради этого доску незачем.
		IsDraft: func(id string) bool {
			if id == "" {
				return false
			}
			if _, err := os.Stat(filepath.Join(root, draftRel(id))); err == nil {
				return true
			}
			_, err := os.Stat(filepath.Join(main, draftRel(id)))
			return err == nil
		},
	}
}

// askQuestions собирает вопросы из ключа --question либо из пачки JSON на
// stdin. Ключ и пачка это два входа одной команды, и вместе они не едут: в
// признаке лежала бы половина вопроса.
func askQuestions(p AskParams) ([]chat.Question, error) {
	if strings.TrimSpace(p.Question) != "" {
		return []chat.Question{{Text: strings.TrimSpace(p.Question)}}, nil
	}
	if p.Stdin == nil {
		return nil, errors.New("вопроса нет: жду --question \"...\" либо пачку вопросов JSON на stdin")
	}
	data, err := io.ReadAll(io.LimitReader(p.Stdin, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("пачка вопросов не прочиталась: %v", err)
	}
	return chat.ParsePack(data)
}

// askText сводит вопросы к одной строке: она уезжает в уведомление, в отметку
// этапа и в причину парковки, где место есть ровно на суть.
func askText(qs []chat.Question) string {
	var out []string
	for _, q := range qs {
		out = append(out, strings.Join(strings.Fields(q.Text), " "))
	}
	return strings.Join(out, "; ")
}

// askSession называет сессию, чьи реплики ожидание считает своими, и говорит,
// откуда она взялась. Порядок такой: ключ, переменная харнеса, реестр чатов по
// задаче. Не нашлось ничего, значит ожидание идёт по безадресным строкам, и это
// видно первой же строкой вывода, а не скрыто.
func askSession(p AskParams, home string, env func(string) string) (sid, note string) {
	if s := strings.TrimSpace(p.Session); s != "" {
		return s, "сессия из ключа --session"
	}
	if s := strings.TrimSpace(env(askSessionEnv)); s != "" {
		return s, "сессия из окружения хода"
	}
	if !p.Draft && home != "" {
		if s, _ := sessions.Load(home).Leads(p.ID); s != "" {
			return s, "сессия из реестра чатов"
		}
	}
	return "", "сессия не назвалась: жду безадресные реплики, адресованные другой сессии пройдут мимо"
}

// askTrees называет деревья, входы которых опрашивает ожидание. Признак пишется
// во вход основного чекаута, а опрашиваются оба, своё дерево и чекаут: ответ
// доезжает и от ручки задачи, и от панели, в какой бы из двух входов он ни лёг
// (LLD DK-430, решение 2).
func askTrees(root, main string) []string {
	if root == "" || root == main {
		return []string{main}
	}
	return []string{main, root}
}

// cmdAsk это боевой вход команды: подписка на сигналы и живые зависимости.
// Сигнал тут не украшение: признак ожидания снимается на любом выходе, и
// брошенный признак запер бы вход подхвата до конца срока.
func cmdAsk(root string, p AskParams) (string, error) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	return runAsk(root, p, liveDeps(root), os.Getenv, sig)
}

// runAsk это ожидание по шагам: признак, зов человека, отметка этапа, опрос
// входа до срока и парковка, если ответа не дождались.
func runAsk(root string, p AskParams, d askDeps, env func(string) string, sig <-chan os.Signal) (string, error) {
	p.ID = strings.ToUpper(strings.TrimSpace(p.ID))
	if p.ID == "" {
		return "", errors.New("кому вопрос: жду ID задачи")
	}
	qs, err := askQuestions(p)
	if err != nil {
		return "", err
	}
	main := d.Main
	if main == "" {
		main = stage.MainRoot(root)
	}
	// Черновик узнаётся сам, а не по флагу: заказ груминга зовёт ожидание тем
	// же `taskctl ask <ID>`, что и задача, и требовать от агента помнить про
	// вид записи значит ловить отказ парковки посреди захода (живой случай
	// DK-517: два захода по восемь минут, оба кончились «нет на доске», а
	// вопросы дошли до человека только текстом в чат).
	guessed := false
	if !p.Draft && d.IsDraft != nil && d.IsDraft(p.ID) {
		p.Draft = true
		guessed = true
	}
	// Вход у ожидания один и тот же и у задачи, и у черновика: панель дашборда
	// кладёт ответ человека в task-<ID> (sessionChatName), и слушать черновику
	// свой draft-<ID> значило бы ждать на адресе, куда никто не пишет. Разводит
	// эти два случая не адрес, а исход по сроку: задача паркуется строкой,
	// черновик оставляет вопрос файлом исхода.
	name := chat.TaskName(p.ID)
	text := askText(qs)
	sid, note := askSession(p, d.Home, env)
	var out []string
	out = append(out, fmt.Sprintf("%s: вопрос человеку, %s", p.ID, note))
	if guessed {
		out = append(out, fmt.Sprintf(
			"%s это запись накопителя, а не строка доски: ждём тем же входом, а не дождавшись, оставим вопрос файлом исхода", p.ID))
	}
	for _, q := range qs {
		out = append(out, "  ? "+q.Text)
		for _, o := range q.Options {
			mark := "  "
			if o.Recommended {
				mark = "* "
			}
			out = append(out, "    "+mark+strings.TrimSpace(o.Label+" "+o.Note))
		}
	}
	// Уведомитель и этап зовутся до опроса: человек узнаёт про вопрос сразу, а
	// не через минуту, и запись «уточнение» стоит ровно на том времени, когда
	// заход встал (её и заводили под этот случай, tools/agentctl/stage.go).
	if d.Notify != nil {
		if n := d.Notify(reasonAsk, p.ID, fmt.Sprintf("%s: вопрос по задаче", p.ID), text); n != "" {
			out = append(out, strings.TrimSpace(n))
		}
	}
	if d.Stage != nil && !p.Draft {
		d.Stage(p.ID, "вопрос: "+text)
	}
	if p.Wait <= 0 {
		// «Не жду, паркуй сразу»: ответ на такой вопрос требует действий
		// человека в мире (доступ, железо, чужая команда), и караулить его
		// заходом бессмысленно. Признак тут не пишется вовсе: ждущего нет, а
		// лежащий признак запер бы вход подхвата на пустом месте.
		return askPark(main, p, d, qs, out, text, "ожидания не было (--wait 0)")
	}
	start := d.Now()
	deadline := start.Add(p.Wait)
	ask := chat.Ask{Until: deadline, Session: sid, Task: p.ID, Questions: qs}
	if err := chat.WriteAsk(main, name, ask); err != nil {
		return "", err
	}
	// Признак снимается на любом выходе, включая падение и сигнал: заперев
	// вход, ожидание оставило бы подхват без безадресных реплик до конца срока.
	defer chat.DropAsk(main, name)
	trees := askTrees(root, main)
	out = append(out, fmt.Sprintf("жду ответа до %s, вход %s",
		deadline.Format("15:04:05"), chat.Path(main, name)))
	for {
		for _, tree := range trees {
			lines, err := chat.Take(tree, name, sid)
			if err != nil && !errors.Is(err, chat.ErrLocked) {
				return "", err
			}
			if len(lines) == 0 {
				continue
			}
			out = append(out, fmt.Sprintf("ответ человека через %s:",
				d.Now().Sub(start).Truncate(time.Second)))
			for _, l := range lines {
				out = append(out, "  "+chat.Said(l))
			}
			out = append(out, "ожидание снято, заход продолжается")
			return strings.Join(out, "\n"), nil
		}
		if !d.Now().Before(deadline) {
			break
		}
		select {
		case <-sig:
			return "", fmt.Errorf("%s: ожидание прервано сигналом, признак снят, задача не припаркована", p.ID)
		default:
		}
		d.Sleep(askPoll)
	}
	return askPark(main, p, d, qs, out, text, fmt.Sprintf("ответа нет %s", p.Wait.Truncate(time.Second)))
}

// askPark паркует задачу вопросом и говорит агенту, что заход кончается
// рубежом. Черновик доски не занимает, и парковать там нечего: вопрос остаётся
// файлом исхода, из которого его берёт экран черновика (LLD DK-354).
func askPark(main string, p AskParams, d askDeps, qs []chat.Question, out []string, text, why string) (string, error) {
	if p.Draft {
		path, err := writeDraftQuestion(main, p.ID, qs)
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %s, а файл исхода не записался: %v", p.ID, why, err))
			return strings.Join(out, "\n"), nil
		}
		out = append(out, fmt.Sprintf("%s: %s, вопрос лежит файлом исхода %s: его берёт экран черновика, а снимает подъём следующего захода",
			p.ID, why, path))
		return strings.Join(out, "\n"), nil
	}
	if d.Park == nil {
		return strings.Join(out, "\n"), nil
	}
	msg, err := d.Park(p.ID, "вопрос: "+askReason(text))
	if err != nil {
		// Парковка ответила отказом: потолок висящих вопросов, чужой статус
		// строки, неуехавшая в origin доска. Ронять заход нечем, вопрос уже
		// задан, но и молчать нельзя: где именно встала парковка, видно по
		// самой строке, и агент смотрит её сам.
		out = append(out, fmt.Sprintf("%s: %s, парковка ответила отказом: %v", p.ID, why, err))
		out = append(out, fmt.Sprintf("строку проверить самому: taskctl show %s", p.ID))
		return strings.Join(out, "\n"), nil
	}
	out = append(out, strings.TrimSpace(msg))
	out = append(out, fmt.Sprintf(
		"%s: %s, задача припаркована вопросом: заход кончается рубежом, ответ разбудит строку тиком сторожка",
		p.ID, why))
	return strings.Join(out, "\n"), nil
}

// askReasonLimit это потолок сути вопроса в причине блока: причина едет одной
// строкой в ячейку доски, и простыня там не помещается.
const askReasonLimit = 160

// askReason режет суть вопроса до причины блока. Точка резки по границе слова:
// обрубленное посреди слова человек читает как поломку, а не как сокращение.
func askReason(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= askReasonLimit {
		return text
	}
	cut := text[:askReasonLimit]
	if i := strings.LastIndex(cut, " "); i > askReasonLimit/2 {
		cut = cut[:i]
	}
	return cut + "..."
}

// writeDraftQuestion кладёт вопрос черновика файлом исхода (LLD DK-354): из
// него экран черновика берёт вопрос после смерти сессии, а снимает файл подъём
// следующего захода. Тело то же, что у признака ожидания: разбор один, и
// читателю не приходится знать про два формата.
func writeDraftQuestion(main, id string, qs []chat.Question) (string, error) {
	path := filepath.Join(main, ".devkit", "draft-"+id+".question")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	body := chat.Ask{Until: time.Now(), Task: id, Questions: qs}.Text()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

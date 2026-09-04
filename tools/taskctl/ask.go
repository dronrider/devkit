package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// Внутренний писатель признака ожидания: `taskctl ask` больше не команда
// агента, агент спрашивает штатным AskUserQuestion. В сессии, поднятой
// панелью, вызов перехватывает хук PreToolUse (hooks/ask-panel.py) и зовёт эту
// команду, чтобы положить вопрос файлом признака, припарковать строку и
// уведомить человека. Ждать ответа тут больше нечему: до DK-724 сессии
// умирали с концом хода, и headless-проходу приходилось сидеть в Bash и
// опрашивать вход сам, а живая сессия оставляет признак лежать без срока до
// самого ответа. Решение целиком в docs/lld/DK-430-task-chat.md, решение 3, и
// в docs/lld/DK-756-foreign-review.md, решение 4.

// reasonAsk это повод уведомителя у заданного вопроса. Парковке остаётся её
// task_blocked, и два события про один вопрос человек читает как «спросили» и
// «встали, ответа не дождавшись».
const reasonAsk = "task_ask"

// askSessionEnv это переменная харнеса с ID внешней сессии. У субагента своего
// ID нет, и адрес у него общий с сессией, которая его подняла.
const askSessionEnv = "CLAUDE_CODE_SESSION_ID"

// AskParams это разобранные аргументы команды.
type AskParams struct {
	ID       string
	Question string
	Session  string
	Draft    bool
	Stdin    io.Reader
}

// askDeps это внешний мир писателя: уведомитель, отметка этапа и парковка.
// Своим полем тут каждый, кого тест обязан подменить: звать настоящий
// уведомитель и настоящую парковку прогон тестов не может, а проверять надо
// именно порядок шагов.
type askDeps struct {
	Notify func(reason, id, title, body string) string
	Stage  func(id, note string)
	Park   func(id, reason string) (string, error)
	// Main это основной чекаут. Поле тут потому, что считает его git по
	// рабочей директории, а тест гоняет писателя на временных корнях, где
	// git-дерева нет вовсе.
	Main string
	Home string
	// IsDraft отвечает, стоит ли за ID запись накопителя, а не строка доски.
	// Полем тут потому же, почему и остальные: тест гоняет писателя на
	// временных корнях, где накопителя может не быть вовсе.
	IsDraft func(id string) bool
}

// liveDeps собирает боевой набор: уведомитель taskctl, запись этапа и
// парковка через тот же cmdMove, каким паркуют руками.
func liveDeps(root string) askDeps {
	main := stage.MainRoot(root)
	return askDeps{
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

// askSession называет сессию, чьи реплики признак ожидания считает своими, и
// говорит, откуда она взялась. Порядок такой: ключ, переменная харнеса,
// реестр чатов по задаче. Не нашлось ничего, значит признак ждёт безадресные
// строки, и это видно первой же строкой вывода, а не скрыто.
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

// cmdAsk это боевой вход команды.
func cmdAsk(root string, p AskParams) (string, error) {
	return runAsk(root, p, liveDeps(root), os.Getenv)
}

// runAsk кладёт вопрос признаком без срока и паркует задачу. Ждать тут
// нечего: живая сессия, поднявшая вопрос, уже кончила ход хуком, и ответ
// доедет реплике, а не этому вызову.
func runAsk(root string, p AskParams, d askDeps, env func(string) string) (string, error) {
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
	// Черновик узнаётся сам, а не по флагу: заказ груминга зовёт писателя тем
	// же ID, что и задача, и требовать от хука помнить про вид записи значит
	// ловить отказ парковки посреди захода (живой случай DK-517: два захода
	// подряд кончились «нет на доске», а вопросы дошли до человека только
	// текстом в чат).
	guessed := false
	if !p.Draft && d.IsDraft != nil && d.IsDraft(p.ID) {
		p.Draft = true
		guessed = true
	}
	// Вход один и тот же и у задачи, и у черновика: панель дашборда кладёт
	// ответ человека в task-<ID> (sessionChatName), и слушать черновику свой
	// draft-<ID> значило бы ждать на адресе, куда никто не пишет.
	name := chat.TaskName(p.ID)
	text := askText(qs)
	sid, note := askSession(p, d.Home, env)
	var out []string
	out = append(out, fmt.Sprintf("%s: вопрос человеку, %s", p.ID, note))
	if guessed {
		out = append(out, fmt.Sprintf(
			"%s это запись накопителя, а не строка доски: вопрос лежит в разговоре, парковки не будет", p.ID))
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
	// Признак без срока: DK-715 меняет саму жизнь ожидания, оно живёт до
	// ответа, что бы ни показали часы, и панель прячет обратный отсчёт.
	if err := chat.WriteAsk(main, name, chat.Ask{Session: sid, Task: p.ID, Questions: qs}); err != nil {
		return "", err
	}
	// Уведомитель зовётся сразу: человек узнаёт про вопрос немедленно, а не
	// когда-нибудь потом, и запись «уточнение» стоит ровно на том времени,
	// когда заход встал (tools/agentctl/stage.go).
	if d.Notify != nil {
		if n := d.Notify(reasonAsk, p.ID, fmt.Sprintf("%s: вопрос по задаче", p.ID), text); n != "" {
			out = append(out, strings.TrimSpace(n))
		}
	}
	if d.Stage != nil && !p.Draft {
		d.Stage(p.ID, "вопрос: "+text)
	}
	return askPark(p, d, out, text)
}

// askPark паркует задачу вопросом и говорит агенту, что заход кончается
// рубежом. Черновик доски не занимает, и парковать там нечего: вопрос уже
// лежит признаком и репликой в разговоре, отвечать на него можно и без строки
// доски (LLD DK-354).
func askPark(p AskParams, d askDeps, out []string, text string) (string, error) {
	if p.Draft {
		out = append(out, fmt.Sprintf(
			"%s: запись накопителя доски не занимает, парковать нечего: вопрос лежит в разговоре, "+
				"ответ снимет признак", p.ID))
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
		out = append(out, fmt.Sprintf("%s: парковка ответила отказом: %v", p.ID, err))
		out = append(out, fmt.Sprintf("строку проверить самому: taskctl show %s", p.ID))
		return strings.Join(out, "\n"), nil
	}
	out = append(out, strings.TrimSpace(msg))
	out = append(out, fmt.Sprintf(
		"%s: задача припаркована вопросом: заход кончается рубежом, ответ снимет признак и разбудит "+
			"строку тиком сторожка", p.ID))
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

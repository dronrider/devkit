package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Судейская секция сценария (LLD DK-805, решение 4). Рядом с машинной
// проверкой лежит критерий на естественном языке, а вердикт по нему выносит
// модель со свежим контекстом. Примеры с разметкой человека едут в самом
// файле сценария: у разметки нет срока годности, протухает согласие судьи с
// ней, и оно устанавливается заново каждым прогоном.
type Judge struct {
	Input     string // «ответ» или путь от корня проекта прогона
	Criterion string
	Yes       []string // примеры, на которых судья обязан сказать «да»
	No        []string // примеры, на которых судья обязан сказать «нет»
}

const (
	sectJudge       = "Судья"
	judgeInputReply = "ответ"
	judgeYes        = "да"
	judgeNo         = "нет"
)

// parseJudge разбирает тело секции «Судья»: ключи «вход» и «критерий», строки
// примеров «да:» и «нет:». Значение в ёлочках читается без них. Нужен хотя бы
// один пример каждого вида, иначе калибровке не на чем сойтись.
func parseJudge(lines []string, at func(i int, format string, a ...any) error) (*Judge, error) {
	j := &Judge{Input: judgeInputReply}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		key, val, ok := strings.Cut(t, ":")
		if !ok {
			return nil, at(i, "в секции «%s» жду «ключ: значение», вижу %q", sectJudge, t)
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		if val == "" {
			return nil, at(i, "пустое значение у «%s» в секции «%s»", key, sectJudge)
		}
		switch key {
		case "вход":
			if val != judgeInputReply {
				if filepath.IsAbs(val) || strings.HasPrefix(val, "../") || strings.Contains(val, "/../") {
					return nil, at(i, "вход %q это не путь от корня проекта прогона и не слово «%s»", val, judgeInputReply)
				}
			}
			j.Input = val
		case "критерий":
			j.Criterion = val
		case judgeYes:
			j.Yes = append(j.Yes, val)
		case judgeNo:
			j.No = append(j.No, val)
		default:
			return nil, at(i, "неизвестный ключ %q в секции «%s»: жду вход, критерий, %s или %s", key, sectJudge, judgeYes, judgeNo)
		}
	}
	if j.Criterion == "" {
		return nil, at(0, "в секции «%s» нет ключа «критерий»", sectJudge)
	}
	if len(j.Yes) == 0 || len(j.No) == 0 {
		return nil, at(0, "в секции «%s» нужен хотя бы один пример «%s:» и один «%s:», иначе судью не на чем калибровать",
			sectJudge, judgeYes, judgeNo)
	}
	return j, nil
}

func unquote(s string) string {
	if strings.HasPrefix(s, "«") && strings.HasSuffix(s, "»") && len(s) > len("«»") {
		return strings.TrimSuffix(strings.TrimPrefix(s, "«"), "»")
	}
	return s
}

// Промпт судьи фиксирован в стенде, а не в сценарии: форма ответа сторожится
// одним местом. Вердикт просится последней строкой после цитаты: с вердиктом
// первой строкой sonnet ставил «да» против собственной цитаты 16 раз из 20
// (DK-410).
const judgePrompt = `Ниже критерий и текст. Найди в тексте то, о чём говорит критерий.
Процитируй место, по которому судишь, и последней строкой напиши одно слово: да или нет.

Критерий: %s

Текст:
%s
`

// judge это вызов судьи: команда, которой промпт уходит на stdin, пустой HOME
// и рабочая директория вне проекта прогона. Судья слепой, ему достаются
// критерий и текст, и больше ничего.
type judge struct {
	Cmd     []string
	Model   string // как называть судью в сообщениях
	Home    string
	Dir     string
	Timeout time.Duration
}

// newJudge собирает судье свой дом: затравка, если она есть, и ссылка на
// связку ключей, как у дома прогона, а правил и определений там нет. Зовётся
// судья из своего каталога, не из проекта, иначе харнес подмешал бы правила
// раскладки.
func newJudge(work string, cmd []string, model, homeSeed, userHome string, timeout time.Duration) (*judge, error) {
	j := &judge{
		Cmd:     cmd,
		Model:   model,
		Home:    filepath.Join(work, "judge", "home"),
		Dir:     filepath.Join(work, "judge", "cwd"),
		Timeout: timeout,
	}
	for _, d := range []string{j.Home, j.Dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if homeSeed != "" {
		if err := copyTree(homeSeed, j.Home, nil); err != nil {
			return nil, fmt.Errorf("затравка HOME %s: %v", homeSeed, err)
		}
	}
	if err := linkKeychain(j.Home, userHome); err != nil {
		return nil, err
	}
	return j, nil
}

// environ это окружение судьи: без HOME машины, без переменных харнеса и без
// указателей стенда. Указатели убираются нарочно: по ним судья нашёл бы проект
// и транскрипт, а видеть он должен только то, что пришло на stdin.
func (j *judge) environ() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "HOME" || strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "OBEY_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+j.Home)
}

// ask отдаёт судье критерий и текст и возвращает его ответ целиком. Ошибка
// тут это не вердикт: харнес не ответил, вызов не уложился в потолок, ответ
// пустой. Такое останавливает стенд, а не красит клетку.
func (j *judge) ask(criterion, text string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), j.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, j.Cmd[0], j.Cmd[1:]...)
	cmd.Dir = j.Dir
	cmd.Env = j.environ()
	cmd.Stdin = strings.NewReader(fmt.Sprintf(judgePrompt, criterion, text))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return "", fmt.Errorf("вызов не уложился в %s", j.Timeout)
	}
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, trimNote(stderr.String()))
	}
	answer := strings.TrimSpace(stdout.String())
	if answer == "" {
		return "", fmt.Errorf("пустой ответ")
	}
	return answer, nil
}

// unavailable складывает текст остановки: модель судьи и ошибка харнеса
// дословно. Отметка при этом не пишется, и текст говорит об этом сам, иначе
// автор искал бы в файле задачи след, которого нет.
func (j *judge) unavailable(scenario, where string, err error) error {
	return fmt.Errorf("судья %s недоступен на сценарии %s, %s: %v; прогон остановлен, отметка не писалась",
		j.Model, scenario, where, err)
}

// judgeVerdict читает вердикт из последней непустой строки ответа. Иное слово
// это ответ не по форме: модель ответила, и это находка про критерий, а не про
// харнес.
func judgeVerdict(answer string) (string, bool) {
	lines := strings.Split(answer, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		t = strings.ToLower(strings.Trim(t, "«»\"'.!*` "))
		switch t {
		case judgeYes, judgeNo:
			return t, true
		}
		return t, false
	}
	return "", false
}

// calibrate прогоняет судью по примерам каждого сценария с секцией «Судья» до
// первой сессии. Примеры уходят судье без разметки. Расхождение хотя бы на
// одном останавливает прогон: судья, который подтверждает всё подряд, не
// стоит и одной сессии, а стоит калибровка секунды на дешёвом ярусе.
func (p Params) calibrate(live []Scenario) error {
	for _, s := range live {
		if s.Judge == nil {
			continue
		}
		type sample struct{ text, want string }
		var samples []sample
		for _, t := range s.Judge.Yes {
			samples = append(samples, sample{t, judgeYes})
		}
		for _, t := range s.Judge.No {
			samples = append(samples, sample{t, judgeNo})
		}
		for _, ex := range samples {
			answer, err := p.jury.ask(s.Judge.Criterion, ex.text)
			if err != nil {
				return p.jury.unavailable(s.ID, "калибровка", err)
			}
			got, ok := judgeVerdict(answer)
			if !ok || got != ex.want {
				return fmt.Errorf("судья сценария %s разошёлся с примером «%s»: калибровка не сошлась, "+
					"сессии не поднимались (ждал %s, ответ: %s)", s.ID, ex.text, ex.want, oneLine(answer))
			}
		}
		p.say("калибровка %s: судья %s сошёлся на %d примерах", s.ID, p.jury.Model, len(samples))
	}
	return nil
}

// judgeInput собирает текст, который судья читает: реплики ассистента из
// транскрипта либо файл проекта прогона.
func (s Scenario) judgeInput(e *runEnv) (string, error) {
	if s.Judge.Input == judgeInputReply {
		return assistantReplies(e.Transcript)
	}
	data, err := os.ReadFile(filepath.Join(e.Project, filepath.FromSlash(s.Judge.Input)))
	if err != nil {
		return "", fmt.Errorf("вход судьи %s: %v", s.Judge.Input, err)
	}
	return string(data), nil
}

// judgeOnce красит клетку по вердикту судьи. Зовётся после зелёной
// sh-проверки: она держит положительное требование и бесплатна, а судья стоит
// вызова модели.
func (p Params) judgeOnce(s Scenario, e *runEnv, a *attempt) error {
	text, err := s.judgeInput(e)
	if err != nil {
		a.Green = false
		a.Note = err.Error()
		return nil
	}
	answer, err := p.jury.ask(s.Judge.Criterion, text)
	if err != nil {
		return p.jury.unavailable(s.ID, fmt.Sprintf("повтор %d", a.Repeat), err)
	}
	a.Judge = oneLine(answer)
	verdict, ok := judgeVerdict(answer)
	switch {
	case !ok:
		a.Green = false
		a.Note = fmt.Sprintf("судья ответил не по форме: «%s»", verdict)
	case verdict == judgeNo:
		a.Green = false
		a.Note = "судья: нет"
	}
	return nil
}

// oneLine сжимает ответ судьи в строку для таблицы и хода прогона: разбор
// виден без --keep, а таблица не расползается на абзацы.
func oneLine(s string) string {
	const cap = 300
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > cap {
		return string(r[:cap]) + "..."
	}
	return string(r)
}

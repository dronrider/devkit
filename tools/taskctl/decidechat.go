package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

// Вопрос человеку текстом в ленте чата (DK-864). Блок собирает утилита из
// перечня развилок записи, а агент вставляет его в реплику как есть. Формат
// один на терминал, окно панели и субагента. Разойдись он, человек читал бы
// один и тот же вопрос двумя разными способами, а панель узнавала бы строку
// варианта только в своей половине случаев.

// strList это повторяемый ключ командной строки. Варианты ответа заводятся по
// одному ключу на вариант. Перечисление в одной строке пришлось бы делить
// разделителем, а он рано или поздно встретится внутри самого варианта.
type strList []string

func (l *strList) String() string { return strings.Join(*l, "; ") }

func (l *strList) Set(v string) error {
	if v = strings.TrimSpace(v); v != "" {
		*l = append(*l, v)
	}
	return nil
}

// chatAnswerHint это последняя строка блока: три вида ответа, которые команда
// разбирает ключом --answer.
const chatAnswerHint = "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»"

// chatOptionLine собирает строку варианта. Номер идёт первым и отделён точкой
// с пробелом. По нему панель находит строку в реплике и вешает галочку слева,
// а человек называет вариант в ответе.
func chatOptionLine(n int, c taskform.ForkChoice) string {
	if c.Recommended {
		return fmt.Sprintf("%d. рекомендую: %s", n, c.Text)
	}
	return fmt.Sprintf("%d. %s", n, c.Text)
}

// chatForkBlock собирает одну развилку: голова с именем и вопросом, ниже
// пронумерованные варианты. Развилка без рекомендации и без вариантов остаётся
// одной головой, и отвечают на неё словами.
func chatForkBlock(f taskform.Fork) string {
	out := []string{fmt.Sprintf("«%s»: %s", f.Name, f.Question)}
	for i, c := range f.Choices() {
		out = append(out, chatOptionLine(i+1, c))
	}
	return strings.Join(out, "\n")
}

// chatBlock собирает блок вопроса из пачки развилок.
func chatBlock(forks []taskform.Fork) string {
	var out []string
	for _, f := range forks {
		out = append(out, chatForkBlock(f))
	}
	out = append(out, chatAnswerHint)
	return strings.Join(out, "\n\n")
}

// chatQuestions переводит пачку в формат признака ожидания. По признаку
// панель знает, что сессия ждёт, а подхват отдаёт ждущему реплики человека.
func chatQuestions(forks []taskform.Fork) []chat.Question {
	var out []chat.Question
	for _, f := range forks {
		q := chat.Question{Text: fmt.Sprintf("«%s»: %s", f.Name, f.Question)}
		for _, c := range f.Choices() {
			q.Options = append(q.Options, chat.Option{Label: c.Text, Recommended: c.Recommended})
		}
		out = append(out, q)
	}
	return out
}

// decideOpen отбирает развилки, которые спрашивают человека.
func decideOpen(doc string) []taskform.Fork {
	var out []taskform.Fork
	for _, f := range taskform.ParseForks(doc) {
		if f.HoldsStart() {
			out = append(out, f)
		}
	}
	return out
}

// decideChat печатает блок вопроса и, в сессии панели, кладёт признак ожидания
// с парковкой строки. Обычный терминал признака не заводит. Человек видит
// вопрос прямо в ленте, и парковать строку под уже прочитанный вопрос значит
// гонять доску впустую.
func decideChat(root, doc string, p DecideParams, d askDeps, env func(string) string) (string, error) {
	open := decideOpen(doc)
	if len(open) == 0 {
		return fmt.Sprintf("%s: открытых развилок нет, спрашивать нечего", p.ID), nil
	}
	pack, rest := open, 0
	if len(pack) > chat.PackLimit {
		rest = len(pack) - chat.PackLimit
		pack = pack[:chat.PackLimit]
	}
	head := fmt.Sprintf("%s: в пачке развилок %d, блок ниже идёт в реплику как есть", p.ID, len(pack))
	if rest > 0 {
		head += fmt.Sprintf("; ещё %d уйдут следующей пачкой", rest)
	}
	out := []string{head}
	if AskPanel(env) {
		msg, err := runAsk(root, AskParams{ID: p.ID, Pack: chatQuestions(pack), Quiet: true}, d, env)
		if err != nil {
			return "", err
		}
		out = append(out, strings.TrimSpace(msg))
	}
	return strings.Join(out, "\n") + "\n\n" + chatBlock(pack), nil
}

// chatRecommended это ответ, закрывающий всю пачку рекомендациями.
const chatRecommended = "по рекомендации"

// chatNumRe узнаёт хвост куска ответа с номером варианта: «печать 2», «печать
// 2.», «печать №2».
var chatNumRe = regexp.MustCompile(`^№?\s*(\d{1,2})\.?$`)

// chatPieceRe режет ответ человека на куски. Запятая и точка с запятой это
// граница между развилками, всё остальное внутри куска.
var chatPieceRe = regexp.MustCompile(`[,;]`)

// chatDecision это разобранный кусок ответа: какую развилку закрыть и чем.
type chatDecision struct {
	Fork taskform.Fork
	Text string
}

// parseAnswer разбирает ответ человека по перечню развилок. Умного разбора тут
// нет и не заводится. Команда узнаёт имя развилки с номером варианта и слова
// «по рекомендации», а кусок, который под это не подошёл, отдаёт агенту
// строкой «не разобрано». Агент дочитывает его сам и закрывает развилку
// обычным `decide «имя» --by человек`.
func parseAnswer(answer string, forks []taskform.Fork) (done []chatDecision, left []string, err error) {
	taken := map[string]bool{}
	all := false
	for _, piece := range chatPieceRe.Split(answer, -1) {
		piece = strings.Join(strings.Fields(piece), " ")
		if piece == "" {
			continue
		}
		if strings.EqualFold(piece, chatRecommended) {
			all = true
			continue
		}
		f, tail, ok := chatNamed(piece, forks)
		if !ok {
			left = append(left, piece)
			continue
		}
		text, terr := chatChoice(f, tail)
		if terr != nil {
			return nil, nil, terr
		}
		if text == "" {
			left = append(left, piece)
			continue
		}
		taken[f.Name] = true
		done = append(done, chatDecision{Fork: f, Text: text})
	}
	if !all {
		return done, left, nil
	}
	if len(left) > 0 {
		// Рядом с «по рекомендации» стоят слова, и что человек ими исключил,
		// команде не видно. Остаток пачки она не трогает, а слова передаёт
		// агенту вместе с остальным неразобранным.
		left = append(left, "«по рекомендации» стоит рядом со словами, остаток пачки не закрыт")
		return done, left, nil
	}
	// «По рекомендации» закрывает остаток пачки: развилки, которые человек не
	// назвал поимённо. Развилка без рекомендации так не закрывается, и это
	// видно строкой «не разобрано».
	for _, f := range forks {
		if taken[f.Name] || !f.HoldsStart() {
			continue
		}
		if strings.TrimSpace(f.Hint) == "" {
			left = append(left, fmt.Sprintf("«%s» без рекомендации", f.Name))
			continue
		}
		done = append(done, chatDecision{Fork: f, Text: strings.TrimSpace(f.Hint)})
	}
	return done, left, nil
}

// chatQuotes снимает с куска ответа кавычки: человек пишет имя развилки и с
// «ёлочками», и без них.
var chatQuotes = strings.NewReplacer("«", "", "»", "", "\"", "")

// chatNamed ищет в начале куска имя развилки и отдаёт хвост после него.
// Длинное имя выигрывает у короткого. Слова «выкат» и «выкат правил»
// различаются только хвостом, и короткое имя съело бы оба.
func chatNamed(piece string, forks []taskform.Fork) (taskform.Fork, string, bool) {
	low := strings.ToLower(strings.TrimSpace(chatQuotes.Replace(piece)))
	var best taskform.Fork
	tail, found := "", false
	for _, f := range forks {
		name := strings.ToLower(f.Name)
		if !strings.HasPrefix(low, name) {
			continue
		}
		rest := strings.TrimSpace(low[len(name):])
		if rest != "" && !strings.HasPrefix(low[len(name):], " ") {
			continue
		}
		if found && len(best.Name) >= len(f.Name) {
			continue
		}
		best, tail, found = f, rest, true
	}
	return best, tail, found
}

// chatChoice переводит хвост куска в текст решения: номер варианта, слова «по
// рекомендации» или пустая строка, когда хвост не разобрался. Номер за
// пределами списка это отказ. Человек назвал вариант, которого в блоке не
// было, и молча закрыть развилку соседним значит соврать про его ответ.
func chatChoice(f taskform.Fork, tail string) (string, error) {
	choices := f.Choices()
	if tail == "" || strings.EqualFold(tail, chatRecommended) {
		if len(choices) > 0 && choices[0].Recommended {
			return choices[0].Text, nil
		}
		return "", nil
	}
	m := chatNumRe.FindStringSubmatch(tail)
	if m == nil {
		return "", nil
	}
	n, _ := strconv.Atoi(m[1])
	if n < 1 || n > len(choices) {
		return "", fmt.Errorf("у развилки «%s» вариантов %d, а в ответе номер %d: варианты лежат в записи подстроками «вариант:»",
			f.Name, len(choices), n)
	}
	return choices[n-1].Text, nil
}

// decideAnswer закрывает развилки ответом человека. Автор решения всегда
// человек. Ключ читает его строку из чата, и другого автора у неё нет.
func decideAnswer(doc string, p DecideParams) (string, string, error) {
	forks := taskform.ParseForks(doc)
	var live []taskform.Fork
	for _, f := range forks {
		if !f.Decided() {
			live = append(live, f)
		}
	}
	if len(live) == 0 {
		return "", "", fmt.Errorf("у %s открытых развилок нет: ответу нечего закрывать", p.ID)
	}
	done, left, err := parseAnswer(p.Answer, live)
	if err != nil {
		return "", "", err
	}
	if len(done) == 0 && len(left) == 0 {
		return "", "", fmt.Errorf("ответ пуст: жду «имя 1, имя 2», слова или «по рекомендации»")
	}
	date := p.Now.Format("2006-01-02")
	for _, dec := range done {
		doc, err = taskform.AppendToFork(doc, dec.Fork.Name, taskform.ForkDecisionLine(taskform.ByHuman, date, dec.Text))
		if err != nil {
			return "", "", err
		}
	}
	return doc, answerNote(p.ID, done, left), nil
}

// answerNote говорит, что команда закрыла сама, а что осталось агенту. Молчать
// про неразобранный кусок нельзя. Ответ человека уже прозвучал, и развилка,
// оставшаяся открытой, снова упрётся в ворота старта.
func answerNote(id string, done []chatDecision, left []string) string {
	var out []string
	if len(done) == 0 {
		out = append(out, fmt.Sprintf("%s: по ответу человека не закрыто ни одной развилки", id))
	} else {
		out = append(out, fmt.Sprintf("%s: закрыто развилок %d, автор решения человек", id, len(done)))
		for _, dec := range done {
			out = append(out, fmt.Sprintf("- «%s»: %s", dec.Fork.Name, dec.Text))
		}
	}
	if len(left) > 0 {
		out = append(out, "не разобрано словами: "+strings.Join(left, "; "))
		out = append(out, fmt.Sprintf("названное словами закрывает агент сам: taskctl decide %s «<имя>» --by человек \"ответ и довод\"", id))
	}
	return strings.Join(out, "\n")
}

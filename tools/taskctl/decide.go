package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// Команда `taskctl decide` пишет перечень развилок записи (LLD DK-552,
// решение 1). Форму строк и разбор держит internal/taskform, здесь остаётся
// поиск записи по ID, штампы даты и автора и печать состояний. Ручная правка
// раздела при этом законна: команда страхует случаи, которые знает, а
// остальное человек дописывает сам по форме из TASKFORM.md.

// DecideParams это разобранные аргументы команды.
type DecideParams struct {
	ID     string
	Name   string // имя развилки, «ёлочки» вокруг него необязательны
	Ask    string // вопрос заведения
	Who    string // значение поля «решает» при заведении
	Hint   string // рекомендация
	By     string // автор решения или передачи
	Text   string // ответ с доводом либо причина передачи
	Leave  bool   // передать развилку исполнителю
	Open   bool   // печатать только открытые
	Now    time.Time
	Commit CommitOpts
}

// decideAuthors переводит значение --by в слово строки записи.
var decideAuthors = map[string]string{
	"человек":     taskform.ByHuman,
	"агент":       taskform.ByAgent,
	"исполнитель": taskform.ByExecutor,
}

// decideRecord ищет запись по ID так же, как её ищет `taskctl ask`: черновик
// накопителя, файл задачи или файл цели. Закрытая задача отказывает: её файл
// уехал в архив, и дописанная там развилка не встретит ни ворот, ни читателя.
func decideRecord(root, id string) (path, kind string, err error) {
	if !idRe.MatchString(id) {
		return "", "", fmt.Errorf("%q не похож на ID задачи", id)
	}
	if p := filepath.Join(root, draftRel(id)); exists(p) {
		return p, "черновик", nil
	}
	if p := taskFilePath(root, id); exists(p) {
		return p, "файл записи", nil
	}
	if got, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "archive", "*", id+".md")); len(got) > 0 {
		return "", "", fmt.Errorf("%s закрыта, её файл лежит в %s: развилки закрытой записи не правят, вопрос заводится новой строкой",
			id, filepath.Join("docs", "tasks", "archive"))
	}
	return "", "", fmt.Errorf("записи %s нет: жду черновик docs/tasks/drafts/%s.md либо файл docs/tasks/%s.md", id, id, id)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// decideName снимает с имени «ёлочки»: команду зовут и так, и так, а в файл
// имя едет одной формой.
func decideName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "«")
	s = strings.TrimSuffix(s, "»")
	return strings.TrimSpace(s)
}

func cmdDecide(root string, p DecideParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if p.Now.IsZero() {
		p.Now = time.Now()
	}
	path, kind, err := decideRecord(root, p.ID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	doc := string(data)
	name := decideName(p.Name)
	switch {
	case p.Ask != "":
		doc, err = decideAsk(doc, p)
	case p.Leave:
		doc, err = decideLeave(doc, name, p)
	case p.By != "":
		doc, err = decideClose(doc, name, p)
	default:
		return decideShow(doc, p), nil
	}
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		return "", err
	}
	rel, rerr := filepath.Rel(root, path)
	if rerr != nil {
		rel = path
	}
	tail, err := p.Commit.apply(root, []string{rel})
	if err != nil {
		return "", err
	}
	head := fmt.Sprintf("%s (%s): ", p.ID, kind)
	switch {
	case p.Ask != "":
		head += fmt.Sprintf("развилка «%s» заведена, %s", decideName(p.Ask), decideWho(p.Who))
	case p.Leave:
		head += fmt.Sprintf("развилка «%s» оставлена исполнителю", name)
	default:
		head += fmt.Sprintf("развилка «%s» решена", name)
	}
	return head + "\n" + decideShow(doc, DecideParams{ID: p.ID}) + tail, nil
}

// decideWho поясняет в ответе, держит ли заведённая развилка старт: забытый
// ключ иначе виден только отказом ворот, а до него ещё надо дойти.
func decideWho(who string) string {
	if who == taskform.ForkExecutor {
		return "решает исполнитель, старта не держит"
	}
	return "решает человек, держит старт задачи"
}

func decideAsk(doc string, p DecideParams) (string, error) {
	who := p.Who
	if who == "" {
		who = taskform.ForkHuman
	}
	if strings.TrimSpace(p.Text) == "" {
		return "", fmt.Errorf("у развилки нет вопроса: taskctl decide %s --ask «имя» \"вопрос\"", p.ID)
	}
	return taskform.AddFork(doc, decideName(p.Ask), p.Text, who, p.Hint)
}

func decideClose(doc, name string, p DecideParams) (string, error) {
	by, ok := decideAuthors[p.By]
	if !ok {
		return "", fmt.Errorf("--by %q не из {человек, агент, исполнитель}", p.By)
	}
	if strings.TrimSpace(p.Text) == "" {
		return "", fmt.Errorf("у решения нет ответа: taskctl decide %s «%s» --by %s \"ответ и довод\"", p.ID, name, p.By)
	}
	return taskform.AppendToFork(doc, name, taskform.ForkDecisionLine(by, p.Now.Format("2006-01-02"), p.Text))
}

// decideLeave передаёт развилку исполнителю: дописывает след и правит поле
// «решает». Одного следа мало, состояние читается полем, и запись без правки
// поля держала бы старт после передачи (LLD DK-552, решение 2).
func decideLeave(doc, name string, p DecideParams) (string, error) {
	by := taskform.ByAgent
	if p.By != "" {
		v, ok := decideAuthors[p.By]
		if !ok || v == taskform.ByExecutor {
			return "", fmt.Errorf("--by %q не из {человек, агент}: развилку оставляет тот, кто ставит задачу", p.By)
		}
		by = v
	}
	f, ok := taskform.FindFork(doc, name)
	if !ok {
		return "", fmt.Errorf("развилки «%s» в перечне нет: завести её через --ask", name)
	}
	if f.Decided() {
		return "", fmt.Errorf("развилка «%s» уже решена (%s %s), оставлять нечего", name, f.By, f.Date)
	}
	doc = decideSetWho(doc, name, taskform.ForkExecutor)
	return taskform.AppendToFork(doc, name, taskform.ForkLeaveLine(by, p.Now.Format("2006-01-02"), p.Text))
}

// decideSetWho переписывает поле «решает» названной развилки. Строки нет,
// значит она встаёт первой подстрокой: перечень, правленный руками, тоже
// бывает без поля, и передача обязана оставить его читаемым.
func decideSetWho(doc, name, who string) string {
	lines := strings.Split(doc, "\n")
	f, ok := taskform.FindFork(doc, name)
	if !ok {
		return doc
	}
	head := f.Line - 1
	for i := head + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "- решает:") {
			lines[i] = taskform.ForkWhoLine(who)
			return strings.Join(lines, "\n")
		}
		if t != "" && !strings.HasPrefix(lines[i], " ") {
			break
		}
	}
	out := append([]string{}, lines[:head+1]...)
	out = append(out, taskform.ForkWhoLine(who))
	out = append(out, lines[head+1:]...)
	return strings.Join(out, "\n")
}

// decideShow печатает перечень с состояниями. Пустой перечень говорит об этом
// прямо: молчание неотличимо от «команда не нашла раздел».
func decideShow(doc string, p DecideParams) string {
	forks := taskform.ParseForks(doc)
	var out []string
	for _, f := range forks {
		if p.Open && f.Decided() {
			continue
		}
		line := fmt.Sprintf("- «%s» (%s): %s", f.Name, decideState(f), f.Question)
		if f.Hint != "" {
			line += "\n  рекомендация: " + f.Hint
		}
		if f.Decided() {
			line += fmt.Sprintf("\n  решено %s %s: %s", f.By, f.Date, f.Answer)
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		if p.Open {
			return "открытых развилок нет"
		}
		return "перечень развилок пуст"
	}
	return strings.Join(out, "\n")
}

// decideState называет состояние словом, а у открытой человеческой добавляет,
// что она держит старт: ровно это увидит тот, кому откажут ворота.
func decideState(f taskform.Fork) string {
	switch f.State() {
	case taskform.StateDecided:
		return taskform.StateDecided
	case taskform.StateLeft:
		return "оставлена исполнителю"
	default:
		return "открыта, держит старт"
	}
}

// decideGoalForks печатает открытые развилки файла цели, на который ссылается
// новая строка (LLD DK-552, решение 2): условие родителя доезжает до наследной
// строки руками того, кто её заводит, и молчать об этом нельзя. Ссылка едет из
// ячейки в виде «[tasks/DK-565.md](tasks/DK-565.md)», путь считается от docs/.
func decideGoalForks(root, link string) string {
	m := linkRe.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	target := strings.TrimSpace(m[1])
	if strings.Contains(target, "://") || !strings.HasSuffix(target, ".md") {
		return ""
	}
	if !strings.HasPrefix(target, "docs/") {
		target = filepath.Join("docs", target)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return ""
	}
	var open []taskform.Fork
	for _, f := range taskform.ParseForks(string(data)) {
		if !f.Decided() {
			open = append(open, f)
		}
	}
	if len(open) == 0 {
		return ""
	}
	out := []string{fmt.Sprintf("\nоткрытые развилки цели (%s), перенести касающиеся новой строки командой decide --ask:", target)}
	for _, f := range open {
		out = append(out, fmt.Sprintf("- «%s» (%s): %s", f.Name, decideState(f), f.Question))
	}
	return strings.Join(out, "\n")
}

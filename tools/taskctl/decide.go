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
	Name   string   // имя развилки, «ёлочки» вокруг него необязательны
	Ask    string   // вопрос заведения
	Who    string   // значение поля «решает» при заведении
	Hint   string   // рекомендация
	Opts   []string // варианты ответа, кроме рекомендованного
	By     string   // автор решения или передачи
	Text   string   // ответ с доводом либо причина передачи
	Leave  bool     // передать развилку исполнителю
	Open   bool     // печатать только открытые
	Chat   bool     // напечатать блок вопроса для реплики в чат
	Answer string   // ответ человека на блок вопроса
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

// cmdDecide это боевой вход команды. Внешний мир писателя признака собирается
// лениво. Заводить его на каждую печать перечня значило бы звать git ради
// команды, которая только читает файл.
func cmdDecide(root string, p DecideParams) (string, error) {
	return runDecide(root, p, nil, os.Getenv)
}

func runDecide(root string, p DecideParams, d *askDeps, env func(string) string) (string, error) {
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
	orig := doc
	name := decideName(p.Name)
	note := ""
	switch {
	case p.Chat:
		if d == nil {
			live := liveDeps(root)
			d = &live
		}
		return decideChat(root, doc, p, *d, env)
	case p.Answer != "":
		doc, note, err = decideAnswer(doc, p)
	case p.Ask != "":
		doc, err = decideAsk(doc, p)
	case len(p.Opts) > 0:
		doc, note, err = decideOption(doc, name, p)
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
	// Дописывание, где все варианты оказались дублями, файла не трогает.
	// Записать его тем же текстом мало: следом идёт коммит, а git на пустом
	// диффе падает вместо внятного «новых вариантов нет» (ревью DK-882).
	if len(p.Opts) > 0 && doc == orig {
		return fmt.Sprintf("%s (%s): %s", p.ID, kind, note), nil
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
	case p.Answer != "":
		head = note + "\n"
		return head + decideShow(doc, DecideParams{ID: p.ID}) + tail, nil
	case p.Ask != "":
		head += fmt.Sprintf("развилка «%s» заведена, %s", decideName(p.Ask), decideWho(p.Who))
	case len(p.Opts) > 0:
		head += note
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
	return taskform.AddFork(doc, decideName(p.Ask), p.Text, who, p.Hint, p.Opts...)
}

// decideOption дописывает варианты ответа к заведённой развилке. Вариантами
// перечень обзавёлся позже самой команды, и записи с вопросом, заведённым
// раньше, остались с одной рекомендацией: блок --chat печатал такой развилке
// один пункт, и выбирать человеку было не из чего. Повторное --ask тут не
// годится, занятое имя оно отбивает своим отказом. Дубль варианта не
// отбивает всю пачку, а называется в ответе: команду зовут списком ключей, и
// один повтор не повод терять остальные.
func decideOption(doc, name string, p DecideParams) (string, string, error) {
	f, ok := taskform.FindFork(doc, name)
	if !ok {
		return "", "", fmt.Errorf("развилки «%s» в перечне нет: завести её через --ask", name)
	}
	if f.Decided() {
		return "", "", fmt.Errorf("развилка «%s» решена (%s %s), вариант ей уже не нужен: пересмотр это второе решение под той же головой", name, f.By, f.Date)
	}
	have := map[string]bool{}
	if h := strings.TrimSpace(f.Hint); h != "" {
		have[h] = true
	}
	for _, o := range f.Options {
		have[strings.TrimSpace(o)] = true
	}
	var added, skipped []string
	for _, o := range p.Opts {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if have[o] {
			skipped = append(skipped, o)
			continue
		}
		var err error
		if doc, err = taskform.AddForkOption(doc, name, o); err != nil {
			return "", "", err
		}
		have[o] = true
		added = append(added, o)
	}
	if len(added) == 0 && len(skipped) == 0 {
		return "", "", fmt.Errorf("у --option нет текста варианта: taskctl decide %s «%s» --option \"ответ\"", p.ID, name)
	}
	note := fmt.Sprintf("развилке «%s» дописано вариантов %d", name, len(added))
	if len(added) == 0 {
		note = fmt.Sprintf("развилке «%s» дописывать нечего", name)
	}
	if len(skipped) > 0 {
		note += fmt.Sprintf(", уже были в перечне %d (%s)", len(skipped), strings.Join(skipped, "; "))
	}
	return doc, note, nil
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
		for _, o := range f.Options {
			line += "\n  вариант: " + o
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

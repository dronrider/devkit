package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Черновик это записанная на ходу идея, которой ещё не выдали метаданные:
// строки на доске у неё нет, потому что Backlog отсортирован по R, и `batch`,
// `pick` и shipctl читают доску, рассчитывая на разобранную работу. Файл лежит
// внутри docs/tasks/, и от этого достаётся даром сразу два: lintOrphanTaskFiles
// пропускает директории и на черновик не ругается, а правило про пуш доски
// (RULES.board.md, «Трекинг задач» п. 9) действует на docs/tasks/ целиком.
type Draft struct {
	ID    string
	Num   int
	Title string
	Path  string
	Age   time.Duration
	// Written это дата из строки «записан» самого файла, Mod время его правки.
	// Возраст считается по первой, а вторая остаётся фолбэком для черновиков,
	// записанных до появления строки.
	Written  time.Time
	Mod      time.Time
	Deferred string // дата пометки «отложен», пусто у неотложенного
	Prio     string // уровень разбора high / mid / low, пусто у немаркированного
}

const (
	draftGroomHeading  = "## Грумминг"
	draftWrittenPrefix = "записан "
	draftPrioPrefix    = "приоритет: "
	draftDateLayout    = "2006-01-02"
)

// draftPrioWords переводит уровень разбора в русское слово файла и печати, а
// draftPrioKeys обратно. Латинские имена живут в команде и в поле prio json,
// русские в самом файле черновика и на экране, как у суффикса приёмки.
var (
	draftPrioWords = map[string]string{"high": "высокий", "mid": "средний", "low": "низкий"}
	draftPrioKeys  = map[string]string{"высокий": "high", "средний": "mid", "низкий": "low"}
)

// draftPrioWord переводит уровень в слово файла и отбивает запись без уровня.
// Грубая оценка спрашивается на записи, а не в грумминге: метка задаёт очередь
// разбора, и поставленная грумингом она появляется тогда, когда разбор уже
// идёт (DK-520).
func draftPrioWord(prio string) (string, error) {
	switch {
	case strings.TrimSpace(prio) == "":
		return "", fmt.Errorf("жду уровень разбора: taskctl draft --prio high|mid|low \"текст идеи\"\n" +
			"  high это разбирать в ближайший заход, mid обычная очередь, low когда-нибудь;\n" +
			"  оценка грубая и на глаз, пересматривается потом через taskctl draft prio <ID>")
	case draftPrioWords[prio] == "":
		return "", fmt.Errorf("уровень %q не из шкалы: taskctl draft --prio high|mid|low \"текст идеи\"", prio)
	}
	return draftPrioWords[prio], nil
}

// draftSubs это слова, которые case "draft" узнаёт за подкоманду, а не за
// текст черновика. Черновика из одного такого слова не записать, но ограничение
// это давно действует для list и стоит того: без узнавания «draft defer DK-116
// причина» молча завёл бы черновик с текстом «defer».
var draftSubs = map[string]bool{"list": true, "defer": true, "prio": true, "attach": true, "drop": true, "ask": true}

// draftWordRe узнаёт одно слово латиницей: имя подкоманды и ID задачи выглядят
// ровно так, а записанная на ходу идея так не выглядит никогда.
var draftWordRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._/-]*$`)

// draftTextGuard отбивает запись, когда текстом черновика оказалось одно слово
// латиницей. Подкоманда узнаётся точным совпадением, и всё остальное падало в
// ветку записи: «draft show DK-162» заводил черновик с текстом «show», а
// «draft add "текст"» с текстом «add», выбросив саму идею, оба раза с кодом
// возврата 0 и обычным сообщением на экране (DK-099).
func draftTextGuard(text string) error {
	word := strings.TrimSpace(text)
	if !draftWordRe.MatchString(word) {
		return nil
	}
	return fmt.Errorf("текстом черновика пришло одно слово латиницей (%q), а так выглядит промах мимо подкоманды, а не идея.\n"+
		"  у draft есть только list, defer, prio, attach, drop; черновик целиком печатает taskctl show <ID>\n"+
		"  записать идею: taskctl draft \"текст идеи\"; если слово и есть весь текст, передай его на stdin", word)
}

// deferRe находит строку пометки в разделе «Грумминг»: «- 2026-08-05, отложен: ...»
var deferRe = regexp.MustCompile(`^-\s*(\d{4}-\d{2}-\d{2}),\s*отложен`)

func draftsDir(root string) string     { return filepath.Join(root, "docs", "tasks", "drafts") }
func draftPath(root, id string) string { return filepath.Join(draftsDir(root), id+".md") }
func draftRel(id string) string        { return filepath.Join("docs", "tasks", "drafts", id+".md") }

// loadDrafts читает накопитель. Каталога нет, значит черновиков нет: заводится
// он первой командой draft, и на проекте без черновиков его быть не должно.
func loadDrafts(root string) ([]Draft, error) {
	entries, err := os.ReadDir(draftsDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now()
	var out []Draft
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		m := idRe.FindStringSubmatch(id)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[2])
		d := Draft{ID: id, Num: num, Path: filepath.Join(draftsDir(root), e.Name())}
		if info, err := e.Info(); err == nil {
			d.Mod = info.ModTime()
		}
		meta := parseDraftFile(d.Path)
		d.Title, d.Written, d.Deferred, d.Prio = meta.Title, meta.Written, meta.Deferred, meta.Prio
		// Возраст от времени правки сбивает любая запись в файл, начиная с
		// первой же пометки, а заодно свежий клон и shipctl start со своим
		// worktree. Строка «записан» этого не боится, и фолбэк остаётся только
		// для черновиков, у которых её нет.
		if !d.Written.IsZero() {
			d.Age = now.Sub(d.Written)
		} else if !d.Mod.IsZero() {
			d.Age = now.Sub(d.Mod)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := draftOrderRank(out[i]), draftOrderRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Num < out[j].Num
	})
	return out, nil
}

// draftOrderRank это место черновика в порядке разбора: high, mid, low,
// немаркированные отдельной группой, отложенные после всех. Ранг строки доски
// отвечает на вопрос «что исполнять», а черновику нужен «что разбирать
// следующим», поэтому шкала своя и на RANKING.md не ложится.
func draftOrderRank(d Draft) int {
	if d.Deferred != "" {
		return 4
	}
	switch d.Prio {
	case "high":
		return 0
	case "mid":
		return 1
	case "low":
		return 2
	}
	return 3
}

func findDraft(drafts []Draft, id string) *Draft {
	for i := range drafts {
		if drafts[i].ID == id {
			return &drafts[i]
		}
	}
	return nil
}

// draftMeta это то, что читается из самого файла черновика: заголовок, дата
// записи, дата пометки об отложенном и уровень разбора.
type draftMeta struct {
	Title    string
	Written  time.Time
	Deferred string
	Prio     string
}

// parseDraftFile читает файл черновика одним заходом: четыре поля из одного и
// того же файла, и второй его читатель платил бы за то же самое дважды на
// каждую строку накопителя. Уровень разбора читается только из шапки до
// первого «## »: строка «приоритет:» в теле идеи это текст, а не метка.
func parseDraftFile(path string) draftMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return draftMeta{}
	}
	var m draftMeta
	head, intro, groom := true, true, false
	for _, raw := range strings.Split(string(data), "\n") {
		ln := strings.TrimSpace(raw)
		if ln == "" {
			continue
		}
		if head {
			m.Title = draftTitleLine(ln)
			head = false
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			intro = false
			groom = ln == draftGroomHeading
			continue
		}
		switch {
		case groom && m.Deferred == "":
			if g := deferRe.FindStringSubmatch(ln); g != nil {
				m.Deferred = g[1]
			}
		case intro && m.Prio == "" && strings.HasPrefix(ln, draftPrioPrefix):
			m.Prio = draftPrioKeys[strings.TrimSpace(strings.TrimPrefix(ln, draftPrioPrefix))]
		case m.Written.IsZero() && strings.HasPrefix(ln, draftWrittenPrefix):
			date := strings.TrimSpace(strings.TrimPrefix(ln, draftWrittenPrefix))
			if t, err := time.ParseInLocation(draftDateLayout, date, time.Local); err == nil {
				m.Written = t
			}
		}
	}
	return m
}

// draftTitleLine чистит первую строку файла: решётка заголовка и свой же ID
// в заголовке накопителю не нужны, их и так видно в колонке ID.
func draftTitleLine(ln string) string {
	ln = strings.TrimPrefix(ln, "# ")
	if i := strings.Index(ln, ": "); i > 0 && idRe.MatchString(ln[:i]) {
		ln = ln[i+2:]
	}
	return ln
}

// ageWords печатает возраст черновика словами: накопитель показывается людям,
// и «3 дня» читается быстрее, чем разница дат.
func ageWords(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days <= 0:
		return "сегодня"
	case days == 1:
		return "вчера"
	}
	return fmt.Sprintf("%d %s", days, dayWord(days))
}

func dayWord(n int) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "дней"
	}
	switch n % 10 {
	case 1:
		return "день"
	case 2, 3, 4:
		return "дня"
	}
	return "дней"
}

// readStdin забирает текст черновика с потока (редактор, пайп, агент). На
// терминале команда ждала бы ввода молча и выглядела бы зависшей, поэтому там
// она сразу говорит, чего ей не хватает.
func readStdin() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("нет текста черновика: передай его аргументом (taskctl draft \"...\") либо на stdin")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// cmdDraft записывает сырую идею файлом и выдаёт ей ID. Метаданные не
// спрашиваются вовсе, кроме грубого уровня разбора: грумминг в момент, когда
// мысль только проявилась, стоит дороже самой мысли, и на этом она теряется, а
// уровень это один флаг и ответ на вопрос «разбирать ли это сегодня».
func cmdDraft(root, text, prio string, c CommitOpts) (string, error) {
	return cmdDraftFrom(root, text, prio, false, c)
}

// cmdDraftFrom это cmdDraft со знанием, откуда пришёл текст: отказ по первой
// строке советует stdin только тому, кто пришёл аргументом.
func cmdDraftFrom(root, text, prio string, viaStdin bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	word, err := draftPrioWord(prio)
	if err != nil {
		return "", err
	}
	// Номер черновика это та же сквозная нумерация, что у доски, поэтому
	// выдаётся он там же, где правят доску: с фичеветки такой ID остался бы
	// невидимым для основного чекаута и достался бы второй задаче.
	if err := boardGuard(root, "draft"); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("пустой черновик: текст передаётся аргументом либо на stdin")
	}
	if err := draftTitleGuard(text, viaStdin); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	id, err := nextID(b, arch, drafts)
	if err != nil {
		return "", err
	}
	title, rest, _ := strings.Cut(text, "\n")
	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(title), "# "))
	rel := filepath.Join("docs", "tasks", "drafts", id+".md")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	body := fmt.Sprintf("# %s: %s\n\n%s%s\n\n%s",
		id, title, draftWrittenPrefix, time.Now().Format(draftDateLayout), draftBodySection(rest))
	// Метку кладёт та же setPrio, что и draft prio: место строки в шапке
	// описано в одном месте, и запись с пересмотром не расходятся.
	t := &draftText{path: abs, lines: strings.Split(body, "\n")}
	t.setPrio(word)
	body = strings.Join(t.lines, "\n")
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{rel})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: черновик в %s, оформить: taskctl add --id %s --title \"...\" --type ... --rank \"...\"%s",
		id, filepath.ToSlash(rel), id, tail), nil
}

// cmdDraftList печатает накопитель: что записано, о чём и как давно. Строка
// это заголовок черновика, тело остаётся в файле и читается через
// taskctl show <ID>: накопитель просматривают целиком, и разбор десятка
// черновиков телами стоит дороже самого разбора.
func cmdDraftList(root string) (string, error) {
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	if len(drafts) == 0 {
		return "черновиков нет", nil
	}
	out := make([]string, 0, len(drafts))
	for _, d := range drafts {
		age := draftAgeWords(d, true)
		out = append(out, fmt.Sprintf("%s (%s): %s", d.ID, age, clipTitle(d.Title)))
	}
	return strings.Join(out, "\n"), nil
}

// draftAgeWords собирает пометки в скобках строки накопителя: возраст, уровень
// разбора и «отложен». Печатают их draft list и хвост list одними и теми же
// словами, чтобы заход по накопителю не расходился сам с собой; дата у
// «отложен» едет только в полный список, хвосту хватает слова.
func draftAgeWords(d Draft, withDate bool) string {
	age := ageWords(d.Age)
	if p := draftPrioWords[d.Prio]; p != "" {
		age += ", " + p
	}
	if d.Deferred != "" {
		age += ", отложен"
		if withDate {
			age += " " + d.Deferred
		}
	}
	return age
}

// draftsLine это хвост taskctl list: черновик не виден на доске, и без такой
// строки накопитель превращается в свалку, о которой никто не вспоминает.
func draftsLine(drafts []Draft) string {
	if len(drafts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(drafts))
	for _, d := range drafts {
		parts = append(parts, fmt.Sprintf("%s (%s)", d.ID, draftAgeWords(d, false)))
	}
	return fmt.Sprintf("Черновики (%d, целиком: taskctl draft list): %s", len(drafts), strings.Join(parts, ", "))
}

// promoteDraft переносит черновик в docs/tasks/<ID>.md: грумминг доводит
// taskctl add --id, отдельной команды на промоушен нет, иначе черновик можно
// потерять, забыв её позвать. Перенос через git mv, как в close. Возвращаемое
// staged=true значит, что перенос шёл через git mv (git знал исходный путь):
// только тогда исходный путь уместен в pathspec коммита. На неотслеживаемом
// черновике git mv отбивается, срабатывает rename, и возвращается staged=false.
func promoteDraft(root, id, title, rank string) (promoted, staged bool, err error) {
	from := draftPath(root, id)
	data, err := os.ReadFile(from)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	to := filepath.Join(root, "docs", "tasks", id+".md")
	staged, err = gitMv(root, from, to)
	if err != nil {
		return false, false, err
	}
	// Файл задачи собирается по форме, а не переезжает как есть: шапка
	// черновика (метка разбора, дата записи) и подразделы SCQA раскладываются
	// по разделам TASKFORM.md, а метаданные в файле задачи не дублируются
	// (RULES.board.md, «Трекинг задач» п. 3).
	body := renderTaskFromDraft(id, title, rank, string(data), time.Now().Format(draftDateLayout))
	if err := os.WriteFile(to, []byte(body), 0o644); err != nil {
		return false, false, err
	}
	return true, staged, nil
}

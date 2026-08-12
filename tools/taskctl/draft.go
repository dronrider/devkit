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
}

const (
	draftGroomHeading  = "## Грумминг"
	draftWrittenPrefix = "записан "
	draftDateLayout    = "2006-01-02"
)

// draftSubs это слова, которые case "draft" узнаёт за подкоманду, а не за
// текст черновика. Черновика из одного такого слова не записать, но ограничение
// это давно действует для list и стоит того: без узнавания «draft defer DK-116
// причина» молча завёл бы черновик с текстом «defer».
var draftSubs = map[string]bool{"list": true, "defer": true, "attach": true, "drop": true}

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
		"  у draft есть только list, defer, attach, drop; черновик целиком печатает taskctl show <ID>\n"+
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
		d.Title, d.Written, d.Deferred = meta.Title, meta.Written, meta.Deferred
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
	sort.Slice(out, func(i, j int) bool { return out[i].Num < out[j].Num })
	return out, nil
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
// записи и дата пометки об отложенном.
type draftMeta struct {
	Title    string
	Written  time.Time
	Deferred string
}

// parseDraftFile читает файл черновика одним заходом: три поля из одного и того
// же файла, и второй его читатель платил бы за то же самое дважды на каждую
// строку накопителя.
func parseDraftFile(path string) draftMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return draftMeta{}
	}
	var m draftMeta
	head, groom := true, false
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
			groom = ln == draftGroomHeading
			continue
		}
		switch {
		case groom && m.Deferred == "":
			if g := deferRe.FindStringSubmatch(ln); g != nil {
				m.Deferred = g[1]
			}
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
// спрашиваются вовсе: грумминг в момент, когда мысль только проявилась, стоит
// дороже самой мысли, и на этом она теряется.
func cmdDraft(root, text string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
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
	title, _, _ := strings.Cut(text, "\n")
	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(title), "# "))
	rel := filepath.Join("docs", "tasks", "drafts", id+".md")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	body := fmt.Sprintf("# %s: %s\n\n%s%s\n\n## Черновик\n\n%s\n",
		id, title, draftWrittenPrefix, time.Now().Format(draftDateLayout), text)
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

// cmdDraftList печатает накопитель: что записано, о чём и как давно.
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
		age := ageWords(d.Age)
		if d.Deferred != "" {
			age += ", отложен " + d.Deferred
		}
		out = append(out, fmt.Sprintf("%s (%s): %s", d.ID, age, d.Title))
	}
	return strings.Join(out, "\n"), nil
}

// draftsLine это хвост taskctl list: черновик не виден на доске, и без такой
// строки накопитель превращается в свалку, о которой никто не вспоминает.
func draftsLine(drafts []Draft) string {
	if len(drafts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(drafts))
	for _, d := range drafts {
		age := ageWords(d.Age)
		if d.Deferred != "" {
			age += ", отложен"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", d.ID, age))
	}
	return fmt.Sprintf("Черновики (%d, целиком: taskctl draft list): %s", len(drafts), strings.Join(parts, ", "))
}

// promoteDraft переносит черновик в docs/tasks/<ID>.md: грумминг доводит
// taskctl add --id, отдельной команды на промоушен нет, иначе черновик можно
// потерять, забыв её позвать. Перенос через git mv, как в close. Возвращаемое
// staged=true значит, что перенос шёл через git mv (git знал исходный путь):
// только тогда исходный путь уместен в pathspec коммита. На неотслеживаемом
// черновике git mv отбивается, срабатывает rename, и возвращается staged=false.
func promoteDraft(root, id string) (promoted, staged bool, err error) {
	from := draftPath(root, id)
	if _, err := os.Stat(from); err != nil {
		return false, false, nil
	}
	staged, err = gitMv(root, from, filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		return false, false, err
	}
	return true, staged, nil
}

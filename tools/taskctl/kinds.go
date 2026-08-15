package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// taskctl kinds это сверка назначения видов приёмки (LLD DK-292, решение 6):
// четыре счёта из того, что уже лежит на диске, без новых записей. Сводка
// отчёт, а не ворота: чинится по ней текст критериев, решает человек.

// kindsClosedSince это граница честности архивных строк. У закрытых раньше
// вида в строке не стояло вовсе, и выводить его задним числом нельзя (LLD,
// решение 6). Граница это 2026-08-15, день когда флаг --accept стал
// обязательным на всех входах (DK-301) и открытые строки получили вид по
// критериям (DK-303): у закрывшихся позже отсутствие суффикса это назначенный
// агентский вид, а не «вида не назначали».
const kindsClosedSince = "2026-08-15"

// kindsRevisedSince это дата появления самой практики пересмотра: строку
// пересмотра ввела DK-298 2026-08-13. Строка несёт свою дату, и более ранняя
// дата в ней это неразобранная правка, а не пересмотр.
const kindsRevisedSince = "2026-08-13"

// revisionLineRe узнаёт строку пересмотра вида в разделе «Приёмка»
// (ACCEPTANCE.md, «Пересмотр по ходу работы»): «- вид: agent, пересмотрен с
// user 2026-08-14 вниз: причина». Дата и направление обязательны, без них
// след не разбирается.
var revisionLineRe = regexp.MustCompile(`^- вид: (agent|mixed|user), пересмотрен с (agent|mixed|user) (2[0-9]{3}-[0-9]{2}-[0-9]{2}) (вниз|вверх)`)

// kindsTask это одна задача в счёте: вид из строки, где строка стоит и что
// нашлось в файле задачи.
type kindsTask struct {
	id      string
	kind    string // вид из суффикса строки, agent это умолчание
	arched  bool   // строка в архиве, не на живой доске
	barrier string // ключ барьера из раздела «Приёмка», пусто если его нет
	deploy  bool   // есть ли раздел «Выкат» (код слит)
	notes   int    // замечания ревью без «чистых» вердиктов
	failed  bool   // живая строка несёт непогашенный признак провала
}

// cmdKinds собирает сводку по видам приёмки из живой доски и архива. Счёт
// пересмотров отдельный: его след несёт собственную дату и переживает любую
// границу закрытия, поэтому идёт по всем файлам задач, включая закрытые до
// kindsClosedSince.
func cmdKinds(root string) (string, error) {
	revDown, revUp, err := kindsRevisions(root)
	if err != nil {
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
	var tasks []kindsTask
	var noKind []string
	take := func(id, title string, arched bool) {
		// Суффикс стоит, но не разбирается: у строки вида нет, а не «agent по
		// умолчанию». Так выглядит опечатка в значении при ручной правке
		// доски, и ноль строк без вида обязан её видеть.
		_, _, acceptSuf, _, _ := splitTitle(title)
		if acceptSuf == "" && strings.Contains(title, "[приёмка:") {
			noKind = append(noKind, id)
			return
		}
		tasks = append(tasks, readKindsTask(root, id, acceptOf(title), arched))
	}
	for _, r := range b.Rows {
		take(r.ID, r.Title, false)
	}
	for _, row := range arch.Rows {
		if row.Cells[4] < kindsClosedSince {
			continue
		}
		take(row.ID, row.Cells[1], true)
	}
	// Признак провала живёт в заголовке живой строки и гасится при починке,
	// поэтому первый след дешёвой стороны это непогашенные провалы на доске.
	for i := range tasks {
		if tasks[i].arched {
			continue
		}
		if row := b.find(tasks[i].id); row != nil {
			_, _, _, failSuf, _ := splitTitle(row.Title)
			tasks[i].failed = failSuf != ""
		}
	}
	return kindsRender(tasks, noKind, revDown, revUp), nil
}

// kindsRevisions собирает следы пересмотра вида по всем файлам задач, живым и
// архивным: строка пересмотра несёт свою дату, и пересмотр 2026-08-14 у
// задачи, закрытой раньше границы kindsClosedSince, остаётся честным счётом.
// У DK-336 раздел «Приёмка» в файле стоит дважды (скелет от add и итоговый от
// пересмотра), поэтому читаются все вхождения заголовка, а не первое.
func kindsRevisions(root string) (down, up []string, err error) {
	patterns := []string{
		filepath.Join(root, "docs", "tasks", "*.md"),
		filepath.Join(root, "docs", "tasks", "archive", "*", "*.md"),
	}
	for _, pat := range patterns {
		files, gerr := filepath.Glob(pat)
		if gerr != nil {
			return nil, nil, gerr
		}
		for _, f := range files {
			data, rerr := os.ReadFile(f)
			if rerr != nil {
				continue
			}
			id := strings.TrimSuffix(filepath.Base(f), ".md")
			scanRevisionLines(string(data), func(dir string) {
				if dir == "вниз" {
					down = append(down, id)
				} else {
					up = append(up, id)
				}
			})
		}
	}
	return down, up, nil
}

// scanRevisionLines проходит файл и отдаёт направление каждой найденной строки
// пересмотра вне ограждённых блоков кода. Заголовок «## Приёмка» может
// встретиться в файле дважды, строка пересмотра узнаётся по своей форме, а не
// по разделу: вне «Приёмка» такой строки не бывает, а пропустить вторую
// секцию значило бы потерять единственный след (DK-336).
func scanRevisionLines(text string, give func(dir string)) {
	fence := ""
	for _, ln := range strings.Split(text, "\n") {
		if m := fenceRe.FindStringSubmatch(ln); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := revisionLineRe.FindStringSubmatch(ln); m != nil && m[3] >= kindsRevisedSince {
			give(m[4])
		}
	}
}

// kindsRender сворачивает задачи в четыре счёта и печатает их. Пересмотры
// приходят отдельным списком: они собираются по всем файлам задач, а не только
// по строкам в счёте. Отдельная функция ради теста: счёты утверждаются по
// известному набору задач.
func kindsRender(tasks []kindsTask, noKind, revDown, revUp []string) string {
	var live, agent, mixed, user int
	var pricey, fails, quiet []string
	for _, t := range tasks {
		if !t.arched {
			live++
		}
		switch t.kind {
		case acceptAgent:
			agent++
		case acceptMixed:
			mixed++
		case acceptUser:
			user++
		}
		if (t.kind == acceptMixed || t.kind == acceptUser) && t.barrier == "" {
			pricey = append(pricey, t.id)
		}
		if t.failed && t.kind == acceptAgent {
			fails = append(fails, t.id)
		}
		// Третий след дешёвой стороны судит только закрытые: у живой задачи
		// выкат может просто ещё не доехать, а ревью не зваться, и класса
		// «мелочь мимо ветки» тут нет.
		if t.arched && t.kind == acceptAgent && !t.deploy && t.notes == 0 {
			quiet = append(quiet, t.id)
		}
	}
	join := func(ids []string) string {
		if len(ids) == 0 {
			return ""
		}
		return " (" + strings.Join(ids, ", ") + ")"
	}
	out := []string{
		fmt.Sprintf("строк в счёте: %d (живых %d, закрытых с %s: %d)",
			len(tasks), live, kindsClosedSince, len(tasks)-live),
		fmt.Sprintf("распределение по видам: agent %d, mixed %d, user %d", agent, mixed, user),
		fmt.Sprintf("дорогая сторона: строк user и mixed без барьера в «Приёмка»: %d%s",
			len(pricey), join(pricey)),
		fmt.Sprintf("дешёвая сторона: провалов проверки %d%s, понижений вида %d%s, закрыто агентских без выката и без замечаний ревью: %d%s",
			len(fails), join(fails), len(revDown), join(revDown), len(quiet), join(quiet)),
		fmt.Sprintf("пересмотры: вниз %d%s, вверх %d%s",
			len(revDown), join(revDown), len(revUp), join(revUp)),
		fmt.Sprintf("строк без вида: %d%s", len(noKind), join(noKind)),
	}
	return strings.Join(out, "\n")
}

// readKindsTask читает файл задачи и собирает по нему всё, что сводке нужно:
// барьер и строку пересмотра из «Приёмка», раздел «Выкат» и замечания ревью.
// Файла может не быть (задача со ссылкой или без файла вовсе), тогда читаемое
// остаётся нулями, и задача идёт в счёт по одной строке доски.
func readKindsTask(root, id, kind string, arched bool) kindsTask {
	t := kindsTask{id: id, kind: kind, arched: arched}
	path := taskFilePath(root, id)
	if arched {
		// Файл закрытой задачи лежит в tasks/archive/<год>/: разделы
		// «Приёмка» и «Выкат» переживают close и уезжают в архив вместе с
		// ним, а год в пути заранее неизвестен, поэтому glob по годам.
		path = filepath.Join(root, "docs", "tasks", "archive", "*", id+".md")
	}
	if text, found, ok := readSectionGlob(path, acceptanceHeading); ok && found {
		t.barrier, _ = parseAcceptance(text)
	}
	if _, found, ok := readSectionGlob(path, mergedSection); ok {
		t.deploy = found
	}
	if matches, _ := filepath.Glob(path); len(matches) > 0 {
		if rf, err := loadReview(matches[0]); err == nil {
			for _, n := range rf.notes {
				if n.outcome() != "чисто" {
					t.notes++
				}
			}
		}
	}
	return t
}

// readSectionGlob читает раздел файла задачи по пути или glob-шаблону. ok=false
// значит, что файла нет; found=false что раздела в нём нет.
func readSectionGlob(pattern, heading string) (text string, found, ok bool) {
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return "", false, false
	}
	return readSectionFromPath(matches[0], heading)
}

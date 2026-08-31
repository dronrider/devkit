package main

import (
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/stage"
)

// branchOfTask повторяет опознание ветки задачи из shipctl
// (tools/shipctl/worktree.go): ветка называется по ID строчными, с
// хвостом-слагом или без (`dk-005`, `dk-005-worktree`). Общего пакета у утилит
// нет, а функция в две строки.
func branchOfTask(branch, id string) bool {
	b, low := strings.ToLower(branch), strings.ToLower(id)
	return b == low || strings.HasPrefix(b, low+"-")
}

// gitMainBranch называет ветку main или master, пустую строку вне git и там,
// где ни той, ни другой нет.
func gitMainBranch(root string) string {
	for _, b := range []string{"main", "master"} {
		if _, err := gitRevParse(root, "--verify", b); err == nil {
			return b
		}
	}
	return ""
}

// unmergedTaskBranch ищет живую ветку задачи, у которой есть коммиты, не
// доехавшие до main. Такая ветка значит, что код задачи лежит только у автора,
// каким бы ни был статус строки. Вне git и в корп-контуре, где доска отдельно
// от кода, ветки не найдётся, и рубеж молча пропускает: врать про несделанное
// слияние хуже, чем его не заметить.
func unmergedTaskBranch(root, id string) (branch string, ahead int) {
	main := gitMainBranch(root)
	if main == "" {
		return "", 0
	}
	out, err := exec.Command("git", "-C", root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return "", 0
	}
	for _, b := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if b == "" || b == main || !branchOfTask(b, id) {
			continue
		}
		cnt, err := exec.Command("git", "-C", root, "rev-list", "--count", main+".."+b).Output()
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(cnt)))
		if err != nil || n == 0 {
			continue
		}
		return b, n
	}
	return "", 0
}

// scenarioElsewhere отвечает, ведёт ли ссылка строки не в файл задачи, а в
// другой документ: LLD-задача описывает проверку прямо в дизайне
// (RULES.board.md, «Трекинг задач» п. 6), и файла задачи у неё может не быть.
// Заглушка вида «(LLD позже)» ссылкой не считается: смотреть по ней некуда.
func scenarioElsewhere(link, id string) bool {
	m := linkRe.FindStringSubmatch(link)
	if m == nil {
		return false
	}
	return path.Base(m[1]) != id+".md"
}

// checkGate держит два рубежа перевода в Check: готовый сценарий проверки и
// слитый код. Оба нарушения приходят от слабой модели, которая прозу правил не
// держит, а через move ходит обязательно, поэтому отказ называет причину и
// следующий шаг.
func checkGate(root string, row *Row) error {
	id := row.ID
	if !scenarioElsewhere(row.Link, id) {
		has, hasFile := hasScenario(root, id)
		rel := "docs/tasks/" + id + ".md"
		switch {
		case !hasFile:
			return fmt.Errorf("%s: в Check пускают только с готовым сценарием проверки, а файла %s нет: завести «taskctl file %s», описать шаги и ожидаемый итог разделом «Сценарий проверки» и повторить move", id, rel, id)
		case !has:
			return fmt.Errorf("%s: в %s нет раздела «Сценарий проверки», без него в Check нельзя: описать шаги и ожидаемый итог (RULES.board.md, «Трекинг задач» п. 6) и повторить move", id, rel)
		}
	}
	if br, ahead := unmergedTaskBranch(root, id); br != "" {
		return fmt.Errorf("%s: ветка %s не слита, впереди main её коммитов %d, а Check значит, что код уже доехал до пользователя: сначала «shipctl merge %s», он и переведёт строку", id, br, ahead, id)
	}
	// Не агентский вид требует раздел «Приёмка» с перебором обходов
	// (LLD DK-292, решение 4): машина считает строки, ревьювер судит причины.
	if kind := acceptOf(row.Title); kind != acceptAgent {
		if err := acceptGate(root, id, kind); err != nil {
			return err
		}
	}
	return nil
}

// acceptGate проверяет, что у не агентского вида в файле задачи стоит раздел
// «Приёмка» с ключом барьера из шести и строкой перебора на каждый обход этого
// барьера. Судить убедительность причины обхода машина не берётся, это работа
// ревьювера; здесь только счёт по закрытому списку.
func acceptGate(root, id, kind string) error {
	rel := "docs/tasks/" + id + ".md"
	text, found, ok := acceptanceSection(root, id)
	if !ok {
		return fmt.Errorf("%s: вид приёмки %s, а файла %s нет: вид с барьером требует раздел «Приёмка» в файле задачи (LLD DK-292, решение 3)", id, kind, rel)
	}
	if !found {
		return fmt.Errorf("%s: вид приёмки %s, а раздела «Приёмка» в %s нет: назвать барьер и перебрать обходы (LLD DK-292, решение 1) и повторить move", id, kind, rel)
	}
	barrier, bypasses := parseAcceptance(text)
	if barrier == "" {
		return fmt.Errorf("%s: в разделе «Приёмка» нет строки «- барьер «<ключ>»:», а вид %s её требует", id, kind)
	}
	want, known := acceptBarriers[barrier]
	if !known {
		return fmt.Errorf("%s: барьер «%s» в разделе «Приёмка» не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", id, barrier)
	}
	if bypasses != want {
		return fmt.Errorf("%s: у барьера «%s» обходов %d, а перебор в «Приёмка» имеет строк %d (нужно столько же, по строке на обход с исходом)", id, barrier, want, bypasses)
	}
	return nil
}

// closeAgentGate требует у агентского вида непустой раздел «Проверка» в файле
// задачи (LLD DK-292, решение 4, рубеж против фиктивного агентского сценария).
// Проверяется наличие, а не честность: пустое закрытие агентской задачи
// становится невозможным, и это ровно та работа, которую машина может сделать,
// не берясь судить содержание. Не агентский вид воротами close не трогается:
// его перебор обходов держат ворота move check (acceptGate). Исключений нет:
// агентского LLD не бывает (решение 1), поэтому у агентского вида файла нет
// только в вырожденном случае (сценарий живёт по ссылке в другом документе),
// и ворота его пропускают молча. Файл есть значит обязан быть непустой раздел
// «Проверка» с реальным выводом прогона.
func closeAgentGate(root, id string) error {
	text, found, ok := verificationSection(root, id)
	if !ok {
		// Файла нет: сценарий живёт в другом документе (LLD) или ссылки на
		// файл не стоит. Агентского LLD не бывает, и ворота этот случай не
		// судят: им не о чём спорить без файла.
		return nil
	}
	if !found || strings.TrimSpace(text) == "" {
		rel := "docs/tasks/" + id + ".md"
		return fmt.Errorf("%s: вид agent требует непустой раздел «Проверка» в %s: выкатить код и вложить туда реальный вывод прогона (RULES.board.md, «Трекинг задач» п. 6), пустое закрытие агентской задачи запрещено", id, rel)
	}
	return nil
}

// closeVerifyGate сверяет прогонявшего сценарий с исполнителем последнего
// этапа «разработка» (DK-642): в цикле DK-138 три дефекта проехали ревью, и
// поймал их только прогон сценария чужими руками, поэтому прогон под тем же
// именем закрытия не даёт. Задача без записи прогона проходит молча: на этом
// держатся автозакрытие тиком (DK-516) и задачи, закрытые до записи
// прогонявшего. Оба имени ищутся в двух местах: в разделе «Ход работы» файла
// задачи, куда этапы уносит смена статуса, и в незакрытом пакете этапов, где
// запись прогона лежит до самого закрытия.
func closeVerifyGate(root, id string) error {
	lines, pending := stageSources(root, id)
	runner, ok := lastVerifyRunner(lines, pending)
	if !ok {
		return nil
	}
	dev, ok := lastDevExecutor(lines, pending)
	if !ok {
		return nil
	}
	if runner == dev {
		return fmt.Errorf("%s: сценарий прогнал %s, он же исполнитель последнего этапа «разработка»: сценарий прогоняет не автор правки, прогон другой моделью отмечается «agentctl stage %s %s --by <модель>»",
			id, runner, id, stage.Verify)
	}
	return nil
}

// stageSources собирает оба источника записей об этапах: строки раздела «Ход
// работы» и незакрытый пакет из ~/.devkit/runs. Отсутствие файла или раздела
// это пустой список, ворота по нему молчат.
func stageSources(root, id string) ([]string, []stage.Stage) {
	var lines []string
	if text, found, ok := readTaskSection(root, id, stageSection); ok && found {
		lines = strings.Split(text, "\n")
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		return lines, nil
	}
	return lines, rec.Stages
}

// lastVerifyRunner находит последнюю запись прогона сценария: сперва в
// незакрытом пакете, он свежее файла, потом в строках «Хода работы».
func lastVerifyRunner(lines []string, pending []stage.Stage) (string, bool) {
	for i := len(pending) - 1; i >= 0; i-- {
		if pending[i].Kind != stage.Verify {
			continue
		}
		if name, ok := stage.VerifyRunner(pending[i].Note); ok {
			return name, true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if name, ok := stage.VerifyRunner(lines[i]); ok {
			return name, true
		}
	}
	return "", false
}

// lastDevExecutor находит модель исполнителя последнего этапа «разработка».
// В строках файла этап узнаётся по ярлыку строки: текст ревью тоже несёт
// вердикт pick, и без ярлыка ревьювер сошёл бы за исполнителя.
func lastDevExecutor(lines []string, pending []stage.Stage) (string, bool) {
	for i := len(pending) - 1; i >= 0; i-- {
		if pending[i].Kind != stage.Dev {
			continue
		}
		if name, ok := stage.Executor(pending[i].Note); ok {
			return name, true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(ln, "- Разработка:") {
			continue
		}
		if name, ok := stage.Executor(ln); ok {
			return name, true
		}
	}
	return "", false
}

// lintUnmerged ловит обратную сторону того же рубежа: строка уже в Check, а
// ветка с её ID живёт с неслитыми коммитами. Так выглядит перевод в обход
// ворот (правку доски руками отменить некому) и брошенное на полпути слияние.
func lintUnmerged(root string, b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Sects[SectCheck].Rows {
		if br, ahead := unmergedTaskBranch(root, r.ID); br != "" {
			finds = append(finds, fmt.Sprintf("%s:%d: %s в Check, а ветка %s не слита (коммитов впереди main: %d), слить: shipctl merge %s",
				bp, r.LineIdx+1, r.ID, br, ahead, r.ID))
		}
	}
	return finds
}

// promptPages это страницы корня, которые агент читает как контракт: форма
// задачи, шкала ранга и виды приёмки. Правки правил ловятся отдельно, по
// началу имени `RULES`.
var promptPages = map[string]bool{"TASKFORM.md": true, "RANKING.md": true, "ACCEPTANCE.md": true}

// promptPath отвечает, едет ли текст файла в контекст агента: скилл,
// определение субагента, файл правил или страница-контракт корня. Проза
// внутри kit/skills (README соседей, вспомогательные скрипты) сюда попадает
// заодно, и это дешевле разбора расширений: подсказка ничего не запрещает.
func promptPath(p string) bool {
	if strings.HasPrefix(p, "kit/skills/") || strings.HasPrefix(p, "kit/agents/") {
		return true
	}
	if strings.Contains(p, "/") {
		return false
	}
	return strings.HasPrefix(p, "RULES") || promptPages[p]
}

// subjectHasTask отвечает, стоит ли ID задачи в теме коммита. Хвостовая цифра
// отсекается: иначе коммиты DK-4480 считались бы коммитами DK-448.
func subjectHasTask(subject, id string) bool {
	rest := subject
	for {
		i := strings.Index(rest, id)
		if i < 0 {
			return false
		}
		rest = rest[i+len(id):]
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			return true
		}
	}
}

// taskPromptFiles собирает промпты, тронутые коммитами задачи. Источник это
// коммиты с ID в теме по всем ссылкам, а не дифф ветки против main: к моменту
// move check код обычно уже слит (ворота выше того и требуют, а сам move
// зовёт shipctl merge после слияния), и дифф ветки там пуст. По коммитам
// подсказка работает на обоих концах: до слияния они лежат в ветке, после в
// main, а мелочь без ветки коммитится в main сразу.
func taskPromptFiles(root, id string) []string {
	out, err := exec.Command("git", "-C", root, "log", "--all", "-F", "--grep="+id,
		"--format=%H%x09%s", "-n", "100").Output()
	if err != nil {
		return nil
	}
	var shas []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sha, subject, ok := strings.Cut(ln, "\t")
		if ok && subjectHasTask(subject, id) {
			shas = append(shas, sha)
		}
	}
	if len(shas) == 0 {
		return nil
	}
	files, err := exec.Command("git", append([]string{"-C", root, "show", "--name-only",
		"--format=", "--no-renames"}, shas...)...).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(strings.TrimSpace(string(files)), "\n") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || !promptPath(p) {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// promptHint печатает одну строку про стенд, когда задача правила промпты.
// Отказа тут нет: цена прогона стенда высока, и решает её платить человек, а
// ворота лишь называют повод, пока задача ещё на виду (LLD DK-448 в файле
// задачи, раздел «Границы»).
func promptHint(root, id string) string {
	paths := taskPromptFiles(root, id)
	if len(paths) == 0 {
		return ""
	}
	shown := paths
	tail := ""
	if len(shown) > 3 {
		shown, tail = shown[:3], fmt.Sprintf(" и ещё %d", len(paths)-3)
	}
	return fmt.Sprintf("подсказка: задача правила промпты (%s%s), а такая правка меняет поведение агентов: проверить её стендом по скиллу prompt-test и вложить прогон в раздел «Проверка» файла задачи",
		strings.Join(shown, ", "), tail)
}

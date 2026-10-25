package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/plans"
	"github.com/dronrider/devkit/internal/taskform"
	"github.com/dronrider/devkit/internal/trace"
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

// checkGate держит рубеж перевода в Check: этапы шаблона строки, у которых в
// gate стоит check и сошлось условие (LLD DK-972, решение 7). У встроенного
// шаблона это сценарий с перебором обходов приёмки, обкатка агентского вида и
// слитый код; перечень читается из файла, и добавленный организацией этап
// ворота спрашивают наравне со встроенными. Отказ называет каждый этап без
// следа и пометку, которой его гасят.
func checkGate(root string, row *Row) error {
	_, err := gatePass(root, row, trace.GateCheck, "move")
	return err
}

// closeGate держит рубеж закрытия: этапы с gate = close, у встроенного
// шаблона те же сценарий и обкатка, что у перевода в Check (DK-685), плюс
// проверка чужими руками со сверкой выката. Слитость ветки закрытие не
// спрашивает, её держит сам выход в Check.
func closeGate(root string, row *Row) error {
	_, err := gatePass(root, row, trace.GateClose, "закрытие")
	return err
}

// stageVerdict это ответ одного этапа шаблона: след на месте либо причина, по
// которой ворота считают этап непройденным. Список с вердиктами печатает
// сухой прогон.
type stageVerdict struct {
	st   plans.Stage
	have bool
	why  string
}

// gatePass спрашивает след каждого этапа шаблона строки с названной точкой
// ворот. Пометка «- Исключение: <title>» этап гасит (решение 7), этап со
// словом «слово» закрыт на слове агента. Ошибка перечисляет непройденные
// одним списком: чинить их лучше за один проход.
func gatePass(root string, row *Row, gate, again string) ([]stageVerdict, error) {
	tpl, ok := templateOf(root, row)
	if !ok {
		// Шаблона у строки нет: перечень этапов читается из файла, и
		// спрашивать нечего. Набор, которого не видно вовсе, линт прощает
		// молча тем же порядком (lintPlanNames).
		return nil, nil
	}
	c := gateCtx(root, row, taskDocOf(root, row.ID), gate, again)
	whence := "Check"
	if gate == trace.GateClose {
		whence = "закрытию"
	}
	var out []stageVerdict
	var lacking []string
	for _, st := range gateStages(tpl, gate, row, c) {
		v := stageVerdict{st: st, have: true}
		if st.Trace != "" {
			verdict, why := trace.Check(st.Trace, c)
			v.have, v.why = verdict != trace.Lack, why
		}
		out = append(out, v)
		if !v.have {
			lacking = append(lacking, fmt.Sprintf("этап «%s» без следа (шаблон %s): %s; снимается пометкой «- Исключение: %s (причина)»", st.Title, tpl.Name, v.why, st.Title))
		}
	}
	if len(lacking) > 0 {
		return out, fmt.Errorf("не готово к %s: %s", whence, strings.Join(lacking, "; "))
	}
	return out, nil
}

// gateStages отбирает этапы шаблона под точку ворот: gate в списке, условие
// сошлось (LLD DK-972, решение 8). Маска путей считается по диффу ветки, и
// там, где диффа уже нет (код слит), этап остаётся: незнание его не снимает,
// гасят его след либо пометка-исключение.
func gateStages(tpl *plans.Template, gate string, row *Row, c trace.Ctx) []plans.Stage {
	kind := acceptOf(row.Title)
	var out []plans.Stage
	for _, st := range tpl.Stages {
		if !amongStr(st.Gate, gate) {
			continue
		}
		if len(st.Types) > 0 && !amongStr(st.Types, row.Type) {
			continue
		}
		if len(st.Accept) > 0 && !amongStr(st.Accept, kind) {
			continue
		}
		if len(st.Paths) > 0 {
			if touched, known := trace.PathsTouched(c.Root, c.Main, c.Branch, st.Paths); known && !touched {
				continue
			}
		}
		if st.Trace != "" && taskform.Exception(c.Doc, st.Title) {
			continue
		}
		out = append(out, st)
	}
	return out
}

// gateCtx собирает контекст следов для ворот: тот же, что у плана на done
// (planTraceCtx в agentctl), но от строки доски, с её файлом и ссылкой.
func gateCtx(root string, row *Row, doc, gate, again string) trace.Ctx {
	c := trace.Ctx{
		Root:     root,
		LogRoot:  root,
		ID:       row.ID,
		Doc:      doc,
		Gate:     gate,
		Again:    again,
		Link:     row.Link,
		TaskType: row.Type,
		Accept:   acceptOf(row.Title),
	}
	c.Main = trace.GitMainBranch(root)
	c.Branch, _ = trace.UnmergedTaskBranch(root, row.ID)
	return c
}

// taskDocOf читает текст файла задачи, отсутствие файла пустой строкой.
func taskDocOf(root, id string) string {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return ""
	}
	return string(data)
}

// amongStr отвечает, есть ли значение в списке.
func amongStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// cmdMoveDry прогоняет ворота перевода, не трогая доску: строка остаётся на
// месте, а вызывающий узнаёт, пустит его move или откажет. Нужно это выкату
// поезда (DK-781): ship переводит состав уже после деплоя, и отказ ворот на
// той стороне заставал точку выката сдвинутой. Ворота одни и те же, потому что
// зовётся тот же checkGate, а не его копия: разъехавшись, копия пропускала бы
// в поезд ровно те строки, на которых move потом отказывает. Замок worktree
// (boardGuard) тут не спрашивается: сухой прогон ничего не пишет.
func cmdMoveDry(root, id, target string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	target = normalizeStatus(target)
	if _, ok := b.Sects[target]; !ok {
		return "", fmt.Errorf("неизвестный статус %q, жду backlog / in-progress / check / blocked", target)
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	if err := needTaskFile(root, id); err != nil {
		return "", err
	}
	if target == SectCheck {
		stages, err := gatePass(root, row, trace.GateCheck, "move")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: ворота перевода в %s пройдены, строка не двигалась%s", id, target, stageReport(stages)), nil
	}
	return fmt.Sprintf("%s: ворота перевода в %s пройдены, строка не двигалась", id, target), nil
}

// stageReport печатает этапы со следами, по которым ворота решили, что они
// пройдены: сухой прогон отвечает и отказом, и полным списком (DK-1144).
func stageReport(stages []stageVerdict) string {
	if len(stages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(stages))
	for _, v := range stages {
		verdict := v.why
		if verdict == "" {
			verdict = "след на месте"
		}
		parts = append(parts, fmt.Sprintf("%s (%s): %s", v.st.Title, v.st.Trace, verdict))
	}
	return "\nэтапы: " + strings.Join(parts, "; ")
}

// acceptGate проверяет, что у не агентского вида в файле задачи стоит раздел
// «Приёмка» с ключом барьера из шести и строкой перебора на каждый обход этого
// барьера. Судить убедительность причины обхода машина не берётся, это работа
// ревьювера; здесь только счёт по закрытому списку.
func acceptGate(root, id, kind, again string) error {
	rel := "docs/tasks/" + id + ".md"
	text, found, ok := acceptanceSection(root, id)
	if !ok {
		return fmt.Errorf("%s: вид приёмки %s, а файла %s нет: вид с барьером требует раздел «Приёмка» в файле задачи (LLD DK-292, решение 3)", id, kind, rel)
	}
	if !found {
		return fmt.Errorf("%s: вид приёмки %s, а раздела «Приёмка» в %s нет: назвать барьер и перебрать обходы (LLD DK-292, решение 1) и повторить %s", id, kind, rel, again)
	}
	barrier, bypasses := trace.Acceptance(text)
	if barrier == "" {
		return fmt.Errorf("%s: в разделе «Приёмка» нет строки «- барьер «<ключ>»:», а вид %s её требует", id, kind)
	}
	want, known := trace.BarrierWanted[barrier]
	if !known {
		return fmt.Errorf("%s: барьер «%s» в разделе «Приёмка» не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", id, barrier)
	}
	if bypasses != want {
		return fmt.Errorf("%s: у барьера «%s» обходов %d, а перебор в «Приёмка» имеет строк %d (нужно столько же, по строке на обход с исходом)", id, barrier, want, bypasses)
	}
	return nil
}

// closeVerifyGate сверяет прогонявшего сценарий с исполнителем последнего
// этапа работы над кодом (DK-642). Проверка переехала в internal/trace
// (VerifyRunner, DK-1144), отсюда её зовёт отбор closable; ворота close
// спрашивают её этапом шаблона с gate = close.
func closeVerifyGate(root, id string) error {
	return trace.VerifyRunner(root, id)
}

// uncheckedDeploy отвечает, стоит ли в файле задачи настоящий непроверенный
// выкат: слияние с кодом за пределами docs/ без действующей на него отметки
// smoke. Проверка переехала в internal/trace (DeployUnchecked, DK-1144),
// отсюда её зовёт отбор closable; ворота close спрашивают её этапом шаблона
// с gate = close.
func uncheckedDeploy(root string, data []byte) bool {
	return trace.DeployUnchecked(root, string(data))
}

// lintUnmerged ловит обратную сторону того же рубежа: строка уже в Check, а
// ветка с её ID живёт с неслитыми коммитами. Так выглядит перевод в обход
// ворот (правку доски руками отменить некому) и брошенное на полпути слияние.
func lintUnmerged(root string, b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Sects[SectCheck].Rows {
		if br, ahead := trace.UnmergedTaskBranch(root, r.ID); br != "" {
			finds = append(finds, fmt.Sprintf("%s:%d: %s в Check, а ветка %s не слита (коммитов впереди main: %d), слить: shipctl merge %s",
				bp, r.LineIdx+1, r.ID, br, ahead, r.ID))
		}
	}
	return finds
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
		if p == "" || seen[p] || !obey.AgentFile(p) {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// promptHint печатает строку про стенд, когда задача правила промпты. Отказа
// тут нет: к переводу в Check слияние уже прошло, и отказывать поздно, а
// ворота стенда стоят на самом слиянии (LLD DK-805, решение 2). Подсказка
// называет команду прогона: след в файл задачи кладёт сам стенд, вкладывать
// таблицу руками не надо. Стоит ли в плане строки стенд со своими воротами,
// решает шаблон: этапа со следом стенда нет, и повода для подсказки тоже нет.
func promptHint(root, id string, tpl *plans.Template) string {
	paths := taskPromptFiles(root, id)
	if len(paths) == 0 {
		return ""
	}
	if tpl != nil && !templateHasStand(tpl) {
		return ""
	}
	shown := paths
	tail := ""
	if len(shown) > 3 {
		shown, tail = shown[:3], fmt.Sprintf(" и ещё %d", len(paths)-3)
	}
	return fmt.Sprintf("подсказка: задача правила промпты (%s%s), а такая правка меняет поведение агентов: прогнать стенд по скиллу prompt-test, `obeycheck --task %s --for %s -k 5`, отметку в файл задачи он положит сам",
		strings.Join(shown, ", "), tail, id, strings.Join(shown, ","))
}

// templateHasStand отвечает, спрашивает ли шаблон след стенда на слиянии.
func templateHasStand(tpl *plans.Template) bool {
	for _, st := range tpl.Stages {
		if st.Trace == trace.WordStand && amongStr(st.Gate, trace.GateMerge) {
			return true
		}
	}
	return false
}

// closeForkGate не даёт закрыть задачу, пока в перечне развилок её файла стоит
// открытая человеческая (LLD DK-552, решение 2). Развилку заводят до старта, но
// всплывает она и посреди работы, а закрытая задача уезжает в архив, где
// перечень уже не правят: незакрытый вопрос уехал бы вместе с ней и вернулся
// переделкой. Отбор развилок и слова отказа общие с воротами `shipctl start`,
// лежат в internal/taskform.
//
// Файла задачи нет, значит и перечня нет, и ворота молчат: так живут строки без
// файла и все задачи, закрытые до появления раздела.
func closeForkGate(root, id string) error {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return nil
	}
	held := taskform.HoldingForks(string(data))
	if len(held) == 0 {
		return nil
	}
	return fmt.Errorf("%s", taskform.ForkGateNote(id, "закрытие задачи", held))
}

// closeDebtGate не даёт закрыть задачу с открытым хвостом долга (DK-1356).
// Отметку открывает сам перевод в Check, а закрывает разбор по скиллу
// board-debt: у ручной сдачи его зовёт исполнитель, у конвейерной шаг лежит в
// заказе проверяющего. Сессия разбора может оборваться, и без ворот хвост
// уезжал бы в архив молча, возвращаясь переделкой. Файла задачи нет значит и
// отметки нет: строки без файла ворота не держат.
func closeDebtGate(root, id string) error {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return nil
	}
	if !taskform.DebtHeld(string(data)) {
		return nil
	}
	return fmt.Errorf("%s", taskform.DebtGateNote(id))
}

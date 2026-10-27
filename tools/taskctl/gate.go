package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/plans"
	"github.com/dronrider/devkit/internal/stage"
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
// одним списком: чинить их лучше за один проход. Внешние чтения (git, файлы
// следов) идут вне замка доски (DK-1469): вложенный taskctl из чужого хода
// иначе мёртво блокируется на том же замке.
func gatePass(root string, row *Row, gate, again string) ([]stageVerdict, error) {
	tpl, ok := templateOf(root, row)
	if !ok {
		// Шаблона у строки нет: перечень этапов читается из файла, и
		// спрашивать нечего. Набор, которого не видно вовсе, линт прощает
		// молча тем же порядком (lintPlanNames).
		return nil, nil
	}
	whence := "Check"
	if gate == trace.GateClose {
		whence = "закрытию"
	}
	var c trace.Ctx
	var out []stageVerdict
	var lacking []string
	if err := cliReadOutside(root, func() error {
		c = gateCtx(root, row, taskDocOf(root, row.ID), gate, again)
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
		return nil
	}); err != nil {
		return nil, err
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
	if target == SectInProgress {
		if err := taskReady(root, row); err != nil {
			return "", err
		}
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

// closeVerifyGate сверяет прогон сценария с участниками текущего круга
// (DK-1360): независимость несёт отдельная сессия со свежим контекстом, и
// совпадение сессии прогона с любой сессией, ведшей разработку или доработку
// круга, закрытия не даёт. Модель разрешена та же: имена моделей независимость
// не доказывают и в сверке не участвуют, имя хранится отдельно для заказов и
// отчётов. Задача без записи прогона проходит молча: на этом держатся
// автозакрытие тиком (DK-516) и задачи, закрытые до записи прогонявшего.
// Источники те же два: раздел «Ход работы» файла задачи, куда этапы уносит
// смена статуса (сессии едут хвостом «сессия <ID>»), и незакрытый пакет, где
// сессия стоит полем. Прогон без сессии и круг с участником без сессии дают
// отказ с причиной: фиктивных ID ворота не создают. Ручной close зовёт её
// этапом шаблона [verify] со следом verify (DK-1144), а отбор closable
// проверяет то же самое напрямую теми же источниками.
func closeVerifyGate(root, id string) error {
	lines, pending, err := checkedStageSources(root, id)
	if err != nil {
		return err
	}
	_, _, err = stage.VerifyIndependence(id, lines, pending)
	return err
}

func checkedStageSources(root, id string) ([]string, []stage.Stage, error) {
	var lines []string
	var pending []stage.Stage
	if err := cliReadOutside(root, func() error {
		lines, pending = trace.StageSources(root, id)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return lines, pending, nil
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

// closeCutGate держит на закрытии LLD её нарезку (DK-489): решения документа
// живут работой доски, а закрытие без нарезки уносило их в архив текстом, про
// который потом некому вспомнить. У DK-459 так чуть не повисли восемь правок
// режима POC, у DK-1325 семь этапов внедрения остались разделом решения в
// день закрытия. Кандидаты читаются из контрактного раздела «Нарезка»
// документа в docs/lld: нарезанный кандидат несёт ID заведённой строки в
// голове, а нарезка целью помечена строкой «- цель <ID>». Наличие пометки
// ворота и проверяют, качество нарезки остаётся за скиллом goal-cut и
// ревью. Документ не нашёлся значит ворот молчит: у строки без документа
// спрашивать нечего, и отсутствие самого дизайна держат ворота заведения.
// Отложенную решением дизайна нарезку гасит пометка-исключение, как у
// DK-1318 с пробой DK-1413.
func closeCutGate(root string, row *Row, b *Board, arch *Archive) error {
	if !isLLD(row.Type) {
		return nil
	}
	if data, err := os.ReadFile(taskFilePath(root, row.ID)); err == nil {
		if taskform.Exception(string(data), taskform.GateCut) {
			return nil
		}
	}
	doc := lldDocOf(root, row)
	if doc == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, doc))
	if err != nil {
		return nil
	}
	rows, goal, found := taskform.CutSection(string(data))
	switch {
	case !found:
		return fmt.Errorf("%s: принятая LLD закрывается с нарезкой, а раздела «Нарезка» в %s нет: выписать кандидатов списком, как в разделе Задачи цели у цели, и нарезать их строками или целью (RULES.board.md, «Трекинг задач» п. 12), а отложенную решением дизайна нарезку загасить пометкой «- Исключение: %s (причина)» в docs/tasks/%s.md и повторить закрытие",
			row.ID, doc, taskform.GateCut, row.ID)
	case goal != "":
		if g := b.find(goal); g != nil {
			if !goalRow(g.Title) {
				return fmt.Errorf("%s: раздел «Нарезка» в %s помечен целью %s, а %s целью не является: кандидаты едут списком в файл цели, пометить настоящую цель или нарезать кандидатов строками", row.ID, doc, goal, goal)
			}
			return nil
		}
		if arch.has(goal) {
			return nil
		}
		return fmt.Errorf("%s: раздел «Нарезка» в %s помечен целью %s, которой нет ни на доске, ни в архиве: завести цель и перенести кандидатов в раздел Задачи цели её файла, либо нарезать строки", row.ID, doc, goal)
	case len(rows) == 0:
		return fmt.Errorf("%s: в разделе «Нарезка» %s нет ни одного кандидата: выписать кандидатов из решений списком, как в разделе Задачи цели у цели, либо, если работы из решений не следует, загасить ворот пометкой «- Исключение: %s (причина)» в docs/tasks/%s.md",
			row.ID, doc, taskform.GateCut, row.ID)
	}
	var pending, dead []string
	for _, r := range rows {
		if r.TaskID == "" {
			pending = append(pending, r.Text)
			continue
		}
		if b.find(r.TaskID) == nil && !arch.has(r.TaskID) {
			dead = append(dead, r.TaskID)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("%s: в разделе «Нарезка» %s не заведено строкой или целью кандидатов: %d из %d: нарезать их (одиночные строки задачами, общий бюджет и порядок дают цель со списком по скиллу goal-cut) и вписать выданный ID в голову строки кандидата, повторить закрытие; отложенную нарезку загасить пометкой «- Исключение: %s (причина)» в docs/tasks/%s.md",
			row.ID, doc, len(pending), len(rows), taskform.GateCut, row.ID)
	}
	if len(dead) > 0 {
		return fmt.Errorf("%s: раздел «Нарезка» %s ссылается на %s, которых нет ни на доске, ни в архиве: исправить ID на живую строку цели или строки нарезки", row.ID, doc, strings.Join(dead, ", "))
	}
	return nil
}

// lldDocOf ищет документ дизайна LLD-строки: сначала по ссылкам файла задачи
// (файл ссылается на дизайн, а не пересказывает его, п. 3 «Трекинг задач»),
// затем по ссылке самой строки, как она живёт у LLD в колонке доски. Своим
// считается документ docs/lld/<ID>-... , чьё имя начинается с ID строки:
// чужой разбор из первых абзацев (корп-контур у строк DK-074) документом
// строки не является. Пустая строка значит документа нет и воротам читать
// нечего.
func lldDocOf(root string, row *Row) string {
	rel := fmt.Sprintf("tasks/%s.md", row.ID)
	for _, ref := range lldRefs(root, rel) {
		if ownDesignName(ref, row.ID) {
			return ref
		}
	}
	if p := linkedDoc(root, row.Link, rel); strings.HasPrefix(p, "docs/lld/") && ownDesignName(p, row.ID) {
		return p
	}
	return ""
}

// ownDesignName отличает свой документ дизайна docs/lld/<ID>-<slug>.md от
// чужого разбора с другим ID в имени: без слага документ носит имя <ID>.md.
func ownDesignName(doc, id string) bool {
	base := filepath.Base(doc)
	return base == id+".md" || strings.HasPrefix(base, id+"-")
}

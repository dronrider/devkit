package trace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/rehearsal"
	"github.com/dronrider/devkit/internal/reviewnote"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// Ctx это всё, что проверяющим нужно знать о точке, где спрашивают след.
// Пустой ID значит сессию без задачи: у неё нет файла задачи, и следы, которые
// в нём живут, закрыты словом (решение 6).
type Ctx struct {
	// Root это дерево, где лежат код и файл задачи.
	Root string
	// ID это задача доски, пустая строка у сессии без задачи.
	ID string
	// Doc это текст файла задачи, пустой без задачи.
	Doc string
	// Main и Branch это ветки, между которыми считается дифф.
	Main, Branch string
	// DocsBranch говорит, что дифф трогает одни тексты.
	DocsBranch bool
	// TaskType это тип строки доски, его спрашивает regcheck.
	TaskType string
	// Accept это вид приёмки строки (agent, mixed, user), пусто без задачи.
	Accept string
	// Again это слово повтора в отказах, у слияния «слияние», у плана «этап».
	Again string
	// LogRoot это дерево, где лежит журнал .devkit/log: при работе через
	// worktree прогоны оседают в дереве задачи.
	LogRoot string
}

// Verdict это итог вопроса о следе этапа.
type Verdict int

const (
	// Have значит, след на месте: этап закрыт по существу.
	Have Verdict = iota
	// Lack значит, следа нет: этап планом не закрыт, причина в строке рядом.
	Lack
	// ByWord значит, след спрашивать нечем: этап закрывается на слове агента,
	// причина в строке рядом. Сюда попадают слово «слово» и следы, живущие в
	// файле задачи, когда задачи у сессии нет.
	ByWord
)

// Check спрашивает след слова у его проверяющего. У каждого слова словаря
// проверяющий один, и ворота с планом зовут одного и того же: разойдись они,
// ворота пускали бы строку, чей этап плана стоит открытым. Текст отказа
// называет причину и следующий шаг, у Have строка пустая.
func Check(word string, c Ctx) (Verdict, string) {
	if word == WordWord {
		return ByWord, "на слове агента"
	}
	if TaskBound(word) && c.ID == "" {
		return ByWord, "след недоступен без задачи, закрыто словом"
	}
	var err error
	switch {
	case strings.HasPrefix(word, StagePrefix):
		err = stageTrace(StageKind(word), c)
	case word == WordTests:
		err = Tests(c.Root, c.Main, c.Branch, c.DocsBranch, c.Doc)
	case word == WordRegcheck:
		err = Regcheck(c.Root, c.LogRoot, c.Main, c.Branch, c.TaskType, c.Doc)
	case word == WordStand:
		err = Stand(c.Root, c.Main, c.Branch, c.ID, c.Doc)
	case word == WordScenario:
		err = Scenario(c)
	case word == WordRehearsal:
		err = rehearsal.Fresh(c.Root, c.ID, c.Doc, c.Again)
	case word == WordReview:
		if c.ID == "" {
			err = reviewNoteTrace(c)
		} else {
			err = ReviewLevel(c.ID, c.Doc)
		}
	case word == WordMerged:
		err = Merged(c)
	case word == WordVerify:
		err = Verify(c)
	default:
		// Незнакомое слово до диспетчера не доходит: plans проверяет его
		// trace.Known при чтении набора. Сюда ворота не попадают вовсе.
		return Lack, "слово " + word + " не из словаря следов"
	}
	if err != nil {
		return Lack, err.Error()
	}
	return Have, ""
}

// Scenario проверяет готовность сценария проверки: раздел «Сценарий проверки»
// в файле задачи, а у вида приёмки mixed и user ещё и перебор обходов в разделе
// «Приёмка» (LLD DK-292, решение 1). Агентскому виду перебор не нужен: его
// барьер один, машина сама гоняет шаги. Перевод в Check это передача на
// проверку, и без сценария задача едет туда без способа себя проверить.
// Погашение живёт пометкой «- Исключение: сценарий (причина)».
func Scenario(c Ctx) error {
	rel := "docs/tasks/" + c.ID + ".md"
	if !HasHeading(c.Doc, "Сценарий проверки") {
		if !taskform.Exception(c.Doc, taskform.GateScenario) {
			return fmt.Errorf("в %s нет раздела «Сценарий проверки», а без него задачу не выпускают: описать шаги и ожидаемый итог (RULES.board.md, «Трекинг задач» п. 6); если сценарий неприменим (бескодовая правка, проверяется вместе с другой задачей), загасить этап пометкой «- Исключение: сценарий (причина)»", rel)
		}
	}
	if c.Accept != "mixed" && c.Accept != "user" {
		return nil
	}
	if taskform.Exception(c.Doc, taskform.GateScenario) {
		return nil
	}
	text, found := sectionOf(c.Doc, "## Приёмка")
	if !found {
		return fmt.Errorf("вид приёмки %s, а раздела «Приёмка» в %s нет: назвать барьер и перебрать обходы (LLD DK-292, решение 1)", c.Accept, rel)
	}
	barrier, bypasses := Acceptance(text)
	if barrier == "" {
		return fmt.Errorf("в разделе «Приёмка» %s нет строки «- барьер «<ключ>»:», а вид приёмки %s её требует", rel, c.Accept)
	}
	want, known := BarrierWanted[barrier]
	if !known {
		return fmt.Errorf("барьер «%s» в разделе «Приёмка» %s не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", barrier, rel)
	}
	if bypasses != want {
		return fmt.Errorf("у барьера «%s» обходов %d, а перебор в «Приёмка» %s имеет строк %d (нужно столько же, по строке на обход с исходом)", barrier, want, rel, bypasses)
	}
	return nil
}

// Merged проверяет, что код задачи доехал до main: в файле задачи стоит
// слияние в «Выкате» либо ветки с ID задачи неслитой больше нет. Оба признака
// спрашиваются вместе, потому что каждый говорит о своём моменте: запись в
// «Выкате» ставит слияние, а живая ветка с коммитами впереди main значит, что
// код лежит только у автора.
func Merged(c Ctx) error {
	if len(taskform.MergedShas(c.Doc)) > 0 {
		return nil
	}
	if br, ahead := UnmergedTaskBranch(c.Root, c.ID); br != "" {
		return fmt.Errorf("%s: ветка %s не слита, впереди main её коммитов %d: сначала «shipctl merge %s», он и переведёт строку", c.ID, br, ahead, c.ID)
	}
	// У сессии без задачи веткой работы считается ветка текущего дерева:
	// несделанное слияние видно по ней так же, как по ветке задачи.
	if c.ID == "" && c.Branch != "" {
		if n := gitAhead(c.Root, c.Main, c.Branch); n > 0 {
			return fmt.Errorf("ветка %s не слита, впереди main её коммитов %d: сначала слияние ветки, потом закрывать этап", c.Branch, n)
		}
	}
	return nil
}

// reviewNoteTrace проверяет след ревью git-заметкой на HEAD: у сессии без
// задачи нет файла с разделом «Ревью», и заметка остаётся единственным местом
// следа. Судит та же пара, что и ворота пуша (reviewTraceGate в shipctl):
// следом считается заметка со строкой уровня либо круга, а не любой текст.
func reviewNoteTrace(c Ctx) error {
	noted, err := reviewnote.Has(c.Root, "HEAD")
	if err != nil {
		return fmt.Errorf("след ревью не прочитан: %v", err)
	}
	if noted {
		return nil
	}
	return fmt.Errorf(`нет следа ревью: заметки ревью на HEAD нет.
Ревью идёт по скиллу review свежим контекстом, а не тем, который писал правку.
След ставится командой taskctl review level <ярлык> <0-3> "причина" (заметка git на HEAD, ref %s),
  ревью без замечаний это taskctl review clean <ярлык> "пояснение".`,
		reviewnote.Ref)
}

// CurrentBranch называет ветку текущего дерева. Пустая строка вне git, при
// отдельном HEAD и на самой main: у беззадачной сессии ветка работы это
// текущая ветка, и main ничем не отличается от отсутствия ветки.
func CurrentBranch(root string) string {
	br, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	br = strings.TrimSpace(br)
	if br == "" || br == "HEAD" || br == gitMainBranch(root) {
		return ""
	}
	return br
}

// gitAhead считает коммиты ветки впереди main, ошибка git считается нулём:
// спрашивающим хватит общего случая, а падать из-за неё дороже, чем пропустить.
func gitAhead(root, main, branch string) int {
	out, err := git(root, "rev-list", "--count", main+".."+branch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// Verify проверяет прогон сценария чужими руками (DK-642): запись этапа
// «проверка», непустой раздел «Проверка» с реальным выводом, а у выкаченной
// задачи ещё и отметка smoke на последний выкат (LLD DK-400, решение 7:
// непроверенный выкат это выкат без smoke). Прогон сессией из круга разработки
// не зачитывается: в цикле DK-138 три дефекта проехали ревью, и поймал их
// только прогон чужими руками. Независимость с DK-1360 несёт отдельная
// сессия, а не другая модель: сверку ведёт internal/stage, ворота отдают ей
// оба источника следа.
func Verify(c Ctx) error {
	rel := "docs/tasks/" + c.ID + ".md"
	text, found := sectionOf(c.Doc, taskform.Verification)
	if !found || strings.TrimSpace(text) == "" {
		return fmt.Errorf("%s: раздел «Проверка» в %s пуст или его нет: выкатить код и вложить туда реальный вывод прогона, пустое закрытие агентской задачи запрещено", c.ID, rel)
	}
	if len(taskform.MergedShas(c.Doc)) > 0 && !taskform.SmokeCovers(c.Doc) {
		return fmt.Errorf("%s: в «Выкате» есть слияние, а отметки smoke на последний выкат нет: сценарий после выката не прогнан; прогнать его на выкаченном коде, вывод вложить в раздел «Проверка», выкат «shipctl smoke %s»", c.ID, c.ID)
	}
	lines, pending := StageSources(c.Root, c.ID)
	if _, found, err := stage.VerifyIndependence(c.ID, lines, pending); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("%s: в «Ходе работы» нет записи прогона: сценарий прогоняет отдельная сессия со свежим контекстом (RULES.board.core.md), а её след «agentctl stage %s проверка --by <модель>»", c.ID, c.ID)
	}
	return nil
}

// stageTrace проверяет след семейства stage:<вид>: строка этапа с этим видом в
// «Ходе работы» файла задачи либо в незакрытом пакете ~/.devkit/runs. Запись
// кладёт хук спавна субагента или утилита, которая начинает работу, сам агент
// её не пишет.
func stageTrace(kind string, c Ctx) error {
	lines, pending := StageSources(c.Root, c.ID)
	for _, s := range pending {
		if s.Kind == kind {
			return nil
		}
	}
	for _, ln := range lines {
		if s, _, ok := stage.ParseLine(ln); ok && s.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("%s: нет записи этапа «%s» ни в «Ходе работы», ни в пакете ~/.devkit/runs: запись кладёт хук спавна субагента либо утилита, начинающая работу; если этап шёл тем же контекстом без субагента, у шаблона для него стоит слово «слово»", c.ID, kind)
}

// Acceptance разбирает текст раздела «Приёмка» на ключ барьера и число строк
// перебора обходов. Ключ пуст, если строки барьера нет. Перебор считается по
// подсписку второго уровня после строки барьера: ровно те строки, что
// относятся к обходам названного барьера.
func Acceptance(text string) (barrier string, bypasses int) {
	lines := strings.Split(text, "\n")
	sawBarrier := false
	for _, ln := range lines {
		if m := acceptBarrierLineRe.FindStringSubmatch(ln); m != nil {
			barrier = m[1]
			sawBarrier = true
			continue
		}
		if sawBarrier && acceptBypassRe.MatchString(ln) {
			bypasses++
		}
	}
	return barrier, bypasses
}

// acceptBarrierLineRe узнаёт строку барьера, acceptBypassRe строку обхода.
var (
	acceptBarrierLineRe = regexp.MustCompile(`^- барьер «([^»]+)»`)
	acceptBypassRe      = regexp.MustCompile(`^  - `)
)

// BarrierWanted это закрытый список шести барьеров (LLD DK-292, решение 1) и
// число обходов у каждого. Столько строк перебора обязано стоять в разделе
// «Приёмка» против названного барьера: у каждого обхода свой исход, и машинный
// счёт идёт по числу строк.
var BarrierWanted = map[string]int{
	"глаза":         3,
	"доступ":        4,
	"необратимость": 4,
	"секрет":        3,
	"согласие":      1,
	"событие":       3,
}

// HasHeading говорит, есть ли в файле задачи заголовок любого уровня с
// заданным текстом. Заголовок внутри ограждённого блока не считается: это
// чужой вывод, а не раздел нашего файла.
func HasHeading(doc, name string) bool {
	lines := strings.Split(doc, "\n")
	mask, _ := stage.FenceMask(lines)
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, "#") && strings.Contains(ln, name) {
			return true
		}
	}
	return false
}

// HasScenario отвечает, готов ли у задачи сценарий проверки в её файле.
// Второе значение ложное, когда файла нет. Признак раздела это заголовок
// «## Сценарий проверки» вне ограждённых блоков, незакрытая ограда уводит в
// блок весь остаток файла: заголовок после неё это чужой вывод, а не раздел.
func HasScenario(root, id string) (has, hasFile bool) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return false, false
	}
	lines := strings.Split(string(data), "\n")
	mask, _ := stage.FenceMask(lines)
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, "## Сценарий проверки") {
			return true, true
		}
	}
	return false, true
}

// sectionOf читает тело раздела текста файла задачи (без строки заголовка),
// раздел кончается следующим заголовком уровня «## ». Заголовок внутри
// ограждённого блока это чужой вывод, а не раздел этого файла. Ограждение, не
// закрытое до конца файла, ограждением не считается: иначе раздел с забытым
// ``` тянулся бы за все заголовки до последней строки (DK-820).
func sectionOf(doc, heading string) (text string, found bool) {
	lines := strings.Split(doc, "\n")
	gap := unclosedFence(lines)
	fence := ""
	in := false
	var out []string
	for i, ln := range lines {
		if m := fenceRe.FindStringSubmatch(ln); m != nil && i+1 != gap {
			switch {
			case fence == "":
				fence = m[1]
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
				fence = ""
			}
			if in {
				out = append(out, ln)
			}
			continue
		}
		if fence != "" {
			if in {
				out = append(out, ln)
			}
			continue
		}
		if !in {
			if strings.HasPrefix(ln, heading) {
				in = true
			}
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			break
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n"), in
}

// fenceRe узнаёт строку ограждения блока кода, unclosedFence отдаёт номер
// строки (с единицы) ограждения, которое не закрылось до конца файла, и ноль,
// когда все блоки закрыты.
var fenceRe = regexp.MustCompile("^(`{3,}|~{3,})")

func unclosedFence(lines []string) int {
	fence, at := "", 0
	for i, ln := range lines {
		m := fenceRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		switch {
		case fence == "":
			fence, at = m[1], i+1
		case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
			fence, at = "", 0
		}
	}
	return at
}

// taskFilePath называет файл живой задачи по её ID.
func taskFilePath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// StageSources собирает оба источника записей об этапах: строки раздела «Ход
// работы» и незакрытый пакет из ~/.devkit/runs. Отсутствие файла или раздела
// это пустой список, спрашивающие по нему молчат.
func StageSources(root, id string) ([]string, []stage.Stage) {
	var lines []string
	if data, err := os.ReadFile(taskFilePath(root, id)); err == nil {
		if text, found := sectionOf(string(data), "## Ход работы"); found {
			lines = strings.Split(text, "\n")
		}
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		return lines, nil
	}
	return lines, rec.Stages
}

// UnmergedTaskBranch ищет живую ветку задачи, у которой есть коммиты, не
// доехавшие до main. Такая ветка значит, что код задачи лежит только у автора,
// каким бы ни был статус строки. Вне git и в корп-контуре, где доска отдельно
// от кода, ветки не найдётся, и рубеж молча пропускает: врать про несделанное
// слияние хуже, чем его не заметить.
func UnmergedTaskBranch(root, id string) (branch string, ahead int) {
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

// branchOfTask узнаёт ветку задачи: ветка называется по ID строчными, с
// хвостом-слагом или без (`dk-005`, `dk-005-worktree`).
func branchOfTask(branch, id string) bool {
	b, low := strings.ToLower(branch), strings.ToLower(id)
	return b == low || strings.HasPrefix(b, low+"-")
}

// gitMainBranch называет ветку main или master, пустую строку вне git и там,
// где ни той, ни другой нет.
func gitMainBranch(root string) string {
	for _, b := range []string{"main", "master"} {
		if _, err := exec.Command("git", "-C", root, "rev-parse", "--verify", b).Output(); err == nil {
			return b
		}
	}
	return ""
}

// GitMainBranch называет ветку main или master, пустую строку вне git и там,
// где ни той, ни другой нет. Спрашивают её план и ворота, чтобы дифф ветки
// задачи считать одинаково.
func GitMainBranch(root string) string { return gitMainBranch(root) }

// PathsTouched отвечает, задел ли дифф main...branch хоть одну маску этапа
// (LLD DK-972, решение 8). Второе значение говорит, известно ли это вовсе:
// диффа может не быть (нет ветки, нет git), и незнание не снимает этап, его
// закрывает либо след, либо правка, маску задевшая. Пустой список масок это
// этап без условия по путям, он нужен всегда.
func PathsTouched(root, main, branch string, masks []string) (touched, known bool) {
	if len(masks) == 0 {
		return false, false
	}
	if main == "" || branch == "" {
		return false, false
	}
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", main+"..."+branch).Output()
	if err != nil {
		return false, false
	}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		for _, m := range masks {
			if pathMatch(m, f) {
				return true, true
			}
		}
	}
	return false, true
}

// pathMatch сверяет путь с маской условия этапа: `*` не переходит через косую
// черту, `**` покрывает каталоги целиком, включая ноль сегментов. Форма та же,
// что у масок шаблона плана, и отдельного синтаксиса тут не заводится.
func pathMatch(pattern, name string) bool {
	return segMatch(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func segMatch(pat, seg []string) bool {
	if len(pat) == 0 {
		return len(seg) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(seg); i++ {
			if segMatch(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	}
	if len(seg) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], seg[0])
	if err != nil || !ok {
		return false
	}
	return segMatch(pat[1:], seg[1:])
}

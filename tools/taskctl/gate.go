package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/checkrun"
	"github.com/dronrider/devkit/internal/merged"
	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/rehearsal"
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

// checkGate держит рубежи перевода в Check: готовый сценарий проверки,
// обкатка, слитый код и перебор обходов приёмки. Нарушения приходят от слабой
// модели, которая прозу правил не держит, а через move ходит обязательно,
// поэтому отказ называет причину и следующий шаг.
func checkGate(root string, row *Row) error {
	id := row.ID
	if err := scenarioGate(root, row, "move", true); err != nil {
		return err
	}
	// Обкатка спрашивается у агентского вида: там каждый шаг выполним с машины
	// агента, и прогнать сценарий до Check ничего не стоит. У mixed и user
	// часть шагов держит человек, требовать от них машинную отметку не о чем.
	if acceptOf(row.Title) == acceptAgent {
		if err := rehearsalGate(root, id); err != nil {
			return err
		}
	}
	if br, ahead := trace.UnmergedTaskBranch(root, id); br != "" {
		return fmt.Errorf("%s: ветка %s не слита, впереди main её коммитов %d, а Check значит, что код уже доехал до пользователя: сначала «shipctl merge %s», он и переведёт строку", id, br, ahead, id)
	}
	// Не агентский вид требует раздел «Приёмка» с перебором обходов
	// (LLD DK-292, решение 4): машина считает строки, ревьювер судит причины.
	if kind := acceptOf(row.Title); kind != acceptAgent {
		if err := acceptGate(root, id, kind, "move"); err != nil {
			return err
		}
	}
	return nil
}

// closeGate держит на закрытии строки те же рубежи, что checkGate держит на
// переводе в Check, кроме слитого кода: закрывают и минуя Check, прямо из In
// progress, и так в архив уезжали задачи без сценария, без обкатки и без
// перебора обходов приёмки (DK-685). Проверки те же самые, а не их копия:
// разойдись они, закрытие снова стало бы дверью без замка. Слитость ветки
// здесь не спрашивается, её держит сам выход в Check.
func closeGate(root string, row *Row) error {
	if err := scenarioGate(root, row, "закрытие", false); err != nil {
		return err
	}
	kind := acceptOf(row.Title)
	if kind == acceptAgent {
		// Агентский вид требует непустого раздела «Проверка» в файле задачи
		// (LLD DK-292, решение 4): пустое закрытие агентской задачи запрещено,
		// и это машинный рубеж против фиктивного сценария из повтора тестов
		// ветки.
		if err := closeAgentGate(root, row.ID); err != nil {
			return err
		}
		return closeRehearsalGate(root, row.ID)
	}
	return acceptGate(root, row.ID, kind, "закрытие")
}

// scenarioGate требует готовый сценарий проверки: без него задача уходит с
// доски без способа себя проверить. Сценарий, живущий в другом документе
// (LLD), ворот проходит по ссылке строки. Ключ needFile говорит, спрашивать ли
// сам файл задачи: перевод в Check его требует, а закрытие нет, потому что
// строки старого запаса заводились без файла и закрыть их иначе было бы
// нечем.
func scenarioGate(root string, row *Row, again string, needFile bool) error {
	id := row.ID
	if scenarioElsewhere(row.Link, id) {
		return nil
	}
	has, hasFile := trace.HasScenario(root, id)
	rel := "docs/tasks/" + id + ".md"
	switch {
	case !hasFile && !needFile:
		return nil
	case !hasFile:
		return fmt.Errorf("%s: без готового сценария проверки задачу не выпускают, а файла %s нет: завести «taskctl file %s», описать шаги и ожидаемый итог разделом «Сценарий проверки» и повторить %s", id, rel, id, again)
	case !has:
		return fmt.Errorf("%s: в %s нет раздела «Сценарий проверки», без него задачу не выпускают: описать шаги и ожидаемый итог (RULES.board.md, «Трекинг задач» п. 6) и повторить %s", id, rel, again)
	}
	return nil
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
		if err := checkGate(root, row); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s: ворота перевода в %s пройдены, строка не двигалась", id, target), nil
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
// отказ с причиной: фиктивных ID ворота не создают.
func closeVerifyGate(root, id string) error {
	lines, pending := trace.StageSources(root, id)
	_, _, err := stage.VerifyIndependence(id, lines, pending)
	return err
}

// closeSmokeGate не даёт закрыть строку с непроверенным выкатом (DK-1363):
// отбор closable сверяет отметку smoke с последним слиянием, а ручной close
// той сверки не делал, и выкаченная строка уезжала в архив без прогона
// сценария, сломанный прод оставался за закрытой строкой. Сверка та же, что у
// closable и у очереди выката (LLD DK-400, решение 7: непроверенный выкат это
// выкат без smoke), а не её копия. Записи слияния нет, значит выката нет
// (LLD, дока, разбор), и ворота молчат. Слияние одной документации прод не
// меняет и выкатом не считается тем же признаком, каким его не видит очередь
// (DK-1424): строка с проверенной агентской докой закрывается без отметки.
// Круг доработки дописывает в «Выкат» новую строку слияния, и отметка
// прошлого круга новый выкат не прикрывает: вернувшаяся из Check строка
// закрывается только после новой отметки.
func closeSmokeGate(root, id string) error {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return nil
	}
	if !uncheckedDeploy(root, data) {
		return nil
	}
	return fmt.Errorf("%s: в «Выкате» есть слияние, а отметки smoke на последний выкат нет: сценарий после выката не прогнан; прогнать его на выкаченном коде, вывод вложить в раздел «Проверка», прогонявшего отметить «agentctl stage %s проверка --by %s», выкат «shipctl smoke %s» и повторить закрытие",
		id, id, repeatModel(root, id), id)
}

// uncheckedDeploy отвечает, стоит ли в файле задачи настоящий непроверенный
// выкат: слияние с кодом за пределами docs/ без действующей на него отметки
// smoke. Читают его ворота close и отбор closable, чтобы ответ у ручного и
// автоматического закрытия не разошёлся. Не удалось разобрать состав
// коммитов значит выкат: ошибиться в сторону требования smoke дешевле, чем
// в сторону пропуска.
func uncheckedDeploy(root string, data []byte) bool {
	if taskform.SmokeCovers(string(data)) {
		return false
	}
	shas := taskform.MergedShas(string(data))
	if len(shas) == 0 {
		return false
	}
	docs, err := merged.CommitsDocsOnly(root, shas)
	return err != nil || !docs
}

// repeatModel это модель для повторного прогона в тексте отказа (DK-1116).
// Считает её `agentctl check`, та же дорога, какой ярус проверяющего берёт
// автономный выкат: ярус вердиктом роли ревью, модель раскладкой подписки.
// Совпадение с моделью разработки выбор не режет (DK-1360): независимость
// несёт сессия, а имя остаётся подсказкой в отказе. Позвать некем или отказ,
// значит отказ close остаётся с прочерком вместо модели: подсказка тут не
// главное, а разваливаться из-за неё воротам незачем.
func repeatModel(root, id string) string {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return checkModelUnknown
	}
	cmd := exec.Command(bin, "check", id)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return checkModelUnknown
	}
	if m := checkrun.ParseModel(string(out)); m != "" {
		return m
	}
	return checkModelUnknown
}

// checkModelUnknown стоит в отказе там, где модель назвать нечем: вместо
// готового имени остаётся прежняя рамка под руку человека.
const checkModelUnknown = "<модель>"

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

// promptHint печатает одну строку про стенд, когда задача правила промпты.
// Отказа тут нет: к переводу в Check слияние уже прошло, и отказывать поздно, а
// ворота стенда стоят на самом слиянии (LLD DK-805, решение 2). Подсказка
// называет команду прогона: след в файл задачи кладёт сам стенд, вкладывать
// таблицу руками не надо.
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
	return fmt.Sprintf("подсказка: задача правила промпты (%s%s), а такая правка меняет поведение агентов: прогнать стенд по скиллу prompt-test, `obeycheck --task %s --for %s -k 5`, отметку в файл задачи он положит сам",
		strings.Join(shown, ", "), tail, id, strings.Join(shown, ","))
}

// rehearsalGate держит третий рубеж перевода в Check: сценарий обкатан до
// того, как задача уехала на проверку. Прогон на месте, в прогретом чекауте с
// живым HOME и PATH, зеленеет там, где на чужой машине красно, и находится это
// уже после слияния (RULES.board.md, «Трекинг задач» п. 6; повод из цикла
// DK-138). Сам разбор отметки лежит в internal/rehearsal: тот же ворот стоит
// на закрытии строки и на слиянии ветки, и своей копии у каждого выхода нет
// намеренно. Файла задачи нет значит и отметке негде стоять: рубеж выше уже
// отказал за сценарий, второй раз ронять не о чем.
func rehearsalGate(root, id string) error {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return nil
	}
	return rehearsal.Fresh(root, id, string(data), "move")
}

// closeRehearsalGate спрашивает обкатку на втором выходе задачи, закрытии
// строки. Ворот move check закрытие обходит стороной: строку закрывают и прямо
// из In progress, и архив принимал задачу, чей сценарий никто не гонял
// (DK-685). Свежесть отметки тут не сверяется: закрытие идёт после Check, HEAD
// к тому часу уехал чужими слияниями, и сверка коммита требовала бы обкатки
// кода, которого задача не писала.
func closeRehearsalGate(root, id string) error {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return nil
	}
	return rehearsal.Mark(id, string(data), "закрытие")
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

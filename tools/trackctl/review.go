package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// cmdReview заводит на боковой доске зеркальную строку ревью чужого тикета:
// вход в сценарий «проведи ревью AB-123» из LLD DK-756 (решение 1). В отличие
// от take, тикет команда не трогает вовсе, ни статус, ни исполнителя, ни
// эстимейт: тест проверяет это по журналу вызовов заглушки. Повторный вызов
// находит стоящую строку и дубля не заводит.
func cmdReview(root, arg, mrFlag string) (string, error) {
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	key := ticketKey(tr.bind, arg)
	existing, err := findReviewRow(root, tr.bind, key)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return fmt.Sprintf("строка ревью уже стоит: %s (%s)", existing.ID, existing.Title), nil
	}
	t, err := tr.adapter.fetch(key)
	if err != nil {
		return "", err
	}
	mrLink, mrLine, err := resolveMR(tr, key, mrFlag)
	if err != nil {
		return "", err
	}
	cost, costLine := reviewCost(tr.contour, t.Estimate)
	rank, rankLine := reviewRank(t)
	title := reviewTitle(key, t.Title)

	id, addOut, err := addReviewRow(root, reviewRowParams{title: title, rank: rank, cost: cost})
	if err != nil {
		return "", err
	}
	if err := setRowLink(root, id, ticketLinkCell(t)); err != nil {
		return "", err
	}
	if err := fillReviewFile(root, id, t, mrLink); err != nil {
		return "", err
	}
	lines := []string{addOut, costLine, rankLine, mrLine, fmt.Sprintf("файл задачи: docs/tasks/%s.md", id)}
	return strings.Join(lines, "\n"), nil
}

// reviewTitle это заголовок зеркальной строки ревью: пометка сценария живёт
// прямо в заголовке, чтобы отличить её от обычной строки того же тикета
// (строки take не носят приставку «Ревью»).
func reviewTitle(key, ticketTitle string) string {
	return fmt.Sprintf("%s %s", reviewMarker(key), ticketTitle)
}

// reviewMarker это пометка сценария без текста тикета: по ней findReviewRow
// узнаёт стоящую строку, а не только по ссылке (ссылка на тикет есть и у
// строки take, пометка нужна ровно затем, чтобы их не путать).
func reviewMarker(key string) string {
	return fmt.Sprintf("Ревью %s:", key)
}

// findReviewRow ищет на доске стоящую строку ревью тикета. Признака нужно два
// разом: ссылка ведёт на тикет, и заголовок несёт пометку сценария.
func findReviewRow(root string, b *binding, key string) (*boardRow, error) {
	rows, err := loadBoardRows(root, b.Key)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if isReviewRowFor(rows[i], key) {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// isReviewRowFor говорит, несёт ли строка пометку сценария ревью ровно этого
// тикета: ссылка на тикет есть и у строки take, пометка в заголовке отличает
// их друг от друга.
func isReviewRowFor(row boardRow, key string) bool {
	return strings.EqualFold(row.Ticket, key) && strings.HasPrefix(row.Title, reviewMarker(key))
}

// isReviewRow это isReviewRowFor по собственной ссылке строки: sync читает
// им, ссылка на тикет своя, чужого ключа сравнивать не с чем.
func isReviewRow(row boardRow) bool {
	return row.Ticket != "" && isReviewRowFor(row, row.Ticket)
}

// branchPrefix отдаёт литеральный префикс ветки тикета из шаблона привязки:
// часть шаблона до «{slug}» с подставленным ключом. У шаблона «{key}-{slug}»
// это «ABC-12-», хвост-слаг отрезан целиком, потому что trackctl его не
// знает, ветку заводит shipctl при взятии в работу.
func branchPrefix(b *binding, key string) string {
	tpl := b.Branch
	if tpl == "" {
		tpl = "{key}-{slug}"
	}
	if idx := strings.Index(tpl, "{slug}"); idx >= 0 {
		tpl = tpl[:idx]
	}
	return strings.ReplaceAll(tpl, "{key}", key)
}

// resolveMR ищет MR тикета. Флаг --mr перекрывает поиск целиком: ссылку
// назвал человек, спрашивать адаптер незачем. Без флага в дело идёт
// необязательная операция mergeRequests: адаптер её не умеет (сегодня это
// Jira, GitLab-клиента в сборке нет), значит строка заводится без MR с
// честной просьбой дать ссылку руками. Адаптер операцию умеет, но нашёл не
// ровно одну ссылку, это отказ всей команды: строка не заводится, пока
// человек не разрешит неоднозначность флагом.
func resolveMR(tr *tracker, key, mrFlag string) (link, note string, err error) {
	if mrFlag != "" {
		return mrFlag, "MR: " + mrFlag + " (дано флагом --mr)", nil
	}
	lister, ok := tr.adapter.(mrLister)
	if !ok {
		return "", fmt.Sprintf("MR: адаптер %s не ищет MR по ветке, ссылку дайте флагом --mr <url>", tr.contour.Adapter), nil
	}
	prefix := branchPrefix(tr.bind, key)
	urls, err := lister.mergeRequests(prefix)
	if err != nil {
		return "", "", err
	}
	switch len(urls) {
	case 0:
		return "", "", fmt.Errorf("MR по ветке %s* не нашёл, дайте ссылку флагом --mr <url>", prefix)
	case 1:
		return urls[0], "MR: " + urls[0], nil
	default:
		return "", "", fmt.Errorf("MR по ветке %s* нашёлся не один: %s; дайте ссылку флагом --mr <url>", prefix, strings.Join(urls, ", "))
	}
}

// reviewCost переводит оценку тикета в цену строки обратной таблицей
// контура (та же cost_s/cost_m/cost_l, что take переводит вперёд). Точного
// совпадения нет, значит переводить нечем: цена остаётся «-», как у любой
// незаведённой оценки, и об этом честно говорит вторая строка вывода.
func reviewCost(c *contour, estimate string) (cost, line string) {
	estimate = strings.TrimSpace(estimate)
	if estimate == "" {
		return "-", "цена: у тикета нет оценки, цену дайте `taskctl set --cost`"
	}
	switch estimate {
	case c.CostS:
		return "S", fmt.Sprintf("цена: оценка тикета %s -> S", estimate)
	case c.CostM:
		return "M", fmt.Sprintf("цена: оценка тикета %s -> M", estimate)
	case c.CostL:
		return "L", fmt.Sprintf("цена: оценка тикета %s -> L", estimate)
	}
	return "-", fmt.Sprintf("цена: оценка тикета %s не совпала ни с одной ступенью контура (S=%s, M=%s, L=%s), цену дайте `taskctl set --cost`", estimate, c.CostS, c.CostM, c.CostL)
}

// reviewRank считает разбивку ранга для новой строки ревью. У контракта
// адаптера нет читаемого числового поля приоритета (rank это только запись,
// см. mrLister рядом), поэтому серьёзность, ценность, неопределённость и
// рычаг это фиксированная заготовка ревью-работы (RANKING.md), а не значение
// из тикета: сомнение «делать ли вообще» тут не про сам ранг, грумминг
// поправит его так же, как у любой заведённой руками строки. Из тикета в
// разбивку идёт единственное читаемое поле, поправка на баг по типу.
func reviewRank(t ticket) (rank, line string) {
	sev, val, unc, lev := 0, 0, 1, 2
	bug := 0
	if strings.Contains(strings.ToLower(t.Type), "bug") {
		bug = 5
	}
	total := sev + val + unc + bug + lev
	rank = fmt.Sprintf("%d+%d+%d+%d+%d", sev, val, unc, bug, lev)
	line = fmt.Sprintf("ранг: заготовка ревью-работы %s = %d (у трекера нет читаемого приоритета, поправьте `taskctl set --rank` после грумминга)", rank, total)
	return rank, line
}

// ticketLinkCell собирает ячейку ссылки зеркальной строки, тем же форматом
// markdown-ссылки, что и у строки take.
func ticketLinkCell(t ticket) string {
	return fmt.Sprintf("[%s](%s)", t.Key, t.URL)
}

// reviewRowParams это вход addReviewRow, отдельным типом ради читаемого
// вызова без длинного списка позиционных строк.
type reviewRowParams struct {
	title, rank, cost string
}

// addReviewRow заводит строку доски командой её утилиты: доску правит
// taskctl, а не trackctl напрямую, тем же порядком, что moveRow у sync.
// Подменяется в тестах, чтобы прогон не требовал taskctl в PATH. Вид
// приёмки mixed с барьером «доступ» строке ставится сразу: публикация
// замечаний ревью в живой трекер требует токена, которого у прогона без
// человека может не быть, а «доступ» из шести барьеров (ACCEPTANCE.md)
// это ровно про такую невозможность прогнать сценарий без обвязки машины.
var addReviewRow = func(root string, p reviewRowParams) (id, msg string, err error) {
	cmd := exec.Command("taskctl", "-C", root, "add",
		"--title", p.title,
		"--type", "task",
		"--rank", p.rank,
		"--cost", p.cost,
		"--accept", "mixed",
		"--barrier", "доступ",
	)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", "", fmt.Errorf("taskctl add: %v: %s", err, text)
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("taskctl add не назвал ID заведённой строки: %q", text)
	}
	return fields[0], text, nil
}

// setRowLink переводит ячейку ссылки свежей строки с болванки файла задачи
// (её кладёт add) на тикет: строка ревью ссылается на тикет тем же порядком,
// что зеркальная строка take (LLD DK-074, «Граница доски и тикета»).
// Подменяется в тестах по тем же причинам, что addReviewRow.
var setRowLink = func(root, id, link string) error {
	cmd := exec.Command("taskctl", "-C", root, "set", id, "--link", link)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskctl set %s --link: %v: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// reviewBarrierLine это строка «- барьер «доступ»:», которую add кладёт в
// файл задачи пустой (без причины после двоеточия). fillReviewFile дописывает
// причину следом.
const reviewBarrierLine = "- барьер «доступ»:"

// reviewBarrierReason это причина барьера «доступ» у строки ревью: публикация
// замечаний в живой трекер требует токена, а без него сценарий проверки
// автоматика до конца не доводит.
const reviewBarrierReason = "публикация в живой трекер требует токена"

// fillReviewFile дописывает в свежий файл задачи то, чего болванка add не
// знает: постановку тикета в «Что происходит» (решение 1 LLD DK-756 велит
// копировать её сюда, в отличие от обычной зеркальной строки, у которой файл
// держит только ссылку) и ссылку на MR, если она нашлась. Причину барьера
// «доступ» add оставляет пустой, эту строку дописывает fillReviewFile тоже.
func fillReviewFile(root, id string, t ticket, mrLink string) error {
	abs := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	body := string(data)

	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		desc = fmt.Sprintf("тикет %s не дал описания, постановку взять из него самому.", t.Key)
	}
	lines := []string{desc}
	if mrLink != "" {
		lines = append(lines, "", "MR: "+mrLink)
	}
	body = taskform.InsertIntoSection(body, taskform.Situation, lines...)

	body = fillBarrierReason(body, reviewBarrierReason)

	return os.WriteFile(abs, []byte(body), 0o644)
}

// fillBarrierReason дописывает причину барьера на пустую строку, которую
// кладёт `taskctl add --barrier`. Строки не будет, если add сменит формат
// болванки, тогда правка молча не встанет, и это находка, а не тихий обход:
// review в этом случае не отказывает (файл создан и ссылку несёт), но
// причина барьера ждёт человека.
func fillBarrierReason(body, reason string) string {
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		if strings.TrimRight(ln, " ") == reviewBarrierLine {
			lines[i] = reviewBarrierLine + " " + reason
			break
		}
	}
	return strings.Join(lines, "\n")
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Отметка последнего pull статусов. Файл пишет sync (DK-084), status читает
// его время: без отметки видно, что доска с тикетами ни разу не сверялась.
const syncMarkPath = ".devkit/tracker.sync"

// tracker это собранная обвязка одной команды: привязка проекта, контур
// компании и адаптер трекера.
type tracker struct {
	bind    *binding
	contour *contour
	adapter adapter
}

func openTracker(root string) (*tracker, error) {
	b, err := loadBinding(root)
	if err != nil {
		return nil, err
	}
	c, err := loadContour(b.Contour)
	if err != nil {
		if names := contourNames(); len(names) > 0 {
			return nil, fmt.Errorf("%v; на машине есть контуры: %s", err, strings.Join(names, ", "))
		}
		return nil, err
	}
	a, err := newAdapter(c)
	if err != nil {
		return nil, err
	}
	return &tracker{bind: b, contour: c, adapter: a}, nil
}

// ticketKey принимает и полный ключ, и один номер: в проекте с привязкой
// префикс всё равно один.
func ticketKey(b *binding, arg string) string {
	arg = strings.TrimSpace(arg)
	if strings.Contains(arg, "-") {
		return arg
	}
	return b.Key + "-" + arg
}

// cmdIssue показывает тикет глазами адаптера и то, как контур укладывает его
// статус в секции доски.
func cmdIssue(root, arg string) (string, error) {
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	t, err := tr.adapter.fetch(ticketKey(tr.bind, arg))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t%s\n", t.Key, t.Title)
	fmt.Fprintf(&b, "статус:\t%s", t.Status)
	sect, ok := tr.contour.sectionOf(t.Status)
	switch {
	case !ok:
		fmt.Fprint(&b, "\n")
	case sect == sectDone:
		fmt.Fprint(&b, " (конечный)\n")
	default:
		fmt.Fprintf(&b, " (секция доски: %s)\n", boardNames[sect])
	}
	fmt.Fprintf(&b, "тип:\t%s\n", orDash(t.Type))
	fmt.Fprintf(&b, "оценка:\t%s\n", orDash(t.Estimate))
	switch {
	case !ok:
		fmt.Fprintln(&b, tr.contour.unknownStatus(t))
	case sect == sectDone:
		fmt.Fprintln(&b, doneHint(t))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cmdTake берёт тикет в работу: переход в целевой статус секции In progress и
// assign на пользователя контура. Одно без другого не считается: тикет In
// progress без исполнителя в чужом процессе заметен сразу.
func cmdTake(root, arg string) (string, error) {
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	key := ticketKey(tr.bind, arg)
	t, err := tr.adapter.fetch(key)
	if err != nil {
		return "", err
	}
	sect, ok := tr.contour.sectionOf(t.Status)
	if !ok {
		return tr.contour.unknownStatus(t), nil
	}
	if sect == sectDone {
		return doneHint(t), nil
	}
	var lines []string
	if sect == sectInProgress {
		lines = append(lines, fmt.Sprintf("тикет %s уже в «%s», перехода не делаю", t.Key, t.Status))
	} else {
		target, err := tr.contour.targetStatus(sectInProgress)
		if err != nil {
			return "", err
		}
		if err := tr.adapter.transition(t.Key, target, tr.contour.fieldsFor(sectInProgress)); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("тикет %s: «%s» -> «%s»", t.Key, t.Status, target))
	}
	if err := tr.adapter.assign(t.Key, tr.contour.User); err != nil {
		return "", err
	}
	lines = append(lines, fmt.Sprintf("исполнитель: %s", tr.contour.User))
	row := mirrorRow(root, tr.bind, t.Key)
	est, err := pushEstimate(tr, t, row)
	if err != nil {
		return "", err
	}
	lines = append(lines, est)
	if rk, err := pushRank(tr, t, row); err != nil {
		return "", err
	} else if rk != "" {
		lines = append(lines, rk)
	}
	lines = append(lines, fmt.Sprintf("ветка по шаблону привязки: %s, хвост даёт «shipctl start --slug»", tr.bind.branchName(t.Key, "<хвост>")))
	return strings.Join(lines, "\n"), nil
}

// mirrorRow ищет зеркальную строку тикета на доске. Доски может не быть вовсе
// (нечитаемая доска это не повод валить переход тикета), поэтому отказ чтения
// здесь тихий: команда скажет про него строкой вывода.
func mirrorRow(root string, b *binding, key string) *boardRow {
	rows, err := loadBoardRows(root, b.Key)
	if err != nil {
		return nil
	}
	return rowForTicket(rows, key)
}

// pushEstimate ставит оценку из цены зеркальной строки: цена переводится в
// часы таблицей контура (cost_s, cost_m, cost_l). Это затраты полного цикла,
// работа агента вместе с сопровождением инженером. Оценку, уже стоящую в
// тикете, команда не трогает: её мог поправить человек, и затирать чужую цифру
// автоматика права не имеет.
func pushEstimate(tr *tracker, t ticket, row *boardRow) (string, error) {
	if row == nil {
		return fmt.Sprintf("оценка: зеркальной строки с ключом %s на доске нет, считать эстимейт не из чего", t.Key), nil
	}
	value := tr.contour.estimateFor(row.Cost)
	if value == "" {
		return fmt.Sprintf("оценка: цена строки %s это «%s», контур %s её в оценку не переводит", row.ID, orDash(row.Cost), tr.contour.Name), nil
	}
	if t.Estimate != "" {
		return fmt.Sprintf("оценка: в тикете уже стоит «%s», не трогаю (цена %s даёт %s)", t.Estimate, row.Cost, value), nil
	}
	if err := tr.adapter.estimate(t.Key, value); err != nil {
		return "", err
	}
	return fmt.Sprintf("оценка: цена %s строки %s -> %s", row.Cost, row.ID, value), nil
}

// pushRank отправляет сумму ранга в числовое поле приоритета, если контур его
// назвал. Ось необязательная: контур поля не назвал, значит и говорить не о
// чем, а названное поле при адаптере без операции rank это молчаливое
// бездействие, и о нём команда говорит вслух.
func pushRank(tr *tracker, t ticket, row *boardRow) (string, error) {
	if tr.contour.RankField == "" {
		return "", nil
	}
	r, ok := tr.adapter.(ranker)
	if !ok {
		return fmt.Sprintf("приоритет: контур назвал поле %s, а адаптер %s операции rank не умеет", tr.contour.RankField, tr.contour.Adapter), nil
	}
	if row == nil || row.Rank == 0 {
		return fmt.Sprintf("приоритет: разбивки ранга на доске нет, поле %s не трогаю", tr.contour.RankField), nil
	}
	if err := r.rank(t.Key, tr.contour.RankField, row.Rank); err != nil {
		return "", err
	}
	return fmt.Sprintf("приоритет: R=%d строки %s -> %s", row.Rank, row.ID, tr.contour.RankField), nil
}

// cmdStatus показывает контур, адаптер и свежесть последнего pull. Отсутствие
// необязательной операции называется вслух: молчание тут неотличимо от
// работающей оси.
func cmdStatus(root string) (string, error) {
	var b strings.Builder
	bind, err := loadBinding(root)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "привязка:\t%s\n", bind.Path)
	fmt.Fprintf(&b, "проект:\t%s, ветки %s\n", bind.Key, bind.branchName("{key}", "{slug}"))
	c, err := loadContour(bind.Contour)
	if err != nil {
		if names := contourNames(); len(names) > 0 {
			return "", fmt.Errorf("%v; на машине есть контуры: %s", err, strings.Join(names, ", "))
		}
		return "", err
	}
	fmt.Fprintf(&b, "контур:\t%s (%s)\n", c.Name, c.Path)
	fmt.Fprintf(&b, "адрес:\t%s, пользователь %s\n", orDash(c.BaseURL), c.User)
	if _, err := c.token(); err != nil {
		fmt.Fprintf(&b, "токен:\t%v\n", err)
	} else {
		fmt.Fprintf(&b, "токен:\tна месте\n")
	}
	var missSect []string
	for _, sect := range statusSections {
		if len(c.Status[sect]) == 0 {
			missSect = append(missSect, sect)
		}
	}
	if len(missSect) > 0 {
		fmt.Fprintf(&b, "статусы:\tне расписаны секции %s\n", strings.Join(missSect, ", "))
	} else {
		fmt.Fprintf(&b, "статусы:\tрасписаны все секции\n")
	}
	a, err := newAdapter(c)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "адаптер:\t%s\n", c.Adapter)
	miss := missingOptional(a)
	if len(miss) == 0 {
		fmt.Fprintf(&b, "операции:\tвсе на месте\n")
	} else {
		for _, m := range miss {
			fmt.Fprintf(&b, "нет операции:\t%s\n", m)
		}
	}
	fmt.Fprintf(&b, "sync:\t%s\n", syncAge(root))
	return strings.TrimRight(b.String(), "\n"), nil
}

// syncAge говорит, когда доска в последний раз догоняла тикеты. Отметки нет
// значит sync не гонялся ни разу, и это надо видеть, а не додумывать.
func syncAge(root string) string {
	fi, err := os.Stat(filepath.Join(root, syncMarkPath))
	if err != nil {
		return "не гонялся ни разу"
	}
	d := time.Since(fi.ModTime())
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад (%s)", int(d.Hours()/24), fi.ModTime().Format("2006-01-02"))
	}
}

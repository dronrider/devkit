package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// cmdDraft кладёт тикет на доску черновиком: вход задачи корп-контура в работу
// начинается тут, а не с take. До DK-1262 строки из тикета делала одна команда
// review, и тикет исполнения уезжал в код мимо груминга, pick и всех ворот
// доски. Теперь порядок один на оба контура: тикет -> черновик -> груминг ->
// take, и груминг оформляет черновик обычной строкой со ссылкой на тикет в
// ячейке, то есть той же зеркальной, которую читают issue, take и sync.
//
// Тикет команда не трогает вовсе, как и review: ни статуса, ни исполнителя.
func cmdDraft(root, arg, prio string) (string, error) {
	if strings.TrimSpace(prio) == "" {
		return "", fmt.Errorf("уровень разбора обязателен: draft <KEY> --prio high|mid|low (его ставит тот, кто записывает, дальше правит taskctl draft prio)")
	}
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	key := ticketKey(tr.bind, arg)
	if row := mirrorRow(root, tr.bind, key); row != nil {
		return fmt.Sprintf("строка тикета %s уже стоит на доске: %s (%s); черновик не нужен, в работу берёт «trackctl take %s»",
			key, row.ID, row.Title, key), nil
	}
	t, err := tr.adapter.fetch(key)
	if err != nil {
		return "", err
	}
	title, titleNote := draftTitle(t.Title)
	id, out, err := addDraft(root, draftText(t, title), prio)
	if err != nil {
		return "", err
	}
	lines := []string{out}
	if titleNote != "" {
		lines = append(lines, titleNote)
	}
	lines = append(lines,
		fmt.Sprintf("оформит черновик груминг (скилл board-groom), ссылка на тикет встаёт в ячейку строки: taskctl set %s --link \"%s\"", id, ticketLinkCell(t)),
		fmt.Sprintf("до строки на доске take откажет: зеркальной строки тикета %s нет", key))
	return strings.Join(lines, "\n"), nil
}

// draftTitle готовит первую строку черновика из заголовка тикета. Потолок
// держит taskctl (форма черновика в TASKFORM.md), и заголовок длиннее потолка
// отбился бы отказом, поэтому длинный summary режется по слову, а целиком он
// всё равно остаётся в теле черновика.
func draftTitle(summary string) (title, note string) {
	summary = strings.TrimSpace(strings.ReplaceAll(summary, "\n", " "))
	if summary == "" {
		return "", ""
	}
	r := []rune(summary)
	if len(r) <= taskform.DraftTitleLimit {
		return summary, ""
	}
	cut := string(r[:taskform.DraftTitleLimit])
	if i := strings.LastIndex(cut, " "); i > taskform.DraftTitleLimit/2 {
		cut = cut[:i]
	}
	cut = strings.TrimRight(cut, " ,.;:")
	return cut, fmt.Sprintf("заголовок тикета длиннее %d символов, в первой строке он обрезан, целиком лежит в теле черновика", taskform.DraftTitleLimit)
}

// draftText собирает текст черновика для stdin taskctl: первая строка это
// заголовок, дальше тело. Подразделы черновика (SCQA) команда не раскладывает
// сама: текст без разметки taskctl целиком кладёт в «Ситуацию», а осложнение,
// вопрос и гипотезу пишет грумер, у которого перед глазами и тикет, и код.
// Постановка тикета копируется в черновик и дальше в файл задачи (решение
// DK-1262, приписка к «Границе доски и тикета» LLD DK-074): без неё грумить
// нечего, а наружу файл задачи не уходит.
func draftText(t ticket, title string) string {
	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		desc = fmt.Sprintf("тикет %s не дал описания, постановку прочитать в нём самом.", t.Key)
	}
	if title == "" {
		title = "тикет " + t.Key + " без заголовка"
	}
	body := []string{title, "", desc, "", "Тикет: " + ticketLinkCell(t)}
	if full := strings.TrimSpace(t.Title); full != "" && full != title {
		body = append(body, "", "Заголовок тикета: "+full)
	}
	return strings.Join(body, "\n") + "\n"
}

// addDraft заводит черновик командой taskctl: номер черновика это сквозная
// нумерация доски, и выдаёт его та же утилита, что правит доску (тем же
// порядком, что addReviewRow и moveRow). Подменяется в тестах, чтобы прогон
// не требовал taskctl в PATH.
var addDraft = func(root, text, prio string) (id, msg string, err error) {
	cmd := exec.Command("taskctl", "-C", root, "draft", "--prio", prio)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err != nil {
		return "", "", fmt.Errorf("taskctl draft: %v: %s", err, got)
	}
	fields := strings.Fields(got)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("taskctl draft не назвал ID черновика: %q", got)
	}
	return strings.TrimSuffix(fields[0], ":"), got, nil
}

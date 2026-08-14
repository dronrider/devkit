package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Переписка с агентом через состояние цели (LLD DK-112, «Переписка»): POST
// кладёт сообщение человека в раздел «Входящие» файла цели, GET отдаёт
// лежащие там строки. Писать в идущий процесс дашборд не берётся: виток
// читает файл цели целиком первым шагом, так что сообщение уйдёт следующему
// витку, а пока строка лежит, клиент честно показывает «ждёт витка».

// inboxHeader это раздел «Входящие» файла цели. Дашборд строки туда кладёт и
// показывает, а убирает их виток записью витка (скилл goal-loop).
const inboxHeader = "## Входящие"

// journalHeader нужен месту вставки: раздела «Входящие» goal-start не
// заводит, и первый POST ставит его перед «Журналом», рядом с которым
// сообщения и читаются.
const journalHeader = "## Журнал"

// inboxFrom это подпись дашборда в строке «Входящих»: по ней строка
// узнаётся своей, и по ней же из неё достаётся текст сообщения. Строку,
// написанную в раздел руками, дашборд чужой не считает и ключом повтора не
// берёт.
const inboxFrom = ", из дашборда: "

// inboxOwnRe узнаёт свою строку по началу: дата, время и подпись стоят там,
// где их ставит POST. Искать подпись где попало нельзя, иначе рукописная
// строка, где эта фраза попалась в тексте (хоть в разговоре про саму
// переписку), сойдёт за свою и отобьёт следующую настоящую отправку как
// повтор.
var inboxOwnRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}` + inboxFrom)

// msgBodyLimit ограничивает тело POST message: сообщение это одна строка
// списка витку, а не вложение; тело сверх предела отбивается своими словами.
const msgBodyLimit = 16 << 10

// addInboxLine дописывает строку в конец «Входящих»; без раздела заводит его
// перед «Журналом», а без «Журнала» в конце файла.
func addInboxLine(doc, line string) string {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimRight(ln, " ") == inboxHeader {
			start = i
			break
		}
	}
	if start < 0 {
		return insertInboxSection(lines, line)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	// Вставка после последней содержательной строки раздела: хвостовые пустые
	// строки остаются отбивкой перед следующим заголовком.
	ins := end
	for ins > start+1 && strings.TrimSpace(lines[ins-1]) == "" {
		ins--
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:ins]...)
	if ins == start+1 {
		out = append(out, "")
	}
	out = append(out, line)
	if ins == len(lines) {
		out = append(out, "")
	}
	out = append(out, lines[ins:]...)
	return strings.Join(out, "\n")
}

func insertInboxSection(lines []string, line string) string {
	block := []string{inboxHeader, "", line, ""}
	for i, ln := range lines {
		if strings.TrimRight(ln, " ") == journalHeader {
			out := make([]string, 0, len(lines)+len(block))
			out = append(out, lines[:i]...)
			out = append(out, block...)
			out = append(out, lines[i:]...)
			return strings.Join(out, "\n")
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	out := append(append([]string{}, lines...), "")
	out = append(out, block...)
	return strings.Join(out, "\n")
}

// inboxLines отдаёт строки «Входящих» без маркера списка: это сообщения,
// которых виток ещё не подхватил.
func inboxLines(doc string) []string {
	out := []string{}
	in := false
	for _, ln := range strings.Split(doc, "\n") {
		trimmed := strings.TrimRight(ln, " ")
		if trimmed == inboxHeader {
			in = true
			continue
		}
		if in && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !in {
			continue
		}
		if item, ok := strings.CutPrefix(trimmed, "- "); ok {
			out = append(out, item)
		}
	}
	return out
}

// pendingSame ищет среди неподхваченных строк ту же реплику дашборда. Отсюда
// растёт дедупликация повтора: своего состояния сервер не заводит, память о
// сказанном это сами «Входящие», а подхваченная витком строка из них уходит,
// и тот же текст после подхвата кладётся заново.
func pendingSame(doc, text string) (string, bool) {
	for _, ln := range inboxLines(doc) {
		head := inboxOwnRe.FindString(ln)
		if head == "" {
			continue
		}
		if ln[len(head):] == text {
			return "- " + ln, true
		}
	}
	return "", false
}

// goalFile проверяет, что id это цель проекта, и отдаёт путь её файла вместе
// с относительным путём от корня проекта (для коммита). Путь вычисляет
// goalDocPath тем же разбором ссылки строки доски, что и журнал: жёсткой
// склейки docs/tasks/<ID>.md здесь нет, и путь не может разойтись с
// журналом. Отказы называются словами и пишутся здесь же: строка не на
// доске, строка не цель (заголовок не от «Цель:»), файла цели нет.
func (s *server) goalFile(w http.ResponseWriter, r *http.Request) (found *Project, id, path, rel string, ok bool) {
	found = s.findProject(w, r, "сообщения цели")
	if found == nil {
		return nil, "", "", "", false
	}
	id = r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return nil, "", "", "", false
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil, "", "", "", false
	}
	row, rowOK := findRow(raw, id)
	if !rowOK {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("на доске %s нет строки %s", found.Name, id)})
		return nil, "", "", "", false
	}
	if !isGoalTitle(row.Title) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%s не цель: заголовок не начинается с «Цель:», а сообщение кладётся только в файл цели", id)})
		return nil, "", "", "", false
	}
	doc := s.goalDocPath(found.Path, id)
	if !doc.seen {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("файла цели %s в %s нет: класть сообщение некуда", doc.rel, found.Name)})
		return nil, "", "", "", false
	}
	return found, id, doc.path, doc.rel, true
}

func (s *server) handleGoalMessagePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, path, rel, ok := s.goalFile(w, r)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		// Тело сверх предела это своя причина, а не «жду JSON»: JSON там мог
		// быть нормальный.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("сообщение длиннее предела %d КБ: во «Входящие» кладётся короткая строка витку", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	// Сообщение это одна строка списка: переводы строк схлопываются в
	// пробелы, иначе хвост многострочного текста выпал бы из разбора
	// «Входящих».
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение класть некуда: жду JSON {\"text\": \"...\"}"})
		return
	}
	// Чтение, сверка с лежащим и запись идут под одним замком: пара запросов с
	// одним текстом приходит не только от двойного нажатия, но и от двух
	// вкладок одного чата, где очередь исходящих дожимает сообщение своим
	// циклом в каждой (DK-287). Без замка оба успевают прочитать файл цели до
	// чужой записи, и запись второго ложится поверх первой: строка либо
	// удваивается, либо, при одинаковом тексте, чужое сообщение теряется
	// целиком. Коммит держится тем же замком, параллельные git-коммиты дерутся
	// за индекс, и потому замок свой у каждого репозитория: обе беды живут
	// внутри одного дерева, а общий на дашборд запирал бы отправку во все
	// проекты, пока один ждёт недоступный origin.
	lock := s.inboxLock(found.Path)
	lock.Lock()
	defer lock.Unlock()
	doc, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не прочитался: %v", err)})
		return
	}
	// Второе нажатие «Отправить» второй строки не заводит: на слабой связи
	// ответ теряется, человек жмёт ещё раз, и виток получал бы одно сообщение
	// дважды. Ответ при этом остаётся успешным и называет лежащую строку,
	// клиенту повтор различать незачем.
	if same, dup := pendingSame(string(doc), text); dup {
		s.logf("повтор сообщения для %s в %s: строка уже лежит во «Входящих»", id, found.Name)
		writeJSON(w, http.StatusOK, map[string]string{
			"id": id, "line": same,
			"message": fmt.Sprintf("такое сообщение уже лежит во «Входящих» файла цели %s: второй строки не завожу, виток прочитает одну", id),
		})
		return
	}
	if s.inboxProbe != nil {
		s.inboxProbe(found.Path)
	}
	line := "- " + s.now().Format("2006-01-02 15:04") + inboxFrom + text
	if err := os.WriteFile(path, []byte(addInboxLine(string(doc), line)), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не записался: %v", err)})
		return
	}
	resp := map[string]string{
		"id": id, "line": line,
		"message": fmt.Sprintf("сообщение легло во «Входящие» файла цели %s: его прочитает следующий виток, идущий не увидит", id),
	}
	if note := commitDocs(found.Path,
		fmt.Sprintf("docs(tasks): %s сообщение с дашборда во «Входящие»", id), rel); note != "" {
		resp["note"] = note
		s.logf("сообщение для %s в %s: %s", id, found.Name, note)
	}
	s.logf("сообщение для %s в %s легло во «Входящие»", id, found.Name)
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleGoalMessageGet(w http.ResponseWriter, r *http.Request) {
	_, id, path, _, ok := s.goalFile(w, r)
	if !ok {
		return
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не прочитался: %v", err)})
		return
	}
	pending := inboxLines(string(doc))
	resp := map[string]any{"id": id, "pending": pending}
	// Пустота различима: «витку нечего ждать» это слова, а не пустой список
	// без причины.
	if len(pending) == 0 {
		resp["note"] = fmt.Sprintf("во «Входящих» файла цели %s пусто: непрочитанных сообщений нет", id)
	}
	writeJSON(w, http.StatusOK, resp)
}

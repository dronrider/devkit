package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// Взвод строки и цепочка уровнями с экрана (LLD DK-933, решения 1 и 4). Обе
// ручки тонкие: согласие на самостоятельный старт и рёбра между уровнями
// считает `taskctl`, а дашборд передаёт ему выбор человека и доносит слова
// отказа до карточки. Своей копии правил взвода тут нет ни строчки: открытая
// развилка, грумминговый вердикт и состав незакрытой цели держат строку в
// утилите, и повторять этот счёт на сервере значило бы разъехаться с ним на
// первой правке.

// chainLevelsMax это потолок уровней одной цепочки. Человек собирает её
// глазами по списку доски, и на десятом уровне диалог перестаёт читаться
// раньше, чем утилита перестаёт справляться; потолок стоит тут же, чтобы
// запрос с сотней уровней не уехал в подпроцесс.
const chainLevelsMax = 10

// chainIDsMax это потолок номеров в одном запросе цепочки, считая рёбра
// первого уровня.
const chainIDsMax = 60

// handleTaskArm ставит и снимает взвод строки. Отказ утилиты уезжает на экран
// словами: причина отказа живёт в строке или в файле задачи, и человек, нажав
// переключатель, узнаёт её там же, где нажимал.
func (s *server) handleTaskArm(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON {\"on\": true|false}: ставить взвод или снимать"})
		return
	}
	args := []string{"arm", id}
	what := "взведена с дашборда"
	if !*body.On {
		args = append(args, "--off")
		what = "взвод снят с дашборда"
	}
	out, code, err := s.taskctlWrite(found.Path, args...)
	if err != nil {
		s.logf("взвод %s в %s не прошёл: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"id": id, "armed": *body.On, "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, what),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("взвод %s в %s: %s", id, found.Name, note)
	}
	s.logf("взвод %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// handleChain собирает цепочку уровнями одним вызовом `taskctl chain`. Ответ на
// предпросмотр это план уровнями, тот же текст, что печатает `--dry-run`:
// строки плана разбираются на список, чтобы диалог показал их по одной, а не
// одним абзацем.
func (s *server) handleChain(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "цепочка задач")
	if found == nil {
		return
	}
	var body struct {
		After  []string   `json:"after"`
		Levels [][]string `json:"levels"`
		DryRun bool       `json:"dry_run"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON {\"after\": [\"DK-NNN\"], \"levels\": [[\"DK-NNN\"], ...]}"})
		return
	}
	args, err := chainArgs(body.After, body.Levels, body.DryRun)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Предпросмотр доску не трогает, и памяти ответа он не сбрасывает: гоняется
	// он на каждое движение в диалоге, а лишний сброс стоил бы подпроцесса
	// taskctl всему экрану.
	out, code, err := taskctlDo(found.Path, args...)
	if !body.DryRun {
		s.forgetBoard(found.Path)
	}
	if err != nil {
		s.logf("цепочка в %s не прошла: %v", found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"project": found.Name, "plan": chainPlan(out), "message": out,
		"dry_run": body.DryRun}
	if body.DryRun {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Коммит один на всю цепочку, как и запись: ID в subject это ID первой
	// строки первого уровня, по ней цепочку и ищут в истории доски.
	head := body.Levels[0][0]
	if note := commitDocs(found.Path, boardCommitMsg(head, "цепочка с дашборда"),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("цепочка в %s: %s", found.Name, note)
	}
	s.logf("цепочка в %s: %s", found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// chainArgs собирает аргументы `taskctl chain` из выбора человека. Пустой
// уровень и чужой ID отбиваются тут: до подпроцесса такой запрос не доходит, а
// человек получает ту же причину словами.
func chainArgs(after []string, levels [][]string, dry bool) ([]string, error) {
	if len(levels) == 0 {
		return nil, fmt.Errorf("цепочке нужен хотя бы один уровень")
	}
	if len(levels) > chainLevelsMax {
		return nil, fmt.Errorf("уровней %d, потолок %d: цепочку такой глубины собирают командой, а не диалогом",
			len(levels), chainLevelsMax)
	}
	n := len(after)
	for i, level := range levels {
		if len(level) == 0 {
			return nil, fmt.Errorf("уровень %d пуст: в нём нужна хотя бы одна задача", i+1)
		}
		n += len(level)
	}
	if n > chainIDsMax {
		return nil, fmt.Errorf("задач в цепочке %d, потолок %d", n, chainIDsMax)
	}
	args := []string{"chain"}
	if len(after) > 0 {
		ids, err := chainIDs(after)
		if err != nil {
			return nil, err
		}
		args = append(args, "--after", strings.Join(ids, ","))
	}
	for _, level := range levels {
		ids, err := chainIDs(level)
		if err != nil {
			return nil, err
		}
		args = append(args, strings.Join(ids, " "))
	}
	if dry {
		args = append(args, "--dry-run")
	}
	return args, nil
}

// chainIDs чистит список номеров: пробелы по краям, верхний регистр и рубеж
// формы ID. Номер не той формы это опечатка в диалоге, и уходить ей в командную
// строку подпроцесса незачем.
func chainIDs(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, raw := range list {
		id := strings.ToUpper(strings.TrimSpace(raw))
		if !goalIDRe.MatchString(id) {
			return nil, fmt.Errorf("%q не похоже на ID задачи", raw)
		}
		out = append(out, id)
	}
	return out, nil
}

// chainPlan разбирает ответ утилиты на строки плана. Пустые строки уходят:
// диалог рисует по строке на уровень, и пустая читалась бы пропущенным
// уровнем.
func chainPlan(out string) []string {
	plan := []string{}
	for _, ln := range strings.Split(out, "\n") {
		if said := strings.TrimSpace(ln); said != "" {
			plan = append(plan, said)
		}
	}
	return plan
}

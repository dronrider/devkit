package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/taskhead"
)

// Передача основного чата записи новому диспетчеру (LLD DK-1422, решения 3 и
// 5). Диспетчер это голова текущего носителя, отдельного поля в журнале нет:
// передача идёт штатными механизмами и строкой журнала, а не сменой пометки.
// Строка «передача» пишется первой, затем уходящей голове заказывается стоп
// существующим «Стопом», лента отмечает передачу системной репликой, и после
// освобождения имени окна поднимается новый носитель.

// handleTaskHandover передаёт работу записи новому диспетчеру. Носителем
// сегодня назначается конвейер, и передача идёт его механизмами: стоп это тот
// же стоп сессии, что у кнопки «Стоп», а подъём это тот же общий подъём
// taskhead. Разговоровым носителям передача понадобится с их писателями
// (DK-1429, DK-1430), до них строка чужого ключа отказывает словами.
func (s *server) handleTaskHandover(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("передача отклонена: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	if isGoalTitle(row.Title) {
		s.logf("передача %s в %s отклонена: цель без конвейера", id, found.Name)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "у цели носителем становится виток, передача витков ждёт своего писателя"})
		return
	}
	var body struct {
		Harness string `json:"harness"`
		Tier    string `json:"tier"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		s.logf("передача %s в %s отклонена: битое тело запроса 400", id, found.Name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"harness\": \"...\", \"tier\": \"...\"} либо пустое тело"})
		return
	}
	bind := s.mainChatBind(id)
	if bind.Chat == "" {
		s.logf("передача %s в %s отклонена: назначения нет", id, found.Name)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "у записи нет назначенного разговора: передавать нечего, работу поднимает запуск"})
		return
	}
	if bind.Chat != "tmux-task-"+id {
		s.logf("передача %s в %s отклонена: носитель %s вне конвейера", id, found.Name, bind.Chat)
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
			"носитель %s назначен не конвейером, передача его ждёт своего писателя", bind.Chat)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		s.logf("передача %s в %s отклонена: tmux не нашёлся 502", id, found.Name)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Строка журнала стоит первой: назначение смещается одной строкой до
	// всякого стопа, и реплика, вошедшая в передачу, ложится безадресно во
	// вход записи по решению 4. Уходящей голове она уже не достаётся.
	s.mainChatMove(id, bind.Chat, bind.Chat, found.Name)
	// Стоп уходящей голове существующим «Стопом»: сторож снимается с окна до
	// его смерти, сессия убивается точным именем, лента получает строку о
	// стопе. Живой работы может и не быть, тогда передавать нечего и нечего
	// останавливать, подъём ниже встаёт на свободное имя.
	stopped := ""
	if kind, note, err := s.stopTaskWork(found, id); err != nil {
		s.logf("передача %s в %s не удалась на стопе: %s", id, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	} else if kind != "" {
		stopped = kind
		if note != "" {
			s.logf("передача %s в %s: %s", id, found.Name, note)
		}
	}
	// Передача говорит о себе в ленте: стоп уведомителя называет снятие, а
	// человек должен видеть и саму смену диспетчера, история передачи видна
	// разговору из журнала назначений.
	what := "живой головы не было, стоп не понадобился"
	if stopped != "" {
		what = "прежней голове заказан стоп"
	}
	s.saidMark(saidTaskKey(id), fmt.Sprintf(
		"работа передана новому диспетчеру: %s, новый поднят в tmux-сессии task-%s", what, id))
	// Подъём нового носителя после освобождения имени окна: конвейер поднимается
	// свежим окном и читает состояние с диска, это его способ продолжения.
	harness, tier, tierWhy, tierBad := s.runAssign(found.Path, id, body.Tier, body.Harness, false)
	if tierBad != "" {
		s.logf("передача %s в %s отклонена: %s", id, found.Name, tierBad)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tierBad})
		return
	}
	own := harness
	client := defaultClient
	if own != nil {
		client = own.Bin
	}
	if m := clientMissing(client); m != "" {
		s.logf("передача %s в %s не удалась: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	model := ""
	if own != nil {
		model = own.tierModel(tier)
		if m := own.tierModel(tier); m != "" {
			if err := s.chatStoreWrite("tmux-task-"+id, chatStore{Model: m}); err != nil {
				s.logf("модель конвейера %s не записалась: %v", id, err)
			}
		}
	}
	res, err := s.startTaskSession(found, id, "task-"+id, own, model,
		runPrompt(row.Sect, id), runPrompt("in-progress", id), "работа", false)
	if err != nil {
		var busy *headBusy
		if errors.As(err, &busy) {
			s.logf("передача %s в %s отклонена: замок головы занят 409", id, found.Name)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		s.logf("передача %s в %s не удалась: %s", id, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.logf("передача %s в %s: строка журнала, стоп %q, подъём %s", id, found.Name, stopped, res.Rung)
	resp := map[string]string{"id": id, "kind": "task", "session": "task-" + id, "tier": tier,
		"message": fmt.Sprintf("работа передана новому диспетчеру: %s, конвейер поднят %s",
			what, headWhere(res, "task-"+id))}
	if own != nil {
		resp["harness"] = own.Name
	}
	if model != "" {
		resp["model"] = model
	}
	resp["message"] += ", " + tierWhy
	writeJSON(w, http.StatusOK, resp)
}

// stopTaskWork снимает живую конвейерную работу строки тем же порядком, что и
// «Стоп» (handleRunStop): работа ищется среди живых работ проекта, сторож
// снимается с окна до его смерти, сессия убивается точным именем, а лента
// получает строку уведомителя о стопе. Пустой род значит, что живой работы у
// строки нет; note это приписка о непоездке уведомителя, а не отказ.
func (s *server) stopTaskWork(p *Project, id string) (kind, note string, err error) {
	raw, err := s.projectBoard(p.Path)
	if err != nil {
		return "", "", err
	}
	view, err := parseBoardView(raw)
	if err != nil {
		return "", "", fmt.Errorf("ответ taskctl не разобрался: %v", err)
	}
	rowed := rowWorks(s.liveWorks(p.Path, view.Prefix, raw), id)
	var work *Work
	for i := range rowed {
		if rowed[i].Via == workViaTmux {
			work = &rowed[i]
			break
		}
	}
	if work == nil {
		return "", "", nil
	}
	kind = work.Kind
	sess := kind + "-" + id
	note = ""
	if kind == "goal" {
		if gr := goalRunPath(s.cfg.Roots); gr == "" {
			note = goalRunMissing
		} else if _, err := runProc("python3", gr, id, "-C", p.Path, "--say", "стоп из дашборда"); err != nil {
			note = fmt.Sprintf("строка про стоп в журнал цикла не записалась: %s", procErr(err))
		}
	}
	s.chatWatchOff(sess)
	if _, err := runProc("tmux", "kill-session", "-t", "="+sess); err != nil {
		return kind, note, fmt.Errorf("tmux не снял сессию %s: %s", sess, procErr(err))
	}
	if say := s.sayStop(p.Path, p.Name, id, kind); say != "" {
		note = strings.TrimPrefix(note+"; "+say, "; ")
	}
	return kind, note, nil
}

// devkitTree называет чекаут devkit по корням конфига: тот же адрес, по
// которому ищутся оболочка конвейера, уведомитель и профили харнесов.
func devkitTree(roots []string) string {
	tr := taskRunPath(roots)
	if tr == "" {
		return ""
	}
	return strings.TrimSuffix(tr, string(filepath.Separator)+filepath.FromSlash(taskhead.TaskRunRel))
}

// resumeWay отвечает, умеет ли харнес пары «модель+подписка» продолжение
// прошлой сессии (LLD DK-1422, решение 5): ключ resume в секции [head]
// профиля. Профиля нет значит, что судить нечем, и продолжение идёт как
// раньше: отказ читался бы как «харнес без продолжения» на машине без чекаута
// devkit, где не работает и сам клиент.
func (s *server) resumeWay(model, harness string) bool {
	h := s.chatHarnessOf(model, harness)
	if h == nil {
		return true
	}
	dk := devkitTree(s.cfg.Roots)
	if dk == "" {
		return true
	}
	head, err := taskhead.ReadHead(taskhead.ProfilePath(dk, h.Name))
	if err != nil {
		return true
	}
	return len(head.Resume) > 0
}

// chatStateLine подкладывает доступное состояние записи в первую реплику
// свежей сессии (LLD DK-1422, решение 5): полнота восстановления не
// объявляется, свежая голова зовёт прежние отрезки основного чата и файл
// записи, остальное дочитывает сама. Пустой возврат значит, что записи за
// разговором нет.
func (s *server) chatStateLine(id string) string {
	if id == "" || !goalIDRe.MatchString(id) {
		return ""
	}
	segs := mainchat.Segments(mainchat.All(s.mainChatData())[id])
	tail := fmt.Sprintf("постановка и ход работы в файле docs/tasks/%s.md", id)
	if len(segs) == 0 {
		return "Прежнего основного чата у записи нет, " + tail + ". "
	}
	names := make([]string, len(segs))
	for i, sg := range segs {
		names[i] = sg.Chat
	}
	return fmt.Sprintf("Прежние отрезки основного чата записи: %s; %s. ",
		strings.Join(names, ", "), tail)
}

// liftWindow называет имя окна подъёма: то же, в котором стоял прежний заход,
// ведь свободное имя клеит новый заход в тот же разговор (DK-430.15). Имя
// окна конвейера и цели здесь не занимает разговор: его носитель это сама
// работа, и разговор в нём гость. Занято либо чужое имя значит, что подъём
// берёт свежее.
func liftWindow(id, was string, alive func(string) bool) string {
	if was == "" || was == "task-"+id || was == "goal-"+id || alive(was) {
		return chatNewName(id, alive)
	}
	return was
}


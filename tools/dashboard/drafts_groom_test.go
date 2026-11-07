package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/mainchat"
)

// Мысли записи и признак прошедшего разбора едут с текстом записи (DK-1564):
// по паре чисел и признаку кнопка чата отличает первый разбор, повторный повод
// и просто разговор, не разбирая текст на клиенте.
func TestDraftIdeasAndGroomedFields(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "накопитель не отличает разобранные мысли", "prio": "mid"}`).Body.Close()

	fields := func() (total, undecided int, groomed bool) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("текст записи: %d %s", resp.StatusCode, text)
		}
		var v struct {
			Total     int  `json:"ideas_total"`
			Undecided int  `json:"ideas_undecided"`
			Groomed   bool `json:"groomed"`
		}
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("ответ записи не разобрался: %v\n%s", err, text)
		}
		list := draftsResp(t, c, e)
		drafts := list["drafts"].([]any)
		row := drafts[0].(map[string]any)
		if row["ideas_total"] != float64(v.Total) || row["ideas_undecided"] != float64(v.Undecided) || row["groomed"] != v.Groomed {
			t.Fatalf("список расходится с записью: %v; запись %s", row, text)
		}
		return v.Total, v.Undecided, v.Groomed
	}
	put := func(text string) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"text": text, "base": draftBase(t, c, e, "XR-005")})
		if err != nil {
			t.Fatal(err)
		}
		resp := doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005", string(payload))
		if text := body(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("правка записи: %d %s", resp.StatusCode, text)
		}
	}

	if total, undecided, groomed := fields(); total != 0 || undecided != 0 || groomed {
		t.Fatalf("свежая запись: мыслей %d/%d, разбор %v, жду 0/0 и нет разбора", total, undecided, groomed)
	}
	put("запись без мыслей\n\n## Идеи\n\n- 2026-11-05: сделать вход в чат без запуска\n- 2026-11-05: убрать портянку тел\n")
	if total, undecided, groomed := fields(); total != 2 || undecided != 2 || groomed {
		t.Fatalf("мысли без судеб: %d/%d, разбор %v, жду 2/2 и нет разбора", total, undecided, groomed)
	}
	put("запись с судьбой\n\n## Идеи\n\n- 2026-11-05: сделать вход в чат без запуска, поднята в задаче XR-001\n- 2026-11-05: убрать портянку тел\n")
	if total, undecided, groomed := fields(); total != 2 || undecided != 1 || !groomed {
		t.Fatalf("мысль с судьбой: %d/%d, разбор %v, жду 2/1 и признак разбора", total, undecided, groomed)
	}
}

// Повторный разбор без повода не поднимается (DK-1564): судьбы приписаны,
// вопросов нет, и разговор продолжается в чате записи. Отказ бьёт пустой
// повтор, а первый разбор, уточнение человека, продолжение выполнением и
// новый повод остаются законными входами, в том числе прямым запросом мимо
// кнопки.
func TestDraftGroomRefusesEmptyRepeat(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "повторный разбор без повода", "prio": "mid"}`).Body.Close()

	put := func(text string) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"text": text, "base": draftBase(t, c, e, "XR-005")})
		if err != nil {
			t.Fatal(err)
		}
		resp := doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005", string(payload))
		if text := body(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("правка записи: %d %s", resp.StatusCode, text)
		}
	}
	groom := func(req string) *http.Response {
		return doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", req)
	}

	put("запись после разбора\n\n## Идеи\n\n- 2026-11-05: сделать вход в чат без запуска, поднята в задаче XR-001\n" +
		"- 2026-11-05: убрать портянку тел, отклонена: портянку оставили\n")
	resp := groom("")
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "неразобранных мыслей нет") {
		t.Fatalf("пустой повтор разбора: %d %s, ожидал 409 со словами", resp.StatusCode, text)
	}

	// Уточнение человека остаётся входом и без повода: вопрос меняет разговор,
	// а не поднимает новую ходку вслепую.
	resp = groom(`{"ask": "перечитай материал записи"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уточнение после разбора: %d %s", resp.StatusCode, text)
	}
	// Продолжение выполнением идёт своим исходом, отказ его не трогает.
	writeTmuxFake(t, e.bin, tmuxLog, "")
	resp = groom(`{"mode": "run"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("продолжение выполнением: %d %s", resp.StatusCode, text)
	}

	// Новая неразобранная мысль снова поднимает разбор: повод вернулся.
	writeTmuxFake(t, e.bin, tmuxLog, "")
	put("запись с новой мыслью\n\n## Идеи\n\n- 2026-11-05: сделать вход в чат без запуска, поднята в задаче XR-001\n" +
		"- 2026-11-05: убрать портянку тел, отклонена: портянку оставили\n- 2026-11-05: сменить шапку\n")
	resp = groom("")
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("разбор по новому поводу: %d %s", resp.StatusCode, text)
	}
}

// Чтение подготовленных задач закрепляет номер за унаследованной карточкой
// старого вида (DK-1564): номер обязан стоять до применения решения, ключ
// остаётся прежним, решённая карточка не трогается, а повторное чтение номер
// не меняет. Ссылка из старого чата адресуется ключом, и ключ на месте.
func TestDraftPreparedAdoptsLegacyOnRead(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "доставка чата из старого разбора", "prio": "mid"}`).Body.Close()
	path := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Фикстура ложится старым блоком в саму запись: prepared-файла нет, ключи
	// короткие, номера нет. Так лежат карточки, оставшиеся до DK-1564.
	state := draftproposal.State{Version: 1, State: "active", Analysis: "старый разбор", Proposals: []draftproposal.Proposal{
		{ID: "chat-delivery", Revision: 3, Outcome: "add", Title: "Доставка чата", Text: "текст задачи"},
		{ID: "done-key", Revision: 1, Outcome: "drop", Text: "лишнее", Reason: "устарело", Status: "rejected"},
	}}
	data, err = draftproposal.Replace(data, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	read := func() (map[string]draftproposal.Proposal, string) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005/prepared", "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("подготовленные задачи: %d %s", resp.StatusCode, text)
		}
		var view struct {
			State draftproposal.State `json:"state"`
		}
		if err = json.Unmarshal([]byte(text), &view); err != nil {
			t.Fatalf("ответ не разобрался: %v\n%s", err, text)
		}
		byKey := map[string]draftproposal.Proposal{}
		for _, p := range view.State.Proposals {
			byKey[p.ID] = p
		}
		return byKey, text
	}
	byKey, text := read()
	legacy, ok := byKey["chat-delivery"]
	if !ok {
		t.Fatalf("карточка со старым ключом пропала: %s", text)
	}
	number := regexp.MustCompile(`^XR-[0-9]+$`).FindString(legacy.BoardID)
	if number == "" {
		t.Fatalf("у карточки со старым ключом нет номера доски: %+v", legacy)
	}
	if done := byKey["done-key"]; done.BoardID != "" || done.Status != "rejected" {
		t.Errorf("решённая карточка тронута закреплением: %+v", done)
	}
	saved, err := draftproposal.ParsePrepared([]byte(readFile(t, draftproposal.PreparedPath(e.proj, "XR-005"))))
	if err != nil {
		t.Fatalf("prepared-файл не прочитался: %v", err)
	}
	found := false
	for _, p := range saved.Proposals {
		if p.ID == "chat-delivery" {
			found = true
			if p.BoardID != number {
				t.Errorf("номер в файле %q, в ответе %q", p.BoardID, number)
			}
		}
	}
	if !found {
		t.Fatalf("карточка со старым ключом не переехала в prepared-файл:\n%s", readFile(t, draftproposal.PreparedPath(e.proj, "XR-005")))
	}

	// Повторное чтение номер не меняет и ничего не пишет: закрепление свершилось
	// один раз, дальше чтение только читает.
	byKey, text = read()
	if again := byKey["chat-delivery"].BoardID; again != number {
		t.Fatalf("номер при повторном чтении сменился: %q, был %q\n%s", again, number, text)
	}
	// Старый блок срезан из записи: карточка живёт в prepared-файле, запись
	// осталась материалом.
	if fresh := readFile(t, path); strings.Contains(fresh, `"chat-delivery"`) {
		t.Errorf("старый блок разбора остался в записи:\n%s", fresh)
	}
}

// Состав согласования накопителя читается одним ответом на все записи
// (DK-1623): прежде экран звал ручку записи на каждый черновик, и при сотнях
// записей волна запросов клала канал дашборда и посредника. Общая ручка стоит
// на закреплении номеров taskctl без ID, ей же держится закрепление карточек
// старого вида, а повторное чтение номера не меняет.
func TestDraftsPreparedServedInOneResponse(t *testing.T) {
	e, c, _ := tasksEnv(t)

	// Пустой накопитель отвечает пустым списком видов, а не отказом: пустая
	// секция согласования неотличима от неотрисованной, если молчит словами.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/prepared", "")
	empty := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("пустой состав согласования: %d %s", resp.StatusCode, empty)
	}
	if !strings.Contains(empty, `"views":[]`) {
		t.Fatalf("пустой накопитель пришёл не пустым списком видов: %s", empty)
	}

	for _, title := range []string{"первая", "вторая", "третья", "четвёртая", "пятая", "шестая"} {
		doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			`{"title": "`+title+`", "prio": "mid"}`).Body.Close()
	}
	// XR-005 ложится старым блоком в саму запись: карточка без номера и решённая
	// карточка, как они остались до DK-1564. Остальные записи без подготовленных
	// задач вовсе.
	path := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state := draftproposal.State{Version: 1, State: "active", Analysis: "старый разбор", Proposals: []draftproposal.Proposal{
		{ID: "chat-delivery", Revision: 3, Outcome: "add", Title: "Доставка чата", Text: "текст задачи"},
		{ID: "done-key", Revision: 1, Outcome: "drop", Text: "лишнее", Reason: "устарело", Status: "rejected"},
	}}
	data, err = draftproposal.Replace(data, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	read := func() (map[string]map[string]any, string) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/prepared", "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("общий состав согласования: %d %s", resp.StatusCode, text)
		}
		var v struct {
			Views []map[string]any `json:"views"`
		}
		if err = json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("ответ не разобрался: %v\n%s", err, text)
		}
		byID := map[string]map[string]any{}
		for _, view := range v.Views {
			byID[view["id"].(string)] = view
		}
		return byID, text
	}
	byID, text := read()
	if len(byID) != 6 {
		t.Fatalf("видов %d, а записей в накопителе 6:\n%s", len(byID), text)
	}
	// Запись без подготовленных задач приходит с пустым списком карточек, а не
	// пропадает из ответа: экран считает секции по одним и тем же видам.
	if props, _ := byID["XR-006"]["state"].(map[string]any); props != nil {
		if list, _ := props["proposals"].([]any); len(list) != 0 {
			t.Errorf("у записи без подготовленных задач оказался состав: %v", byID["XR-006"]["state"])
		}
	}
	// Снимок и база на месте: по ним экран держит правку и подтверждение.
	view := byID["XR-005"]
	snapshot, _ := view["snapshot"].(string)
	basis, _ := view["basis"].(string)
	if snapshot == "" || basis == "" {
		t.Errorf("у вида записи нет снимка или базы: %v", view)
	}
	st := view["state"].(map[string]any)
	var number string
	for _, raw := range st["proposals"].([]any) {
		p := raw.(map[string]any)
		boardID, _ := p["board_id"].(string)
		switch p["id"] {
		case "chat-delivery":
			number = regexp.MustCompile(`^XR-[0-9]+$`).FindString(boardID)
			if number == "" {
				t.Errorf("карточка старого вида осталась без номера: %v", p)
			}
		case "done-key":
			if boardID != "" || p["status"] != "rejected" {
				t.Errorf("решённая карточка тронута закреплением: %v", p)
			}
		}
	}
	if number == "" {
		t.Fatalf("карточка старого вида пропала из состава: %s", text)
	}

	// Повторное общее чтение номер не меняет и ничего не пишет.
	byID, text = read()
	for _, raw := range byID["XR-005"]["state"].(map[string]any)["proposals"].([]any) {
		p := raw.(map[string]any)
		boardID, _ := p["board_id"].(string)
		if p["id"] == "chat-delivery" && boardID != number {
			t.Fatalf("номер при повторном чтении сменился: %q, был %q\n%s", boardID, number, text)
		}
	}

	// Битый prepared-файл одной записи виден словами отказа, а не пустым
	// списком: молчаливая пустота неотличима от пустого накопителя.
	broken := draftproposal.PreparedPath(e.proj, "XR-007")
	if err = os.WriteFile(broken, []byte("not toml {{{"), 0644); err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/prepared", "")
	if resp.StatusCode == http.StatusOK || !strings.Contains(body(t, resp), "error") {
		t.Fatalf("битая запись читается молча: %d %s", resp.StatusCode, body(t, resp))
	}
	if err = os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if _, text = read(); !strings.Contains(text, `"XR-005"`) {
		t.Fatalf("после починки состав не читается: %s", text)
	}
}

func TestDraftFailedGroomStatusAndRetry(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "prepared"}[prepared], func(t *testing.T) {
			e, c, _ := tasksEnv(t)
			writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
			writeScript(t, e.bin, "claude", "exit 0")
			writeAgentctlFake(t, e.bin, harnessTiersFixture)
			doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"title":"ошибка первого разбора","prio":"mid"}`).Body.Close()
			path, _ := draftPathOf(e.proj, "XR-005")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			state := draftproposal.State{Version: 1, State: "active", Analysis: "error"}
			if prepared {
				err = draftproposal.SavePrepared(e.proj, "XR-005", state)
			} else {
				data, err = draftproposal.Replace(data, state)
				if err == nil {
					err = os.WriteFile(path, data, 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := mainchat.Put(e.home, mainchat.Bind{Record: "XR-005", Project: "demo", Chat: "failed-carrier", Source: mainchat.ByWork}); err != nil {
				t.Fatal(err)
			}
			primary := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-005/chat", "")
			var binding map[string]any
			json.Unmarshal([]byte(body(t, primary)), &binding)
			if primary.StatusCode != http.StatusOK || binding["chat"] != "failed-carrier" {
				t.Fatalf("назначение черновика: %d %v", primary.StatusCode, binding)
			}
			resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
			var detail map[string]any
			json.Unmarshal([]byte(body(t, resp)), &detail)
			rows := draftsResp(t, c, e)["drafts"].([]any)
			row := rows[0].(map[string]any)
			for _, v := range []map[string]any{detail, row} {
				if v["groom_error"] != true || v["groomed"] != true || v["ideas_undecided"] != float64(0) {
					t.Fatalf("ошибка первого разбора не видна: %v", v)
				}
			}
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
			if text := body(t, resp); resp.StatusCode != http.StatusOK {
				t.Fatalf("повтор после ошибки: %d %s", resp.StatusCode, text)
			}
		})
	}
}

func TestDraftIdeaThroughRealTaskctl(t *testing.T) {
	st := newScenarioStand(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	if os.Getenv("DASHBOARD_ACCEPT_BIN") != "" {
		writeScript(t, st.e.bin, "taskctl", "exec "+shQuote(st.bin)+" \"$@\"")
	}
	base := draftThoughtsServer(t, st.e)
	url := base + "/api/projects/demo/drafts/XR-4"
	phrase := "До разбора добавить доступ к исходнику"
	payload, _ := json.Marshal(map[string]string{"text": phrase})
	resp := doReq(t, st.c, "POST", url+"/ideas", string(payload))
	output := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CLI записал мысль, но API ответил ошибкой: %d %s", resp.StatusCode, output)
	}
	var result map[string]any
	if err = json.Unmarshal([]byte(output), &result); err != nil || result["message"] != "мысль записана" {
		t.Fatalf("ответ API: %s %v", output, err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil || strings.Count(string(data), phrase) != 1 {
		t.Fatalf("мысль не записана ровно один раз: %s %v", data, err)
	}
	resp = doReq(t, st.c, "GET", url, "")
	var detail map[string]any
	if err = json.Unmarshal([]byte(body(t, resp)), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["ideas_undecided"] != float64(1) || detail["groomed"] != false || detail["ideas_allowed"] != true {
		t.Fatalf("мысль сырого черновика не видна: %v", detail)
	}
	if !strings.Contains(detail["text"].(string), phrase) {
		t.Fatalf("сохранённая мысль не читается: %v", detail)
	}
	if err = draftproposal.SavePrepared(st.e.proj, "XR-4", draftproposal.State{Version: 1, State: "active", Analysis: "empty"}); err != nil {
		t.Fatal(err)
	}
	preparedPath := draftproposal.PreparedPath(st.e.proj, "XR-4")
	beforePrepared := readFile(t, preparedPath)
	boardPath := filepath.Join(st.e.proj, "docs", "TASKS.md")
	beforeBoard := readFile(t, boardPath)
	resp = doReq(t, st.c, "POST", url+"/ideas", `{"text":"Замечание после успешного пустого разбора"}`)
	output = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(output, "Чате разбора") {
		t.Fatalf("мысль после результата: %d %s", resp.StatusCode, output)
	}
	if readFile(t, st.path) != string(data) || readFile(t, preparedPath) != beforePrepared || readFile(t, boardPath) != beforeBoard {
		t.Fatal("отказ после результата изменил исходник, комплект или доску")
	}
}

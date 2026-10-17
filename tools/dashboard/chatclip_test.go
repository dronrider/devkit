package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Длинные вставки и несколько вложений одной реплики (DK-1403). Вставка, не
// пролезающая в тело реплики, кладётся файлом в каталог загрузок той же
// дорогой, что и снимок экрана: реплика несёт ссылку, файл агент читает сам.
// Стенды тут про саму дорогу файла: фрагмент ложится и читается, отказ
// размера называется словами, а не «жду JSON», и файлы не остаются сиротами,
// когда запись разговора уходит.

// clipPut кладёт текстовый фрагмент в чат и отдаёт путь, имя и размер файла.
func clipPut(t *testing.T, e *testEnv, c *http.Client, sid, text string) (string, string, int) {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/clip",
		fmt.Sprintf(`{"text": %q}`, text))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("фрагмент не лёг: %d %s", resp.StatusCode, got)
	}
	var put struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Bytes int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(got), &put); err != nil {
		t.Fatal(err)
	}
	if put.Path == "" || put.Name == "" || put.Bytes == 0 {
		t.Fatalf("ручка фрагмента ответила неполно: %s", got)
	}
	return put.Path, put.Name, put.Bytes
}

// Фрагмент ложится файлом в каталог вложений разговора и читается оттуда
// целиком, с переводами строк и без перемен по дороге. Второй фрагмент той же
// секунды ложится рядом, а не поверх первого: имена режутся с точностью до
// секунды, а вставляют вложения друг за другом.
func TestChatClipLandsAndServes(t *testing.T) {
	e, c := chatEnv(t)
	text := "первый фрагмент\nвторая строка с переводом\r\nтретья с кареткой"
	path, name, size := clipPut(t, e, c, "blank-clip1", text)
	want := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if size != len(want) {
		t.Fatalf("размер посчитан по сырому тексту: %d, ждал %d", size, len(want))
	}
	if filepath.Base(path) != name || !strings.Contains(path, filepath.Join("blank-clip1")) {
		t.Fatalf("фрагмент лёг мимо каталога разговора: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файла фрагмента нет на диске: %v", err)
	}
	_, second, _ := clipPut(t, e, c, "blank-clip1", "второй фрагмент")
	if second == name {
		t.Fatalf("второй фрагмент лёг поверх первого: %s", second)
	}
	for nm, want := range map[string]string{name: want, second: "второй фрагмент"} {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/blank-clip1/clip?name="+nm, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("фрагмент %s не читается: %d", nm, resp.StatusCode)
		}
		if got := body(t, resp); got != want {
			t.Fatalf("фрагмент %s приехал не тем текстом: %q", nm, got)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Fatalf("фрагмент %s отдался не текстом: %s", nm, ct)
		}
	}
	// Побег из каталога вложений сводится к базе имени: ручка не превращается
	// в чтение произвольного файла.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/blank-clip1/clip?name=../"+name, "")
	if resp.StatusCode != http.StatusOK || body(t, resp) != want {
		t.Fatalf("побег имени не свёлся к базе: %d", resp.StatusCode)
	}
	// Чужого файла ручка не отдаёт.
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/blank-clip1/clip?name=nope.txt", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("чужое имя отдалось не отсутствием: %d", resp.StatusCode)
	}
}

// Отказы ручки фрагмента называются словами: пустой фрагмент, битый id
// разговора и вставка сверх предела говорят о себе, а не о внутренностях
// разбора JSON.
func TestChatClipRefusalsNameSize(t *testing.T) {
	e, c := chatEnv(t)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/blank-x/clip", `{"text": "  "}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body(t, resp), "пустой фрагмент класть некуда") {
		t.Fatalf("пустой фрагмент: %d %s", resp.StatusCode, body(t, resp))
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/плохой/clip", `{"text": "текст"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body(t, resp), "не похоже на id чата") {
		t.Fatalf("битый id: %d %s", resp.StatusCode, body(t, resp))
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/blank-x/clip",
		`{"text": "`+strings.Repeat("a", clipLimit+1)+`"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(got, fmt.Sprintf("фрагмент длиннее предела %d МБ", clipLimit>>20)) {
		t.Fatalf("длинный фрагмент: %d %s", resp.StatusCode, got)
	}
}

// Черновик незачатого разговора держит ссылки вложений тем же запросом, что и
// текст: после перезагрузки вкладки коробка отправки собирается по ним, а
// пустой список снимает ссылки ушедшей реплики.
func TestChatDraftKeepsClipRefs(t *testing.T) {
	e, c := chatEnv(t)
	id := blankMake(t, e, c, `{"model": "sonnet"}`)
	path, _, size := clipPut(t, e, c, id, "длинная вставка ушла файлом")
	files := fmt.Sprintf(`[{"kind":"frag","name":"фрагмент","path":%q,"bytes":%d}]`, path, size)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/draft",
		`{"text": "глянь вложение", "files": `+files+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("черновик с вложениями не лёг: %d %s", resp.StatusCode, body(t, resp))
	}
	st := blankStore(t, e.home, id)
	if len(st.Files) != 1 || st.Files[0].Path != path || st.Files[0].Kind != "frag" {
		t.Fatalf("память диалога не держит ссылки вложений: %+v", st.Files)
	}
	row := blankRow(blankList(t, e, c, ""), id)
	if row == nil || len(row.Files) != 1 || row.Files[0].Name != "фрагмент" {
		t.Fatalf("строка списка без вложений черновика: %+v", row)
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/draft",
		`{"text": "", "files": []}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("пустой черновик не лёг: %d %s", resp.StatusCode, body(t, resp))
	}
	if st = blankStore(t, e.home, id); len(st.Files) != 0 {
		t.Fatalf("пустой список не снял ссылки вложений: %+v", st.Files)
	}
}

// Вложения держат незачатую запись так же, как текст черновика: вставка бывает
// собрана заранее и без слов, и терять её из-за отсутствия текста незачем.
func TestChatBlankFilesKeepRecordAlive(t *testing.T) {
	e, c := chatEnv(t)
	id := blankMake(t, e, c, `{"model": "sonnet"}`)
	path, _, _ := clipPut(t, e, c, id, "вставка без слов")
	st := blankStore(t, e.home, id)
	st.Born = time.Now().Add(-2 * time.Hour).Unix()
	st.Draft = ""
	st.Files = []chatUpload{{Kind: "frag", Name: "фрагмент", Path: path}}
	if err := e.s.chatStoreWrite(id, st); err != nil {
		t.Fatal(err)
	}
	if blankRow(blankList(t, e, c, ""), id) == nil {
		t.Fatal("вложения не удержали запись: час прошёл, а вставка жива")
	}
}

// Отжившая запись уносит свои вложения: файла без записи ничей, и разговор,
// если он всё же случился, ссылается на вложения своего sid. Закрытие рукой
// делает то же самое сразу.
func TestChatBlankGoneTakesUploadsWithIt(t *testing.T) {
	e, c := chatEnv(t)
	id := blankMake(t, e, c, `{"model": "sonnet"}`)
	clipPut(t, e, c, id, "вставка недожитого разговора")
	st := blankStore(t, e.home, id)
	st.Born = time.Now().Add(-2 * time.Hour).Unix()
	if err := e.s.chatStoreWrite(id, st); err != nil {
		t.Fatal(err)
	}
	if row := blankRow(blankList(t, e, c, ""), id); row != nil {
		t.Fatal("пустая запись не ушла по сроку")
	}
	if _, err := os.Stat(e.s.shotDir(id)); !os.IsNotExist(err) {
		t.Fatal("вложения не ушли вместе с записью")
	}

	other := blankMake(t, e, c, `{"model": "sonnet"}`)
	clipPut(t, e, c, other, "вставка закрытого разговора")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+other+"/drop", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("закрытие записи: %d %s", resp.StatusCode, body(t, resp))
	}
	if _, err := os.Stat(e.s.shotDir(other)); !os.IsNotExist(err) {
		t.Fatal("закрытие руками оставило вложения сиротами")
	}
}

// Вложения под ключом «new-» хозяина не получат никогда: реплика ждущей задаче
// кладёт файл до всякого разговора. Неделя проходит, и метла общего списка
// уносит их, а свежие не трогает: реплика с ними ещё может лежать в очереди.
func TestChatUploadSweepTakesStaleFreshKeys(t *testing.T) {
	e, c := chatEnv(t)
	root := e.s.shotDir("")
	old := filepath.Join(root, "new-old")
	fresh := filepath.Join(root, "new-fresh")
	for _, dir := range []string{old, fresh} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	blankList(t, e, c, "")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("недельные вложения без разговора не смелись")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("свежие вложения метла тронула: %v", err)
	}
}

// Разметка панели живёт своими статическими проверками в chatfold_test.go: там
// же отказы размера, и весь тот файл собирается на старом коде, отчего краснота
// регрессии видна regcheck.


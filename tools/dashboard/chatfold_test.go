package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Порог свёртки вставки и отказы размера (DK-1403). Файл нарочно стоит особняком
// от стендов дороги вложений: он не зовёт ни одного нового символа, собирается
// на старом коде, и краснота регрессии видна regcheck.

// Отказ размера у черновика и реплики называет предел и выход: длинная вставка
// в поле и была главным посетителем обеих ручек, а прежний ответ «жду JSON»
// читался поломкой разбора, хотя тело просто не пролезало в предел.
func TestChatSayAndDraftRefuseOversizeWithWords(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, time.Now())
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/aaaa-1111/say",
		`{"text": "`+strings.Repeat("a", msgBodyLimit+1)+`"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(got, fmt.Sprintf("реплика длиннее предела %d КБ", msgBodyLimit/1024)) ||
		strings.Contains(got, "жду JSON") {
		t.Fatalf("отказ размера реплики: %d %s", resp.StatusCode, got)
	}
	id := blankMake(t, e, c, `{"model": "sonnet"}`)
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/draft",
		`{"text": "`+strings.Repeat("a", msgBodyLimit+1)+`"}`)
	got = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(got, fmt.Sprintf("черновик длиннее предела %d КБ", msgBodyLimit/1024)) ||
		strings.Contains(got, "жду JSON") {
		t.Fatalf("отказ размера черновика: %d %s", resp.StatusCode, got)
	}
}

// Разметка панели: порог свёртки вставки равен порогу выжимки команд, вставка
// длиннее порога идёт блоком, дубликат не добавляется вторым блоком, а префиксы
// вложений стоят в том порядке, в котором их снимает сервер.
func TestStaticChatClipsFoldDedupAndOrder(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(text, "thresholdRunes") {
		t.Fatal("порог свёртки не сверяется с порогом выжимки команд")
	}
	if !strings.Contains(funcBody(t, text, "const CHAT_FOLD_RUNES"), "4096") {
		t.Fatal("порог свёртки вставки не равен порогу выжимки команд")
	}
	if !strings.Contains(funcBody(t, text, "const shotKey"), "st.sid || st.blank") {
		t.Fatal("ключ вложения не знает про незачатый разговор")
	}
	paste := funcBody(t, text, `ta.addEventListener("paste"`)
	if !strings.Contains(paste, "runesOf(text) >= CHAT_FOLD_RUNES") {
		t.Fatal("длинная вставка не сворачивается в блок")
	}
	if !strings.Contains(paste, `addClip({ kind: "image"`) || !strings.Contains(paste, `addClip({ kind: "frag"`) {
		t.Fatal("вставка из буфера не кладёт вложения списком")
	}
	add := funcBody(t, text, "const addClip")
	if !strings.Contains(add, "c.kind === clip.kind") || !strings.Contains(add, "was.hi") {
		t.Fatal("дубликат вставки не подсвечивает стоящий блок")
	}
	attach := funcBody(t, text, "function attachPrefix")
	shotAt := strings.Index(attach, "shotPrefix")
	fragAt := strings.Index(attach, "fragPrefix")
	if shotAt < 0 || fragAt < 0 || shotAt > fragAt {
		t.Fatal("префиксы вложений не в порядке съёма сервером")
	}
	if !strings.Contains(funcBody(t, text, "const post = (text, sel, withClips"), "attachPrefix(withClips) + wire0") {
		t.Fatal("реплика не несёт префиксы всех вложений")
	}
	stored := funcBody(t, text, "function chatClipsStoredWrite")
	if !strings.Contains(stored, "clipLean") || strings.Contains(funcBody(t, text, "function clipLean"), "data") {
		t.Fatal("ссылки вложений в хранилище едут с данными")
	}
	if !strings.Contains(text, "resend(again, rec.id, rec.wire)") {
		t.Fatal("повтор восстановленной записи едет без своего провода")
	}
}

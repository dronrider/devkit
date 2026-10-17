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
// длиннее порога идёт блоком, дубликат не добавляется вторым блоком, а ссылки
// вложений незачатого разговора переживают перезагрузку вкладки.
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
	if !strings.Contains(funcBody(t, text, "const post = (text, sel, withClips"), "attachPrefix(withClips) + wire0") {
		t.Fatal("реплика не несёт префиксы всех вложений")
	}
	stored := funcBody(t, text, "function chatClipsStoredWrite")
	if !strings.Contains(stored, "clipLean") || strings.Contains(funcBody(t, text, "function clipLean"), "data") {
		t.Fatal("ссылки вложений в хранилище едут с данными")
	}
	// Пустое хранилище вкладки читается null, а не пустым списком: пустой
	// массив в цепочке источников истиннен, и вложения записи незачатого
	// разговора не доходили до панели.
	read := funcBody(t, text, "function chatClipsStoredRead")
	if !strings.Contains(read, "if (!raw) return null") {
		t.Fatal("пустое хранилище вкладок читается пустым списком, вложения записи не достаются")
	}
	// Первое касание панели не пишет пустой список на сервер: он стёр бы
	// вложения черновика раньше, чем вкладка их показала.
	keep := funcBody(t, text, "const attachKeep")
	if !strings.Contains(keep, `lean === "[]" && !attachPushed`) {
		t.Fatal("первое касание панели проталкивает пустой список вложений на сервер")
	}
	if !strings.Contains(text, "resend(again, rec.id, rec.wire)") {
		t.Fatal("повтор восстановленной записи едет без своего провода")
	}
}

// Отправленный провод со смешанным порядком вложений разбирается чисто: панель
// кладёт префиксы в порядке вставки, и фрагмент перед снимком остаётся обычным
// случаем. Стенд зовёт только saidOf и смотрит текст, отчего собирается на
// старом коде, и краснота регрессии видна regcheck.
func TestSaidOfPeelsMixedOrder(t *testing.T) {
	wire := "<fragment file=\"/tmp/c.txt\">\nвставлен фрагмент 4 КБ\n</fragment>\n" +
		"<screenshot file=\"/tmp/a.png\">\nвставлен снимок экрана\n</screenshot>\nчто это значит"
	got := saidOf(wire, "socket")
	if got.Text != "что это значит" {
		t.Fatalf("смешанный порядок вложений оставил обёртку в тексте: %q", got.Text)
	}
}

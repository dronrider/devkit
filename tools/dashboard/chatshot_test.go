package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Вложение чата судится по содержимому, а не по заявленному виду (DK-1476):
// после DK-1403 клиент заявляет вид служебным словом "image" либо пустотой,
// и отказ по заявлению закрыл дорогу скриншота целиком. Стенды гоняют ручку
// с сигнатурами png и jpeg под пустым и служебным видом, сверяют расширение
// легшего файла с байтами, а не с заявлением, и байты вне четырёх картинок
// получают отказ.

// shotPut кладёт вложение в разговор и отдаёт ответ ручки целиком.
func shotPut(t *testing.T, e *testEnv, c *http.Client, sid, kind, data string) *http.Response {
	t.Helper()
	return doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/shot",
		fmt.Sprintf(`{"kind": %q, "data": %q}`, kind, data))
}

// shotName разбирает ответ принятого вложения и проверяет, что файл лёг на
// диск: имя потом сверяется с содержимым.
func shotName(t *testing.T, resp *http.Response) string {
	t.Helper()
	got := body(t, resp)
	var put struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Bytes int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(got), &put); err != nil {
		t.Fatal(err)
	}
	if put.Path == "" || put.Name == "" || put.Bytes == 0 {
		t.Fatalf("ручка вложения ответила неполно: %s", got)
	}
	if _, err := os.Stat(put.Path); err != nil {
		t.Fatalf("файла вложения нет на диске: %v", err)
	}
	return put.Name
}

func TestChatShotJudgedByBytes(t *testing.T) {
	e, c := chatEnv(t)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n\x00\x00"))
	jpeg := base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff\xe0дальше тело"))

	resp := shotPut(t, e, c, "blank-shot1", "", png)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("пустой заявленный вид с байтами png не принят: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".png") {
		t.Fatalf("расширение легло не по байтам png: %s", name)
	}

	resp = shotPut(t, e, c, "blank-shot1", "image", jpeg)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("служебный заявленный вид с байтами jpeg не принят: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".jpg") {
		t.Fatalf("расширение легло не по байтам jpeg: %s", name)
	}

	// Заявление png при байтах jpeg не перекрашивает файл: судят содержимое.
	resp = shotPut(t, e, c, "blank-shot1", "image/png", jpeg)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("байты jpeg под заявлением png не приняты: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".jpg") {
		t.Fatalf("заявление перекрасило расширение файла: %s", name)
	}

	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	resp = shotPut(t, e, c, "blank-shot1", "image", svg)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(got, "байты") {
		t.Fatalf("svg не отклонён отказом про содержимое: %d %s", resp.StatusCode, got)
	}
}

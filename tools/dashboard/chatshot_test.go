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

// Поле data разбирается по трём известным вкусам мусора (DK-1481): переводы
// строк и пробелы внутри base64, алфавит base64url с минусом и подчёркиванием
// и необрезанный префикс data-URL прямо в поле. Мусор четвёртого вида
// отказывает, а отказ кладёт в журнал вид, длину и голову поля: точный вкус
// виден по следу, без повторного отказа у пользователя.
func TestChatShotDirtyData(t *testing.T) {
	e, c := chatEnv(t)
	var lines []string
	e.s.logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n\x00\x00"))

	resp := shotPut(t, e, c, "blank-shot2", "image", png[:6]+"\n"+png[6:10]+" "+png[10:])
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("переводы строк и пробелы внутри base64 не приняты: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".png") {
		t.Fatalf("расширение легло не по байтам png: %s", name)
	}

	// Слэш внутри base64 переведён в подчёркивание: алфавит base64url.
	url64 := strings.NewReplacer("+", "-", "/", "_").Replace(
		base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n\x00\x00\xfb\xff")))
	resp = shotPut(t, e, c, "blank-shot2", "image", url64)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("алфавит base64url не принят: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".png") {
		t.Fatalf("расширение легло не по байтам png: %s", name)
	}

	resp = shotPut(t, e, c, "blank-shot2", "image/png", "data:image/png;base64,"+png)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("необрезанный префикс data-URL не принят: %d %s", resp.StatusCode, body(t, resp))
	}
	if name := shotName(t, resp); !strings.HasSuffix(name, ".png") {
		t.Fatalf("расширение легло не по байтам png: %s", name)
	}

	junk := "четвёртый вкус: не base64"
	resp = shotPut(t, e, c, "blank-shot2", "image", junk)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(got, "base64") {
		t.Fatalf("мусор вне известных видов не отклонён: %d %s", resp.StatusCode, got)
	}
	var noted string
	for _, line := range lines {
		if strings.Contains(line, "не разобралось как base64") {
			noted = line
		}
	}
	if noted == "" {
		t.Fatalf("отказ не оставил следа в журнале: %q", lines)
	}
	for _, want := range []string{"blank-shot2", `вид "image"`,
		fmt.Sprintf("длиной %d", len(junk)), fmt.Sprintf("%q", junk)} {
		if !strings.Contains(noted, want) {
			t.Errorf("строка журнала без %q: %s", want, noted)
		}
	}
}

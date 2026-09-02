package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Поиск в списке разговоров шёл по набору, уже урезанному кнопкой архива и
// окном списка: строка `list = chatArchShown(st.chats, chatArchMode())` в
// static/app.js стояла раньше запроса, и убранный или старый разговор не
// находился набранным текстом (DK-726). Стенд testdata/poc_chatfind.mjs
// сторожит саму логику отбора синтетическим DOM, здесь два прогона по-другому:
// TestChatFindBeyondWindow сверяет серверную часть, на которую опирается
// поиск (days=0 снимает окно), а TestDashboardSmokeChatFindArchived гоняет
// сценарий настоящим headless chrome по-настоящему поднятому серверу.

// chatFindDriverJS это агентская часть смоука: список открывается настоящим
// нажатием кнопки шапки панели (`.cdpick`), запрос уходит настоящим вводом в
// поле поиска шапки списка (`.cdtop input`) с настоящим событием input, а не
// вызовом внутренней функции приложения. Перед вводом водитель убеждается, что
// архивная запись не видна вовсе: без этой проверки смоук не отличил бы
// «поиск нашёл убранное» от «кнопка вообще ничего не прятала».
const chatFindDriverJS = `
(async function () {
  function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }
  async function waitFor(fn, timeout) {
    const t0 = Date.now();
    for (;;) {
      let v = null;
      try { v = fn(); } catch (e) { v = null; }
      if (v) return v;
      if (Date.now() - t0 > timeout) throw new Error("не дождался условия");
      await sleep(40);
    }
  }
  function liveSlot() {
    const pin = document.getElementById("cpin");
    if (!pin) return null;
    return Array.from(pin.querySelectorAll(".cslot")).find(
      (n) => !n.className.includes("off")) || null;
  }
  const result = { ok: false };
  try {
    await waitFor(() => {
      const p = document.getElementById("cpanel");
      return p && !p.hidden && liveSlot();
    }, 8000);
    const pick = await waitFor(() => liveSlot().querySelector(".cdpick"), 8000);
    pick.click();
    await waitFor(() => liveSlot().querySelector(".cdrop"), 8000);
    await waitFor(() => liveSlot().querySelectorAll(".cdrow").length >= 1, 8000);
    const before = Array.from(liveSlot().querySelectorAll(".cdrow")).map((n) => n.textContent);
    if (before.some((t) => t.indexOf("архивный разговор DK-726") >= 0)) {
      throw new Error("архивная запись видна до поиска при умолчании кнопки: " + before.join(" | "));
    }
    const find = liveSlot().querySelector(".cdtop input");
    if (!find) throw new Error("поля поиска в шапке списка нет");
    find.value = "архивный разговор";
    find.dispatchEvent(new Event("input", { bubbles: true }));
    const row = await waitFor(() => Array.from(liveSlot().querySelectorAll(".cdrow"))
      .find((n) => n.textContent.indexOf("архивный разговор DK-726") >= 0) || null, 8000);
    result.found = row.textContent;
    result.marked = row.textContent.indexOf("в архиве") >= 0;
    result.ok = true;
  } catch (e) {
    result.error = String((e && e.message) || e);
  }
  try {
    await fetch("/__smoke_result__", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(result),
    });
  } catch (e) {
    // Сеть смоука отвалилась: тест на своей стороне отметит это таймаутом
    // ожидания, и запасного пути сказать больше нечем.
  }
})();
`

// TestDashboardSmokeChatFindArchived поднимает настоящий сервер (newTestEnv),
// убирает один из двух разговоров в архив настоящей ручкой и открывает
// отданную сервером страницу настоящим headless chrome: водитель набирает
// запрос в поле поиска и ждёт, что убранный разговор доедет до выдачи с
// клеймом «в архиве», хотя кнопка архива стоит на умолчании (архивные
// скрыты).
func TestDashboardSmokeChatFindArchived(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: смоук поиска чата пропущен")
	}
	e := newTestEnv(t)
	now := time.Now()
	const archID = "1111aaaa-1111-4111-8111-111111111111"
	const liveID = "2222bbbb-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", archID,
		smokeLine("архивный разговор DK-726", now.Add(-2*time.Hour)), now.Add(-2*time.Hour))
	writeSession(t, e.home, e.proj, "", liveID,
		smokeLine("свежий разговор сегодня DK-726", now.Add(-time.Hour)), now.Add(-time.Hour))

	c := e.loggedClient(t)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+archID+"/archive", `{"archived": true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка разговора в архив перед смоуком: %d %s", resp.StatusCode, body(t, resp))
	}

	base, resultCh := smokeWrap(t, e, chatFindDriverJS)
	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/" + liveID
	res := runChromeSmoke(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("смоук поиска архивного разговора упал: %s", res.Error)
	}
	if !res.Marked {
		t.Errorf("найденная архивная строка не помечена клеймом «в архиве»: %q", res.Found)
	}
	t.Logf("smoke: живой сервер %s, поиск нашёл убранный разговор при умолчании кнопки архива", url)
}

// TestChatFindBeyondWindow сверяет серверную опору поиска мимо окна: ручка
// списка отдаёт окно свежести по умолчанию, а ключом days=0 (тем самым, каким
// static/app.js грузит список при набранном запросе, chatLoadWindow(project,
// st, 0)) отдаёт весь список машины, откуда клиент дальше ищет сам.
// Постановка DK-726 называет этот путь уже рабочим («поиск окна не знает
// нарочно»); тест запирает его регрессией, а не берёт на веру.
func TestChatFindBeyondWindow(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Now()
	const fresh = "aaaa1111-1111-4111-8111-111111111111"
	const stale = "bbbb2222-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", fresh,
		saidLine("сегодняшний разговор DK-726", now.Add(-time.Hour)), now.Add(-time.Hour))
	writeSession(t, e.home, e.proj, "", stale,
		saidLine("разговор старше окна DK-726", now.AddDate(0, 0, -10)), now.AddDate(0, 0, -10))

	windowed, older := chatsWindow(t, e, c, "")
	if chatIn(windowed, stale) {
		t.Fatalf("окно списка неожиданно принесло разговор старше трёх суток: %+v", windowed)
	}
	if !older {
		t.Fatal("окно не сказало, что раньше есть ещё: кнопке «показать раньше» неоткуда взяться")
	}

	all, _ := chatsWindow(t, e, c, "&days=0")
	got := chatOne(all, stale)
	if got == nil {
		t.Fatalf("список без окна не принёс разговор старше трёх суток: %+v", all)
	}
	if !strings.Contains(got.Title, "DK-726") {
		t.Errorf("заголовок разговора старше окна не доехал целиком: %+v", got)
	}
}

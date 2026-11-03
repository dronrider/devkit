package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Крестик панели на телефоне держит память разговора, и вернуться в закрытый
// разговор можно полоской внизу и кнопкой шапки; на широком экране закрытие
// снимает память, и повторное открытие начинается с общего чата доски.
// Поведение живёт в статике, поэтому проверяется стендом на настоящем
// static/app.js с моком дерева (testdata/poc_chatclose.mjs): предмет проверки
// это память вкладки, история и собранная полоска, а не текст исходника.
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticChatCloseKeepsLastOnPhone(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден, стенд закрытия панели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatclose.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("закрытие панели на телефоне: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Полоска возврата остаётся принадлежностью узкого экрана: на широком панель
// стоит сбоку и возвращаться некуда. Раскладку медиазапросов одним узлом не
// проверить, поэтому сторожат сами правила, как у ширин панели (DK-280).
func TestStaticChatBackStripNarrowOnly(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	if !strings.Contains(css, ".cback{display:none") {
		t.Error("полоска возврата не спрятана по умолчанию: она встанет и на широком экране")
	}
	narrow := funcBody(t, css, "@media (max-width:900px){")
	if !strings.Contains(narrow, ".cback:not([hidden]){display:block}") {
		t.Error("полоска возврата не включена внутри узкого экрана: на телефоне её не видно")
	}
	wide := funcBody(t, css, "@media (max-width:1100px){")
	if strings.Contains(wide, "cback") {
		t.Error("полоска возврата включена на средней ширине, где панель лежит поверх доски без полоски возврата")
	}
}

// chatcloseDriverJS это агентская часть сквозного прогона DK-1546: на телефоне
// крестик держит память разговора, и вернуться в закрытый разговор можно
// полоской внизу после перезагрузки страницы. Водитель идёт двумя фазами:
// первая набирает черновик, закрывает панель крестиком и перезагружает
// страницу с живой памятью, вторая возвращается полоской и требует черновик
// на месте. Фаза живёт в sessionStorage и переживает перезагрузку.
const chatcloseDriverJS = `
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
    if (sessionStorage.getItem("dk1546") !== "2") {
      await waitFor(() => {
        const p = document.getElementById("cpanel");
        return p && !p.hidden && liveSlot();
      }, 8000);
      const slot = liveSlot();
      const ta = slot.querySelector("textarea");
      if (!ta) throw new Error("нет поля ввода");
      ta.value = "черновик с телефона";
      ta.dispatchEvent(new Event("input", { bubbles: true }));
      const addr = location.hash.split("/chat/")[1] || "";
      await waitFor(() => (localStorage.getItem("devkit.chat.draft." + addr) || "")
        .includes("черновик с телефона"), 3000);
      const shut = await waitFor(() => slot.querySelector("button.nx"), 8000);
      shut.click();
      await waitFor(() => document.getElementById("cpanel").hidden, 8000);
      await waitFor(() => !location.hash.includes("/chat/"), 8000);
      if (document.getElementById("cback").hidden) {
        throw new Error("полоски возврата нет после крестика");
      }
      sessionStorage.setItem("dk1546", "2");
      location.reload();
      return;
    }
    await waitFor(() => !document.getElementById("cback").hidden, 8000);
    if (location.hash.includes("/chat/")) {
      throw new Error("перезагрузка вернула разговор в адрес: доска спрятана");
    }
    const btn = await waitFor(() => document.getElementById("cback").querySelector("button"), 8000);
    btn.click();
    const slot = await waitFor(() => {
      const p = document.getElementById("cpanel");
      return p && !p.hidden && liveSlot();
    }, 8000);
    const ta = slot.querySelector("textarea");
    if (!ta) throw new Error("после возврата нет поля ввода");
    if (!ta.value.includes("черновик с телефона")) {
      throw new Error("черновик потерян: " + ta.value);
    }
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
  } catch (e) {}
})();
`

// runChromeNarrow открывает страницу смоука в headless chrome окном телефона.
// Обычное окно смоука шириной 1280 точек оставило бы полоску возврата и память
// разговора за порогом узкого экрана, а тут они предмет проверки.
func runChromeNarrow(t *testing.T, chrome, url string, resultCh <-chan smokeResult) smokeResult {
	t.Helper()
	dir := t.TempDir()
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-sandbox",
		"--hide-scrollbars", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--window-size=500,900", url)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("chrome смоук не поднялся: %v", err)
	}
	// Двум фазам водителя нужна перезагрузка страницы, и запас времени выше
	// одиночного прогона.
	var res smokeResult
	var ok bool
	select {
	case res = <-resultCh:
		ok = true
	case <-time.After(30 * time.Second):
	}
	stop()
	_ = cmd.Wait()
	if !ok {
		t.Fatalf("смоук не дождался результата от браузера за 30с\n%s", out.String())
	}
	return res
}

// TestDashboardSmokeChatCloseKeepsReturnOnPhone повторяет путь человека с
// телефона на настоящем поднятом сервере и настоящем headless chrome: черновик
// набирается в поле ввода, панель закрывается крестиком, страница
// перезагружается с живой памятью, и полоска возврата приводит в тот же
// разговор с черновиком на месте. Стенд testdata/poc_chatclose.mjs проверяет
// ту же механику моком дерева, а здесь решают настоящие медиазапросы окна,
// настоящая история браузера и настоящая запись черновика в localStorage.
func TestDashboardSmokeChatCloseKeepsReturnOnPhone(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: смоук закрытия панели пропущен")
	}
	e := newTestEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-xr-7\n")
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	const sid = "draft1"
	writeSession(t, e.home, e.proj, "", sid,
		smokeLine("разговор с черновиком", noon.Add(-time.Hour)), noon.Add(-time.Hour))
	writeBinds(t, e.home, listedBind(e.home, sid, "-", "chat-xr-7"))

	base, resultCh := smokeWrap(t, e, chatcloseDriverJS)
	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/" + sid
	res := runChromeNarrow(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("смоук возврата в разговор упал: %s", res.Error)
	}
	t.Logf("smoke: живой сервер %s, крестик держит память, полоска возвращает разговор с черновиком", url)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// smokeDriverJS это агентская часть проверки DK-656 (замечание ревью): скрипт
// встаёт вторым модулем сразу после настоящего /assets/app.js на реально
// отданной сервером странице и повторяет путь человека через настоящий DOM,
// без обращения к внутренним функциям приложения. Список открывается тем же
// нажатием, что и в браузере (кнопка `.cdpick`), уборка идёт тем же нажатием
// кнопки архива строки (`.cdarch`). Готовый факт едет на сервер настоящим
// fetch: страница держит открытым поток уведомлений (EventSource), и снимок
// разметки по virtual-time-budget с ним никогда не досчитался бы бюджета,
// а обычный wall-clock прогон такого узла не боится.
const smokeDriverJS = `
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
  // Панель держит слот на каждый открытый разговор пулом (chatSlotPut):
  // прежний слот остаётся в #cpin погашенным классом "off", а не снимается,
  // и глобальный querySelector(".cdpick") находил бы его первым, а не
  // показанный. Живой узел это единственный слот без "off".
  function liveSlot() {
    const pin = document.getElementById("cpin");
    if (!pin) return null;
    return Array.from(pin.querySelectorAll(".cslot")).find(
      (n) => !n.className.includes("off")) || null;
  }
  function rowsNow(slot) {
    return Array.from(slot.querySelectorAll(".cdrow")).map((n) => ({
      title: n.querySelector("b") ? n.querySelector("b").textContent : "",
      on: n.classList.contains("on"),
    }));
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
    await waitFor(() => liveSlot().querySelectorAll(".cdrow").length >= 3, 8000);
    result.heads = Array.from(liveSlot().querySelectorAll(".cdday")).map((n) => n.textContent);
    result.rows = rowsNow(liveSlot());
    const beforeB = liveSlot().querySelector(".cdpick b");
    const beforeText = beforeB ? beforeB.textContent : "";
    const curRow = liveSlot().querySelector(".cdrow.on");
    if (!curRow) throw new Error("нет строки текущего разговора");
    const archBtn = curRow.querySelector(".cdarch");
    if (!archBtn) throw new Error("нет кнопки архива у текущей строки");
    archBtn.click();
    // Переключение панели меняет слот целиком (новый ключ пула): ждём, пока
    // в #cpin встанет живой слот с другим текстом шапки, а не пытаемся ловить
    // изменение текста в узле, который сама смена и заменяет.
    await waitFor(() => {
      const s = liveSlot();
      const b = s && s.querySelector(".cdpick b");
      return b && b.textContent !== beforeText;
    }, 8000);
    const afterB = liveSlot().querySelector(".cdpick b");
    result.after = afterB ? afterB.textContent : "";
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

// ringWakeDriverJS это агентская часть сквозного прогона DK-711: кольцо
// хода работ замирало до обновления страницы, потому что опрос /pulse гас
// вместе с chatLive уходящего разговора, а возврат из пула поднимал заново
// только живое панели. Водитель повторяет путь человека на настоящей
// странице: разговор с идущей работой открыт адресом, уход в соседний
// разговор и возврат идут сменой хвоста адреса, как это делает живая
// навигация. Смена снимка работы за спиной происходит ручкой /__smoke_flip__
// того же сервера смоука: транскрипт трогать нельзя, свежий транскрипт
// держал бы состояние «работает» и конец работы был бы нечему заметить.
// План сессии лежит в файле, и переписывание файла это тот же путь, каким
// план меняет сам агент. Спрос кольца виден странице её же учётом ресурсов,
// и возврат обязан принести новый запрос /pulse с именем возвращённой
// сессии, а не только перерисовать дробь.
const ringWakeDriverJS = `
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
  function ring() {
    const s = liveSlot();
    return (s && s.querySelector(".ringwrap")) || null;
  }
  function fraction(w) {
    return Array.from(w.querySelectorAll(".rnum")).map((n) => n.textContent).join("/");
  }
  function pulses() {
    return performance.getEntriesByType("resource")
      .filter((r) => r.name.indexOf("/pulse") >= 0 && r.name.indexOf("sid=work1") >= 0).length;
  }
  const result = { ok: false };
  try {
    const first = await waitFor(() => {
      const w = ring();
      return w && w.classList.contains("r-working") ? w : null;
    }, 10000);
    result.before = fraction(first);
    if (!first.querySelectorAll(".seg.here").length) {
      throw new Error("на кольце нет идущего пункта: " + result.before);
    }
    if (pulses() < 1) throw new Error("опрос кольца не виден странице");
    const base = location.hash.split("/chat/")[0];
    const wasTitle = (liveSlot().querySelector(".cdpick b") || {}).textContent || "";
    location.hash = base + "/chat/neighbor";
    await waitFor(() => {
      const t = liveSlot().querySelector(".cdpick b");
      return t && t.textContent && t.textContent !== wasTitle;
    }, 10000);
    // Таймер уходящего разговора гаснет в момент ухода, и через паузу счёт
    // опросов уже не растёт: дальше сравниваем с неподвижным числом.
    await sleep(300);
    const away = pulses();
    const flip = await fetch("/__smoke_flip__", { method: "POST" });
    if (!flip.ok) throw new Error("смена плана не прошла: " + flip.status);
    location.hash = base + "/chat/work1";
    const back = await waitFor(() => {
      const w = ring();
      return w && fraction(w) === "3/3" ? w : null;
    }, 10000);
    if (back.querySelectorAll(".seg.here").length) {
      throw new Error("идущий пункт остался на кольце закрытой работы");
    }
    result.pulses = pulses() - away;
    if (result.pulses < 1) throw new Error("возврат в разговор не спросил пульс кольца");
    result.after = fraction(back);
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

type smokeRow struct {
	Title string `json:"title"`
	On    bool   `json:"on"`
}

type smokeResult struct {
	OK     bool       `json:"ok"`
	Error  string     `json:"error"`
	Heads  []string   `json:"heads"`
	Rows   []smokeRow `json:"rows"`
	After  string     `json:"after"`
	Before string     `json:"before"`
	Pulses int        `json:"pulses"`
}

// smokeLine это строка транскрипта с заданным временем реплики: своя, а не
// sessionLine, потому что группировке списка нужна метка настоящего
// сегодня-вчера-раньше браузера, а не застывшая дата фикстуры ленты.
func smokeLine(text string, at time.Time) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q,"gitBranch":"main"}`+"\n",
		text, at.UTC().Format("2006-01-02T15:04:05.000Z"))
}

// smokeWrap ставит настоящий index.html и настоящий /assets/app.js за один
// маршрут смоука: /__smoke__ несёт куку входа, добытую настоящей ручкой
// /api/login (та же проверка токена, что и у живого человека), а разметка
// это дословный index.html сервера с одним добавленным модулем-водителем
// сразу после родного <script type="module">. Всё остальное, включая сами
// api-ручки списка и архива, идёт тем же обработчиком s.handler(), что и у
// боевого дашборда: браузер бьёт по-настоящему поднятому серверу, а не по
// файлу на диске. Готовый результат драйвер шлёт отдельной ручкой того же
// сервера, /__smoke_result__, и она же отдаёт его тесту каналом. Водителя
// передаёт тест: у каждой сквозной проверки свой сценарий. Дополнительные
// ручки смоука принимает крючками, они встают в тот же mux до общего
// обработчика.
func smokeWrap(t *testing.T, e *testEnv, driver string, extra ...func(*http.ServeMux)) (string, <-chan smokeResult) {
	t.Helper()
	login, err := http.Post(e.srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token":"`+e.cfg.Token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("вход смоука: %d", login.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range login.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("вход смоука не вернул куку сессии")
	}
	html, err := os.ReadFile(filepath.Join("static", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	const appTag = `<script type="module" src="/assets/app.js"></script>`
	if !strings.Contains(string(html), appTag) {
		t.Fatal("разметка index.html разъехалась: тег app.js не найден")
	}
	injected := strings.Replace(string(html), appTag,
		appTag+"\n<script type=\"module\">"+driver+"</script>", 1)

	resultCh := make(chan smokeResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__smoke__", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, cookie)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(injected))
	})
	mux.HandleFunc("POST /__smoke_result__", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var res smokeResult
		if err := json.NewDecoder(r.Body).Decode(&res); err == nil {
			select {
			case resultCh <- res:
			default:
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for _, hook := range extra {
		hook(mux)
	}
	mux.Handle("/", e.s.handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, resultCh
}

// runChromeSmoke открывает страницу смоука в headless chrome обычным
// временем (без --dump-dom и --virtual-time-budget: страница держит
// EventSource уведомлений, и снимок с бюджетом виртуального времени завис
// бы, ожидая конца потока, которого не будет) и ждёт результат, который
// драйвер сам присылает POST-ом на сервер смоука.
func runChromeSmoke(t *testing.T, chrome, url string, resultCh <-chan smokeResult) smokeResult {
	t.Helper()
	dir := t.TempDir()
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-sandbox",
		"--hide-scrollbars", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--window-size=1280,900", url)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("chrome смоук не поднялся: %v", err)
	}
	var res smokeResult
	var ok bool
	select {
	case res = <-resultCh:
		ok = true
	case <-time.After(25 * time.Second):
	}
	stop()
	_ = cmd.Wait()
	if !ok {
		t.Fatalf("смоук не дождался результата от браузера за 25с\n%s", out.String())
	}
	return res
}

// TestDashboardSmokeChatGroupOrder бьёт по настоящему поднятому серверу
// настоящим headless chrome (замечание ревью DK-656): TestStaticChatListGroups
// и TestStaticChatArchiveCurrentSwitchesPanel читают static/app.js прямо из
// дерева синтетическим DOM и зелены уже до слияния, а этот тест поднимает
// httptest-сервер с реальными обработчиками (newTestEnv), открывает
// отданную им страницу настоящим браузером и повторяет ровно тот сценарий,
// который наблюдал автор задачи: живая сессия трёхдневной давности рядом с
// сегодняшними мёртвыми. Список открывается настоящим нажатием кнопки шапки
// панели, уборка текущего разговора настоящим нажатием кнопки архива строки.
// Панель держит открытый разговор своим слотом в пуле (chatSlotPut), и смена
// слота при переключении на следующий разговор закрывает выпадающий список
// заодно: это работает так и у ручного клика по соседней строке, поэтому
// тест сверяет не то, что список остался открытым, а то, что панель сама
// встала на следующий разговор без повторного захода в список.
func TestDashboardSmokeChatGroupOrder(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: смоук списка чатов пропущен")
	}
	e := newTestEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-live-1\n")

	const (
		live1Title = "живой разговор трёхдневной давности DK-656"
		dead1Title = "мёртвый разговор сегодня DK-656"
		dead2Title = "текущий мёртвый разговор сегодня DK-656"
	)
	now := time.Now()
	writeSession(t, e.home, e.proj, "", "live1",
		smokeLine(live1Title, now.Add(-72*time.Hour)), now.Add(-72*time.Hour))
	writeBinds(t, e.home, listedBind("live1", "-", "chat-live-1"))
	writeSession(t, e.home, e.proj, "", "dead1",
		smokeLine(dead1Title, now.Add(-2*time.Hour)), now.Add(-2*time.Hour))
	writeSession(t, e.home, e.proj, "", "dead2",
		smokeLine(dead2Title, now.Add(-1*time.Hour)), now.Add(-1*time.Hour))

	base, resultCh := smokeWrap(t, e, smokeDriverJS)
	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/dead2"
	res := runChromeSmoke(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("смоук списка чатов упал: %s\nrows=%+v\nheads=%v", res.Error, res.Rows, res.Heads)
	}
	if got := strings.Join(res.Heads, "|"); got != "открытый чат|активные|сегодня" {
		t.Errorf("заголовки групп: %q, ожидал «открытый чат|активные|сегодня»", got)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("строк списка: %d, ожидал 3: %+v", len(res.Rows), res.Rows)
	}
	if !res.Rows[0].On || res.Rows[0].Title != dead2Title {
		t.Errorf("первая строка не открытый чат: %+v", res.Rows[0])
	}
	if res.Rows[1].Title != live1Title {
		t.Errorf("вторая строка не живая трёхдневная сессия: %+v", res.Rows[1])
	}
	if res.Rows[2].Title != dead1Title {
		t.Errorf("третья строка не сегодняшний мёртвый разговор: %+v", res.Rows[2])
	}
	if res.After != dead1Title {
		t.Errorf("панель после уборки текущего чата: %q, ожидал переключение на %q без повторного захода в список",
			res.After, dead1Title)
	}
	t.Logf("smoke: живой сервер %s, группы списка и переключение панели подтверждены настоящим DOM headless-chrome", url)
}

// TestDashboardSmokeRingWakesOnReturn гоняет багу DK-711 сквозным путём:
// работа в разговоре закончилась, а кольцо хода работ крутилось с числом
// оставшихся пунктов до обновления страницы. Стенд testdata/poc_ringwake.mjs
// проверяет ту же механику синтетическим DOM на файле скрипта, а здесь путь
// проходит по-настоящему поднятый сервер и настоящий браузер, и прогон
// осмыслен уже после выката: он спрашивает у живой страницы, а не у дерева
// исходников. Разговор work1 идёт (свежий транскрипт, задача XR-1, план из
// трёх пунктов с одним закрытым), соседний разговор пуст. Водитель уходит в
// соседний разговор адресом, тест за его спиной переписывает файл плана на
// закрытый, водитель возвращается. Правка DK-711 поднимает опрос кольца
// возвратом из пула, поэтому кольцо дорисовывает закрытый план и видно
// новым запросом /pulse; на коде до правки дробь стоит на месте, и прогон
// валится ожиданием.
func TestDashboardSmokeRingWakesOnReturn(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: смоук кольца пропущен")
	}
	e := newTestEnv(t)
	// Доска нужна задачей XR-1 в работе: кольцо спрашивает пульс задачи,
	// а не одной сессии.
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", pulseBoardJSON))
	writeScript(t, e.bin, "tmux", "exit 1")

	now := time.Now()
	writeSession(t, e.home, e.proj, "", "work1",
		pulseTranscript(now.Add(-20*time.Second), "Bash", "go test ./tools/..."),
		now.Add(-20*time.Second))
	writeBinds(t, e.home,
		bindRecord(now.Add(-time.Minute).Format("2006-01-02T15:04:05"), "work1", "XR-1", "заказ"))
	writeSession(t, e.home, e.proj, "", "neighbor",
		smokeLine("соседний разговор без работы", now.Add(-3*time.Hour)), now.Add(-3*time.Hour))

	if err := os.MkdirAll(planDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	open := `[{"text":"разобрать механизм кольца","state":"completed"},` +
		`{"text":"написать стенд возврата","state":"in_progress"},` +
		`{"text":"выехать смоуком","state":"pending"}]`
	if err := os.WriteFile(planPath(e.home, "work1"), []byte(open), 0o644); err != nil {
		t.Fatal(err)
	}

	flip := func(mux *http.ServeMux) {
		mux.HandleFunc("POST /__smoke_flip__", func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			closed := `[{"text":"разобрать механизм кольца","state":"completed"},` +
				`{"text":"написать стенд возврата","state":"completed"},` +
				`{"text":"выехать смоуком","state":"completed"}]`
			if err := os.WriteFile(planPath(e.home, "work1"), []byte(closed), 0o644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	base, resultCh := smokeWrap(t, e, ringWakeDriverJS, flip)
	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/work1"
	res := runChromeSmoke(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("смоук кольца упал: %s (до ухода %q, опросов возврата %d)",
			res.Error, res.Before, res.Pulses)
	}
	if res.Before != "1/3" || res.After != "3/3" {
		t.Errorf("дробь кольца: до ухода %q, после возврата %q, ждал 1/3 и 3/3",
			res.Before, res.After)
	}
	if res.Pulses < 1 {
		t.Errorf("опросов пульса на возврате %d, возврат обязан спросить пульс", res.Pulses)
	}
	t.Logf("smoke: живой сервер %s, кольцо дорисовало закрытый план после возврата, спрос виден (%d новых опросов)",
		url, res.Pulses)
}

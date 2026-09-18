package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Замер молчания скрытой вкладки настоящим браузером (DK-986). Разбор
// исходника говорит только о том, что выключатель видимости в коде стоит, а
// человек жаловался на греющуюся машину, и мера тут это число запросов к
// серверу. Поэтому страницу открывает настоящий headless chrome по
// по-настоящему поднятому серверу, а считает запросы сам сервер.
//
// Окно замера меряется числом запросов, а не миллисекундами: тесты дашборда
// гоняются под нагрузкой и по времени плавают (DK-811). Открытая вкладка
// набирает четыре запроса за столько, за сколько наберёт, и ровно столько же
// времени вкладка потом лежит в фоне.

// quietDriverJS уводит вкладку в фон тем же признаком, каким это делает
// браузер (`document.visibilityState` и `document.hidden` плюс событие
// `visibilitychange`), и сам считает окна по счётчику сервера. Внутренних
// функций экрана драйвер не трогает: предмет замера это запросы, которые
// увидит сервер человека.
const quietDriverJS = `
const sleep = (ms) => new Promise((ok) => setTimeout(ok, ms));
const count = async () => {
  const r = await fetch("/__quiet_count__");
  const b = await r.json();
  return b.n;
};
const showTab = (on) => {
  Object.defineProperty(document, "visibilityState",
    { configurable: true, get: () => (on ? "visible" : "hidden") });
  Object.defineProperty(document, "hidden", { configurable: true, get: () => !on });
  document.dispatchEvent(new Event("visibilitychange"));
};
(async () => {
  const res = { ok: false, error: "", visible: 0, hidden: 0, catchup: 0, ms: 0 };
  try {
    // Страница поднялась, панель разговора открылась, первые запросы прошли.
    await sleep(2000);
    const from = await count();
    const t0 = Date.now();
    while (Date.now() - t0 < 25000) {
      res.visible = (await count()) - from;
      if (res.visible >= 4) break;
      await sleep(250);
    }
    res.ms = Date.now() - t0;
    if (res.visible < 4) {
      throw new Error("открытая вкладка набрала " + res.visible + " запросов за " + res.ms + " мс");
    }
    const before = await count();
    showTab(false);
    await sleep(res.ms);
    res.hidden = (await count()) - before;
    const back = await count();
    showTab(true);
    for (let i = 0; i < 24; i += 1) {
      await sleep(250);
      res.catchup = (await count()) - back;
      if (res.catchup) break;
    }
    res.ok = true;
  } catch (e) {
    res.error = String((e && e.message) || e);
  }
  try {
    await fetch("/__quiet_result__", { method: "POST",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(res) });
  } catch (e) {
    // Связь оборвалась: тест на своей стороне отметит это сроком ожидания.
  }
})();
`

type quietResult struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Visible int    `json:"visible"`
	Hidden  int    `json:"hidden"`
	Catchup int    `json:"catchup"`
	Ms      int    `json:"ms"`
}

// quietCounter считает запросы к api дашборда. Поток уведомлений в счёт не
// идёт: его держит открытым сама страница, и переподключение потока это не
// опрос.
type quietCounter struct {
	mu sync.Mutex
	n  int
}

func (c *quietCounter) add() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *quietCounter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// quietChrome ищет браузер для этого замера. Сверх общего findChrome сюда
// годится и обычный Chrome из Applications: стендам ширины он не подходит
// (окно уже пятисот точек macOS ему не открывает, см. sameWindow), а замеру
// запросов ширина безразлична. В общий поиск он не идёт нарочно: там его
// подхватили бы полтора десятка стендов вида, и прогон пакета вырос бы с трёх
// минут до десяти.
func quietChrome() string {
	if path := findChrome(); path != "" {
		return path
	}
	for _, path := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
	} {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	return ""
}

// TestDashboardSmokeHiddenTabQuiet: вкладка в фоне сервер не спрашивает, а
// возврат к ней приносит догон.
func TestDashboardSmokeHiddenTabQuiet(t *testing.T) {
	chrome := quietChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер молчания скрытой вкладки пропущен")
	}
	e := newTestEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-live-1\n")

	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	writeSession(t, e.home, e.proj, "", "live1",
		smokeLine("живой разговор DK-986", noon.Add(-2*time.Hour)), noon.Add(-2*time.Hour))
	writeBinds(t, e.home, listedBind(e.home, "live1", "-", "chat-live-1"))

	counter := &quietCounter{}
	resultCh := make(chan quietResult, 1)
	base, _ := smokeWrap(t, e, quietDriverJS, func(mux *http.ServeMux) {
		// Путь «GET /api/» точнее общего «/», поэтому запросы экрана идут
		// сначала сюда, а обслуживает их тот же обработчик дашборда.
		inner := e.s.handler()
		mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/notifications") {
				counter.add()
			}
			inner.ServeHTTP(w, r)
		})
		mux.HandleFunc("GET /__quiet_count__", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]int{"n": counter.get()})
		})
		mux.HandleFunc("POST /__quiet_result__", func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			var res quietResult
			if err := json.NewDecoder(r.Body).Decode(&res); err == nil {
				select {
				case resultCh <- res:
				default:
				}
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})

	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/live1"
	res := runChromeQuiet(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("замер скрытой вкладки упал: %s (открытая %d запросов за %d мс, фон %d, догон %d)",
			res.Error, res.Visible, res.Ms, res.Hidden, res.Catchup)
	}
	// Один запрос в фоне это ответ, ушедший из браузера до самой отметки, и
	// сторожить его нечем: отметка и запрос летят разными соединениями.
	if res.Hidden > 1 {
		t.Errorf("вкладка в фоне спросила сервер %d раз за то же окно, в котором открытая спросила %d: "+
			"опрос идёт мимо выключателя видимости", res.Hidden, res.Visible)
	}
	if res.Catchup < 1 {
		t.Errorf("возврат к вкладке не догнал пропущенное: запросов после возврата %d", res.Catchup)
	}
	t.Logf("smoke: открытая вкладка %d запросов за %d мс, в фоне %d, догон %d (%s)",
		res.Visible, res.Ms, res.Hidden, res.Catchup, url)
}

// runChromeQuiet держит браузер дольше смоука списка чатов: замер сидит два
// окна подряд, а длину окна назначает сама открытая вкладка.
func runChromeQuiet(t *testing.T, chrome, url string, resultCh <-chan quietResult) quietResult {
	t.Helper()
	dir := t.TempDir()
	ctx, stop := context.WithTimeout(context.Background(), 120*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-sandbox",
		"--hide-scrollbars", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--window-size=1280,900", url)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("chrome замера не поднялся: %v", err)
	}
	var res quietResult
	var ok bool
	select {
	case res = <-resultCh:
		ok = true
	case <-time.After(100 * time.Second):
	}
	stop()
	_ = cmd.Wait()
	if !ok {
		t.Fatalf("замер не дождался результата от браузера за 100с\n%s", out.String())
	}
	return res
}

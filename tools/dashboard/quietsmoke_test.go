package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// Круг видимости: номер поднимается на каждой смене видимости, и запрос
// экрана уезжает на сервер с номером того круга, в котором его завели
// (DK-1184). Времени прихода на сервер тут верить нельзя: под нагрузкой
// запрос уходит из браузера до отметки ухода в фон, а доезжает после неё.
let turn = 0;
const rawFetch = window.fetch.bind(window);
window.fetch = (input, init) => {
  const url = String((input && input.url) || input || "");
  if (!url.startsWith("/api/")) return rawFetch(input, init);
  const next = Object.assign({}, init || {});
  const head = new Headers((init && init.headers) || {});
  head.set("X-Quiet-Turn", String(turn));
  next.headers = head;
  return rawFetch(input, next);
};
const showTab = (on) => {
  turn += 1;
  Object.defineProperty(document, "visibilityState",
    { configurable: true, get: () => (on ? "visible" : "hidden") });
  Object.defineProperty(document, "hidden", { configurable: true, get: () => !on });
  document.dispatchEvent(new Event("visibilitychange"));
};
(async () => {
  const res = { ok: false, error: "", visible: 0, hidden: 0, catchup: 0, after: 0, ms: 0 };
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
    // Отметка возврата: по ней сервер отделит догон от всего, что было раньше.
    await fetch("/__quiet_back__", { method: "POST" });
    showTab(true);
    for (let i = 0; i < 24; i += 1) {
      await sleep(250);
      res.catchup = (await count()) - back;
      if (res.catchup) break;
    }
    // Лесенка догона длиной около секунды: ждём её целиком, иначе сервер
    // увидит одну первую ступеньку и разлив мерить будет нечем.
    await sleep(1300);
    res.catchup = (await count()) - back;

    // Второй уход в фон посреди лесенки. Человек вернулся на вкладку и тут же
    // ушёл снова: заведённые ступеньки обязаны погаснуть вместе с кругами.
    showTab(false);
    await sleep(600);
    const second = await count();
    showTab(true);
    // Лесенка пошла, но до первой ступеньки вкладка снова уходит в фон.
    await sleep(100);
    showTab(false);
    await fetch("/__quiet_gone__?turn=" + turn, { method: "POST" });
    await sleep(2000);
    res.after = (await count()) - second;
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
	After   int    `json:"after"`
	Ms      int    `json:"ms"`
}

// quietCounter считает запросы к api дашборда, помнит, когда каждый пришёл, и
// помнит круг видимости, который назвала сама страница в момент отправки.
// Поток уведомлений в счёт не идёт: его держит открытым сама страница, и
// переподключение потока это не опрос.
type quietCounter struct {
	mu   sync.Mutex
	n    int
	at   []time.Time
	path []string
	turn []int
	back time.Time
	gone int
}

func (c *quietCounter) add(path string, turn int) {
	c.mu.Lock()
	c.n++
	c.at = append(c.at, time.Now())
	c.path = append(c.path, path)
	c.turn = append(c.turn, turn)
	c.mu.Unlock()
}

// quietTurn разбирает номер круга видимости. Запрос без номера это запрос,
// ушедший раньше, чем драйвер подменил fetch, то есть заведомо в первом,
// открытом круге.
func quietTurn(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// caught называет ручки, которые ответили в окне догона: по ним видно, какие
// опросы в лесенке вообще есть.
func (c *quietCounter) caught(window time.Duration) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for i, at := range c.at {
		if at.After(c.back) && at.Sub(c.back) <= window {
			out = append(out, c.path[i])
		}
	}
	return out
}

func (c *quietCounter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// mark запоминает миг возврата к вкладке.
func (c *quietCounter) mark() {
	c.mu.Lock()
	c.back = time.Now()
	c.mu.Unlock()
}

// spread раскладывает догон по окнам длиной size: сколько запросов пришло в
// каждом окне, считая от первого запроса догона. Залп занимает одно окно,
// лесенка несколько, и судит тест по числу занятых окон, а не по одной паре
// меток (замечание ревью): под нагрузкой пара схлопывается, а картина занятых
// окон держится.
func (c *quietCounter) spread(window, size time.Duration) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var got []time.Time
	for _, at := range c.at {
		if at.After(c.back) && at.Sub(c.back) <= window {
			got = append(got, at)
		}
	}
	if len(got) == 0 {
		return nil
	}
	out := make([]int, int(window/size)+1)
	for _, at := range got {
		box := int(at.Sub(got[0]) / size)
		if box >= len(out) {
			box = len(out) - 1
		}
		out[box]++
	}
	return out
}

// busyBoxes это число занятых окон, а span длина догона от первого запроса до
// последнего.
func busyBoxes(boxes []int, size time.Duration) (int, time.Duration) {
	busy := 0
	last := 0
	for i, n := range boxes {
		if n == 0 {
			continue
		}
		busy++
		last = i
	}
	return busy, time.Duration(last) * size
}

// mind запоминает круг видимости второго ухода в фон: его называет сама
// страница, и круга без номера тут не бывает, первая же смена видимости даёт
// единицу.
func (c *quietCounter) mind(turn int) {
	c.mu.Lock()
	c.gone = turn
	c.mu.Unlock()
}

// goneTurn отдаёт круг второго ухода в фон. Нуль значит, что отметки от
// страницы не пришло вовсе и мерить нечем.
func (c *quietCounter) goneTurn() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gone
}

// stamped называет наибольший круг видимости среди запросов экрана. Нуль тут
// значит, что драйвер запросы не разметил вовсе, и счёт после ухода в фон
// ничего не проверяет: неразмеченный запрос числится в первом круге и в счёт не
// идёт никогда.
func (c *quietCounter) stamped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	top := 0
	for _, turn := range c.turn {
		if turn > top {
			top = turn
		}
	}
	return top
}

// afterGone считает запросы, которые страница завела после второго ухода в
// фон. Мерка это круг видимости из самого запроса, а не время его прихода:
// круг ставит страница в момент отправки, и задержка доставки под нагрузкой
// вердикт не двигает (DK-1184). Ступенька лесенки, пережившая уход, видна тут
// и только тут.
func (c *quietCounter) afterGone() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gone == 0 {
		return 0
	}
	n := 0
	for _, turn := range c.turn {
		if turn >= c.gone {
			n++
		}
	}
	return n
}

// TestQuietCounterCountsPageTurnNotArrival держит мерку счётчика: запрос,
// заведённый страницей до ухода в фон, в счёт после ухода не идёт, сколько бы
// он ни летел до сервера. Счёт по времени прихода тут краснел под нагрузкой на
// готовых ветках (DK-1184).
func TestQuietCounterCountsPageTurnNotArrival(t *testing.T) {
	c := &quietCounter{}
	// Круг 2 это возврат к вкладке, круг 3 второй уход в фон.
	c.add("/api/board", 2)
	c.mind(quietTurn("3"))
	// Запоздавший запрос догона: пришёл после отметки, а заведён до неё.
	c.add("/api/chat/live1", 2)
	// Ступенька, пережившая уход: её страница завела уже в фоне.
	c.add("/api/board", 3)
	if got := c.afterGone(); got != 1 {
		t.Errorf("после ухода в фон сосчитано %d запросов, а завёлся в фоне один", got)
	}
	// Без отметки от страницы мерить нечем, и счёт молчит вместо того, чтобы
	// зачесть фону весь прогон.
	empty := &quietCounter{}
	empty.add("/api/board", 1)
	if got := empty.goneTurn(); got != 0 {
		t.Errorf("круг ухода без отметки страницы %d, а ждали нуль", got)
	}
	if got := empty.afterGone(); got != 0 {
		t.Errorf("без отметки ухода сосчитано %d запросов, а ждали нуль", got)
	}
	// Верхний круг отделяет размеченные запросы от неразмеченных: драйвер без
	// подмены fetch оставил бы нули, и счёт после ухода прошёл бы пустым.
	if got := c.stamped(); got != 3 {
		t.Errorf("верхний круг видимости %d, а размечали до третьего", got)
	}
	if got := (&quietCounter{}).stamped(); got != 0 {
		t.Errorf("верхний круг пустого счётчика %d, а ждали нуль", got)
	}
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
				counter.add(r.URL.Path, quietTurn(r.Header.Get("X-Quiet-Turn")))
			}
			inner.ServeHTTP(w, r)
		})
		mux.HandleFunc("GET /__quiet_count__", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]int{"n": counter.get()})
		})
		mux.HandleFunc("POST /__quiet_back__", func(w http.ResponseWriter, r *http.Request) {
			counter.mark()
			w.WriteHeader(http.StatusNoContent)
		})
		mux.HandleFunc("POST /__quiet_gone__", func(w http.ResponseWriter, r *http.Request) {
			counter.mind(quietTurn(r.URL.Query().Get("turn")))
			w.WriteHeader(http.StatusNoContent)
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
	// Догон идёт лесенкой, а не залпом (замечание ревью). Судит тут разброс по
	// окнам в полступеньки, а не одна пара меток: залп занимает одно окно,
	// лесенка несколько.
	//
	// Сколько ступенек на этом стенде, сказано числом. Опросов вне первого ряда
	// тут три: пульс кольца, вопрос клиента и сторожок молчащего потока
	// событий. Запрос шлют два первых, сторожок при живом потоке молчит, и
	// разброс держится на двух ступеньках. Порог занятых окон поэтому взят
	// двойкой: одна ступенька вправе схлопнуться с первым рядом (заминка
	// event loop под нагрузкой), и тест это переживёт, а залп даёт одно окно
	// на все десять запросов и краснеет.
	const (
		quietWindow = 1500 * time.Millisecond
		quietBox    = 75 * time.Millisecond
		quietBusy   = 2
	)
	boxes := counter.spread(quietWindow, quietBox)
	busy, span := busyBoxes(boxes, quietBox)
	if busy < quietBusy {
		t.Errorf("догон пришёл залпом: %v запросов по окнам в %v, занято окон %d при пороге %d, "+
			"длина догона %v. Опросы будятся разом, и возвращённая вкладка получает всплеск запросов",
			boxes, quietBox, busy, quietBusy, span)
	}
	// Второй уход в фон посреди лесенки. Недобуженная ступенька обязана
	// погаснуть вместе с кругами, и запросов, заведённых после ухода, быть не
	// должно ни одного. Скидки на запоздавший ответ тут больше нет: круг
	// видимости у запроса ставит сама страница, и ушедший до отметки запрос
	// несёт прежний круг, когда бы он ни доехал.
	if counter.goneTurn() == 0 {
		t.Fatalf("страница не назвала круг второго ухода в фон: счёт после ухода мерить нечем")
	}
	if counter.stamped() == 0 {
		t.Fatalf("драйвер не разметил запросы экрана кругом видимости: счёт после ухода в фон прошёл бы пустым")
	}
	if gone := counter.afterGone(); gone > 0 {
		t.Errorf("повторный уход в фон не снял ступеньки лесенки: после него страница завела %d запросов",
			gone)
	}
	t.Logf("smoke: открытая вкладка %d запросов за %d мс, в фоне %d, догон %d по окнам %v "+
		"(занято %d, длина %v), после второго ухода %d при круге ухода %d и верхнем круге %d (%s)",
		res.Visible, res.Ms, res.Hidden, res.Catchup, boxes, busy, span, counter.afterGone(),
		counter.goneTurn(), counter.stamped(), url)
	t.Logf("smoke: ручки догона %v", counter.caught(quietWindow))
}

// runChromeQuiet держит браузер дольше смоука списка чатов: замер сидит два
// окна подряд, а длину окна назначает сама открытая вкладка.
func runChromeQuiet(t *testing.T, chrome, url string, resultCh <-chan quietResult) quietResult {
	t.Helper()
	dir := t.TempDir()
	ctx, stop := context.WithTimeout(context.Background(), 240*time.Second)
	defer stop()
	// Обычный Chrome на свежем профиле лезет в систему, и человек у экрана
	// получает чужие диалоги. Связка ключей macOS спрашивает доступ к «Chrome
	// Safe Storage», а под временным HOME обкатки связки входа нет вовсе, и
	// вопрос висит поверх работы. Апдейтер браузера тем временем уходит в сеть,
	// и первый запуск с чистым домом доходил до страницы через полторы минуты.
	// Флаги ниже оставляют браузеру одну работу, открыть страницу стенда:
	// хранилище паролей поддельное, в связку ключей замер не ходит.
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-sandbox",
		"--hide-scrollbars", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--use-mock-keychain", "--password-store=basic",
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "--disable-extensions",
		"--disable-default-apps", "--metrics-recording-only", "--mute-audio",
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
	// Срок с большим запасом: сам замер укладывается в пятнадцать секунд, а
	// первый запуск обычного Chrome на чистом доме сначала поднимает свой
	// апдейтер и до страницы доходит через полторы минуты.
	case <-time.After(200 * time.Second):
	}
	stop()
	_ = cmd.Wait()
	if !ok {
		t.Fatalf("замер не дождался результата от браузера за 200с\n%s", out.String())
	}
	return res
}

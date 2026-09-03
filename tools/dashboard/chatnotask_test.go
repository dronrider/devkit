package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Кончившийся разговор без задачи (DK-727). Решение 6 LLD DK-430 держало
// четвёртую причину гашения ввода: разговор кончился, а задачи за ним нет,
// реплику взять некому. Код тем временем уехал: ручка реплики поднимает
// `claude --resume` и без задачи, а панель зовёт это дорогой `resume` с живым
// полем ввода. Разведка 2026-09-02 нашла расхождение чтением, живая проверка
// 2026-09-03 подтвердила его прогоном, и решение пересмотрено в самом LLD.
//
// Тесты ниже держат пересмотренное обещание с двух сторон. Ручка обязана
// увезти реплику резюмом и без задачи, а панель обязана назвать причину
// гашения там, где гасит, и не гасить нигде больше.

// endedChat кладёт кончившийся разговор: транскрипт на диске есть, запись
// реестра называет tmux-сессию, а живых сессий у tmux нет вовсе. Задача
// берётся из записи реестра, прочерк значит разговор без задачи.
func endedChat(t *testing.T, e *testEnv, sid, task string, tmux string) {
	t.Helper()
	writeSession(t, e.home, e.proj, "", sid,
		saidLine("разговор кончился вчера", time.Now().Add(-30*time.Hour)),
		time.Now().Add(-30*time.Hour))
	writeBinds(t, e.home, "2026-09-01T10:00:00 сессия "+sid+" задача "+task+
		" проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux "+tmux+"\n")
}

// Реплика в кончившийся разговор без задачи уезжает резюмом той же сессии:
// ручка отвечает дорогой `resume`, поднимает клиента с `--resume` на тот же
// адрес и дописывает реестр записью подъёма. Отказать тут было бы честно
// только при обещании гашения, а его больше нет: истории у разговора хватает,
// и продолжает её тот же транскрипт.
func TestChatSayResumeWithoutTask(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-2222-4222-8222-333333333333"
	endedChat(t, e, sid, "-", "chat-7")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "Ответь одним словом"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в кончившийся разговор без задачи не прошла: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"resume"`) {
		t.Fatalf("дорога реплики не резюм: %s", got)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "new-session -d -s chat-1 ") {
		t.Errorf("продолжение поднято не своей сессией: %s", said)
	}
	if !strings.Contains(said, "--resume '"+sid+"'") {
		t.Errorf("клиент поднят без продолжения того же разговора: %s", said)
	}
	if !strings.Contains(said, "Ответь одним словом") {
		t.Errorf("реплика человека не уехала вводной резюма: %s", said)
	}
	// Задачи у разговора нет, и выдумывать её подъёму нельзя: с DEVKIT_TASK
	// хук старта записал бы чужую строку доски за разговором свободного чата.
	if strings.Contains(said, "DEVKIT_TASK") {
		t.Errorf("подъём разговора без задачи назвал задачу: %s", said)
	}
	if strings.Count(said, "send-keys") != 0 {
		t.Errorf("реплика ушла клавишами в мёртвую сессию: %s", said)
	}
	binds := readFile(t, filepath.Join(e.home, ".devkit", "sessions.log"))
	if !strings.Contains(binds, "сессия "+sid+" задача - ") ||
		!strings.Contains(binds, "повод резюм чата tmux chat-1") {
		t.Errorf("подъём не записан реестром на тот же адрес: %s", binds)
	}
}

// Обратный случай той же дороги: у кончившегося разговора есть задача. Резюм
// тут тот же самый, а разница в заказе: имя сессии идёт от задачи, и её же
// имя едет в окружении, иначе хук старта не свяжет разговор со строкой доски.
func TestChatSayResumeWithTask(t *testing.T) {
	e, c := chatEnv(t)
	sid := "bbbb2222-3333-4333-8333-444444444444"
	endedChat(t, e, sid, "XR-4", "chat-XR-4-1")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "Ответь одним словом"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в кончившийся разговор задачи не прошла: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"resume"`) {
		t.Fatalf("дорога реплики не резюм: %s", got)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "new-session -d -s chat-XR-4-1 ") {
		t.Errorf("продолжение разговора задачи поднято не её именем: %s", said)
	}
	if !strings.Contains(said, "DEVKIT_TASK='XR-4'") {
		t.Errorf("подъём разговора задачи не назвал её в окружении: %s", said)
	}
}

// Причины гашения ввода поимённо (пересмотренное решение 6 LLD DK-430). Стенд
// перебирает состояния панели одно за другим и сверяет с каждым, заперто поле
// или открыто: гасят два состояния, подъём сессии и адрес без транскрипта, а
// кончившийся разговор, с задачей и без неё, поле держит живым. Возврат
// четвёртой причины ловится тут, а не на приёмке. Без node шаг пропускается:
// узел стенда, а не рабочей части (стенд testdata/poc_chatnotask.mjs).
func TestStaticChatWayEndedNoTask(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд причин гашения ввода пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatnotask.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("причины гашения ввода: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// smokeSayWord это слово ответа агента, которого водитель ждёт в ленте.
// Пишет его фиктивный клиент, поднятый резюмом, и другого пути в ленту у него
// нет: ответ, приехавший этим словом, приехал ответом на реплику.
const smokeSayWord = "готово по DK-727"

// smokeSayDriverJS это водитель сквозной проверки: он повторяет путь человека
// по настоящей странице. Дожидается панели открытого разговора, убеждается,
// что поле ввода не заперто, печатает в него реплику, жмёт настоящую кнопку
// отправки и ждёт в ленте ответа агента. Ответ приезжает потоком транскрипта,
// то есть тем же путём, каким его видит человек.
const smokeSayDriverJS = `
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
  const WORD = %q;
  const result = { ok: false };
  try {
    const slot = await waitFor(() => {
      const p = document.getElementById("cpanel");
      return p && !p.hidden && liveSlot();
    }, 8000);
    const ta = await waitFor(() => slot.querySelector("textarea.csay"), 8000);
    result.before = ta.disabled ? "заперто" : "открыто";
    if (ta.disabled) throw new Error("поле ввода заперто: " + ta.placeholder);
    ta.value = "Ответь одним словом";
    ta.dispatchEvent(new Event("input", { bubbles: true }));
    const send = await waitFor(() => Array.from(slot.querySelectorAll("button")).find(
      (b) => b.textContent.trim() === "Отправить"), 8000);
    if (send.disabled) throw new Error("кнопка отправки заперта");
    send.click();
    // Ответ агента приезжает в ленту потоком транскрипта: ждём его текст в
    // разметке, а не ответа ручки. Реплика доехала только тогда, когда
    // человек видит ответ.
    result.found = await waitFor(() => {
      const feed = liveSlot() && liveSlot().querySelector(".chatfeed");
      const said = feed ? feed.textContent : "";
      const at = said.indexOf(WORD);
      return at >= 0 ? said.slice(at, at + 40) : null;
    }, 20000);
    result.after = liveSlot().querySelector("textarea.csay").disabled ? "заперто" : "открыто";
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
    // Сеть смоука отвалилась: тест на своей стороне отметит это таймаутом.
  }
})();
`

// TestDashboardSmokeSayEndedNoTask бьёт по настоящему поднятому серверу
// настоящим headless chrome. Стенд панели и тест ручки зелены уже до слияния и
// читают дерево исходников, а тут путь идёт целиком: страница отдана сервером,
// поле ввода настоящее, реплика уезжает ручкой реплики, фиктивный tmux
// исполняет собранную дашбордом команду, а фиктивный клиент дописывает ответ в
// тот же транскрипт. Ответ агента приходит в ту же ленту потоком, и это и есть
// обещание DK-727, которое иначе проверялось бы глазами на приёмке.
func TestDashboardSmokeSayEndedNoTask(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: смоук реплики в кончившийся разговор пропущен")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh не найден: исполнять команду подъёма нечем")
	}
	e := newTestEnv(t)
	sid := "cccc3333-4444-4444-8444-555555555555"
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	path := writeSession(t, e.home, e.proj, "", sid,
		smokeLine("разговор без задачи кончился вчера", noon.Add(-26*time.Hour)),
		noon.Add(-26*time.Hour))
	writeBinds(t, e.home, "2026-09-01T10:00:00 сессия "+sid+" задача - проект demo дерево "+
		e.proj+" транскрипт "+path+" источник заказ повод startup tmux chat-7\n")

	// Фиктивный клиент дописывает ответ агента в транскрипт того же разговора:
	// ровно это делает `claude --resume`, и ровно этого ждёт лента. Ответ идёт
	// только на резюм: тем же именем сервер зовёт клиента за заголовком
	// разговора (`claude -p`), и без разбора аргументов ответ появился бы в
	// ленте сам собой, без всякой реплики.
	writeScript(t, e.bin, "claude", fmt.Sprintf(`case "$*" in
*--resume*)
  printf '%%s\n' '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]},"timestamp":"%s","gitBranch":"main"}' >> %q
  ;;
esac
exit 0`, smokeSayWord, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), path))
	// Фиктивный tmux не только записывает вызов, но и исполняет команду
	// подъёма: без этого сквозной путь обрывался бы на границе процесса, и
	// ответа агента в ленте не появилось бы ни при какой правке.
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", fmt.Sprintf(`echo "$@" >> %q
case "$1" in
ls) exit 1;;
new-session)
  for a in "$@"; do last="$a"; done
  sh -c "$last" >> %q 2>&1
  ;;
esac
exit 0`, tmuxLog, tmuxLog+".out"))

	base, resultCh := smokeWrap(t, e, fmt.Sprintf(smokeSayDriverJS, smokeSayWord))
	url := base + "/__smoke__#" + filepath.Base(e.proj) + "/chat/" + sid
	res := runChromeSmoke(t, chrome, url, resultCh)
	if !res.OK {
		t.Fatalf("смоук реплики в кончившийся разговор упал: %s (поле %s)\n%s",
			res.Error, res.Before, readFile(t, tmuxLog))
	}
	if res.Before != "открыто" {
		t.Errorf("поле ввода кончившегося разговора без задачи: %s", res.Before)
	}
	if !strings.Contains(res.Found, smokeSayWord) {
		t.Errorf("ответа агента в ленте нет: %q", res.Found)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "--resume '"+sid+"'") {
		t.Errorf("подъём пошёл не резюмом того же разговора: %s", said)
	}
	t.Logf("smoke: живой сервер %s, реплика доехала резюмом и ответ агента пришёл в ту же ленту", url)
}

// Зонд браузерного стенда чата цели (DK-938, TestBrowserGoalChatOpensLoop).
//
// Страницу отдаёт настоящий сервер дашборда на синтетическом доме: цель XR-100
// идёт циклом, её виток живой и скрытый, а рядом лежит вчерашний груминг той же
// задачи, который кнопка открывала вместо витка. Зонд жмёт кнопку чата и
// снимает, что показала панель: заголовок, красную остановку или зелёную точку
// работы, плашку над полем ввода. Где жать, говорит адрес открытия: `#demo`
// это строка доски, `#demo/XR-100` это форма цели. Каждый заход стенд начинает
// с чистой страницы, как человек, пришедший по ссылке. Итог уходит в заголовок
// страницы, его и читает тест из --dump-dom.
(function () {
  const want = "XR-100";
  const form = location.hash === "#demo/" + want;
  const out = { where: form ? "form" : "row", look: null, err: "" };
  const sleep = (ms) => new Promise((ok) => setTimeout(ok, ms));
  const until = async (fn, what) => {
    for (let i = 0; i < 250; i++) {
      const v = fn();
      if (v) return v;
      await sleep(100);
    }
    throw new Error("не дождался " + what);
  };
  const shown = (n) => Boolean(n && n.getClientRects().length > 0);
  const chatBtn = () => Array.from(document.querySelectorAll(
    'button[aria-label="Чат по задаче ' + want + '"]')).filter(shown).pop();
  // Зелёный это цвет работы из переменной стилей, а не число в зонде: число
  // разошлось бы со стилями молча.
  const green = () => {
    const probe = document.createElement("span");
    probe.style.background = "var(--run)";
    document.body.append(probe);
    const color = getComputedStyle(probe).backgroundColor;
    probe.remove();
    return color;
  };
  // Последнее, что зонд видел в панели: на сбое оно едет в итог, иначе
  // причину пришлось бы угадывать.
  let seen = "";
  const panel = () => {
    const p = document.getElementById("cpanel");
    if (!p || p.hidden) return null;
    const b = p.querySelector(".cdpick b");
    const title = b ? b.textContent.trim() : "";
    const row = p.querySelector(".busyrow");
    const busy = shown(row);
    seen = location.hash + " шапка «" + title + "» индикатор " + (busy ? "есть" : "нет");
    if (!title || !busy) return null;
    const dot = row.querySelector(".dot");
    const note = p.querySelector('[data-pkey="chat-note"]');
    return {
      title,
      busy,
      stop: row.classList.contains("stop"),
      green: Boolean(dot && getComputedStyle(dot).backgroundColor === green()),
      note: note ? note.textContent.trim() : "",
    };
  };
  (async () => {
    try {
      if (form) await until(() => shown(document.querySelector(".tpen")), "формы цели");
      const b = await until(chatBtn, "кнопки чата");
      b.click();
      out.look = await until(panel, "панели чата после кнопки");
    } catch (err) {
      out.err = String(err && err.message ? err.message : err) + " (видел: " + seen + ")";
    }
    document.title = "DK938:" + encodeURIComponent(JSON.stringify(out));
  })();
})();

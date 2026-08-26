// Стенд пула панелей разговоров (требование пользователя: переключение чата
// должно быть мгновенным). Открытая панель не сносится, а прячется, и возврат
// в неё показывает готовый узел тем же ходом, без похода в сеть.
//
// Предмет проверки: возврат виден сразу и без запросов, спрятанный слот стоит
// content-visibility (а не снятым узлом), давние слоты вытесняются по счёту, и
// спрятанный разговор никто не опрашивает.
//
// Зовётся: node testdata/poc_pool.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

const app = appPathArg();
const css = readFileSync(join(dirname(app), "style.css"), "utf8");
// Потолок пула читается из самой статики: константа лексическая, наружу
// песочницы она не торчит, а сверять её пересказом значит сверять пересказ.
const poolMax = Number((readFileSync(app, "utf8").match(/const CHAT_POOL_MAX = (\d+)/) || [])[1]);

const chats = [];
for (let i = 1; i <= 9; i += 1) {
  chats.push({ id: "chat" + i + "-0000", project: "demo", title: "разговор номер " + i,
    mtime: "2026-08-22T10:0" + i + ":00+03:00", state: "dead", tasks: [] });
}
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };

const { sandbox, asked } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, total: 1,
      items: [{ key: sid + ":1", seq: 1, role: "user", time: "2026-08-22T10:00:00+03:00",
        text: "слова в разговоре " + sid }] };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const pin = sandbox.document.getElementById("cpin");
const text = (node) => {
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(text)].join(" ");
};
// Видно только показанный слот: спрятанный стоит с классом off.
const shown = (node) => {
  if (String(node.className || "").includes("off")) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(shown)].join(" ");
};
const open = async (id) => {
  sandbox.location.hash = "#demo/chat/" + id;
  await sandbox.refresh();
  await settle();
  await settle();
};

// --- прячется content-visibility, а не снятым узлом ---
{
  if (!/\.cslot\.off\{[^}]*content-visibility:\s*hidden/.test(css)) {
    fail("спрятанный слот прячется не content-visibility: правила нет в style.css");
  }
  if (/\.cslot\.off\{[^}]*display:\s*none/.test(css)) {
    fail("спрятанный слот погашен display:none: браузер выбросит его отрисовку");
  }
}

// --- два разговора: уходящий остаётся в пуле спрятанным ---
await open("chat1-0000");
if (!shown(pin).includes("разговор номер 1")) {
  fail("первый разговор не показался: " + shown(pin).slice(0, 200));
}
await open("chat2-0000");
if (!shown(pin).includes("разговор номер 2")) {
  fail("второй разговор не показался: " + shown(pin).slice(0, 200));
}
if (shown(pin).includes("разговор номер 1")) {
  fail("уходящий разговор остался на экране вторым слотом: " + shown(pin).slice(0, 300));
}
if (!text(pin).includes("разговор номер 1")) {
  fail("уходящий разговор снесён вместе с узлом: возврат в него будет стоить сборки");
}
if (sandbox.chatSlotKeys().length !== 2) {
  fail("в пуле не два разговора: " + JSON.stringify(sandbox.chatSlotKeys()));
}

// --- возврат в открытый разговор виден тем же ходом и без запросов ---
{
  sandbox.location.hash = "#demo/chat/chat1-0000";
  sandbox.repaintChatOnly();
  // Показ идёт тем же ходом: ответа сети он не ждёт вовсе. Запрос за свежестью
  // при этом уходит, но человек уже смотрит на свой разговор.
  if (!shown(pin).includes("разговор номер 1")) {
    fail("возврат в открытый разговор не показал его тем же ходом: " + shown(pin).slice(0, 300));
  }
  if (shown(pin).includes("разговор номер 2")) {
    fail("на экране два разговора разом: " + shown(pin).slice(0, 300));
  }
  if (shown(pin).includes("чат открывается") || shown(pin).includes("открывается другой")) {
    fail("возврат показал плашку ожидания поверх готового разговора");
  }
  await settle();
  await settle();
}

// --- спрятанный разговор никто не опрашивает ---
{
  await open("chat2-0000");
  const asks = asked.length;
  // Тик таймеров: живые опросы спрятанного слота, если бы они остались,
  // сходили бы за его лентой и статусом.
  await settle();
  await settle();
  const after = asked.slice(asks).filter((p) => p.includes("chat1-0000"));
  if (after.length) {
    fail("спрятанный разговор всё ещё опрашивается: " + JSON.stringify(after));
  }
}

// --- давние слоты вытесняются по счёту ---
{
  for (let i = 3; i <= 9; i += 1) await open("chat" + i + "-0000");
  if (!poolMax || poolMax < 5 || poolMax > 7) {
    fail("потолок пула не в обещанных пяти-семи: " + poolMax);
  }
  const keys = sandbox.chatSlotKeys();
  if (keys.length > poolMax) fail("пул перерос потолок: " + JSON.stringify(keys));
  if (keys.some((k) => k.includes("chat1-0000"))) {
    fail("давний разговор не вытеснен: пул растёт без края");
  }
  if (!text(pin).includes("разговор номер 9")) fail("свежий разговор пропал из панели");
  if (text(pin).includes("разговор номер 1")) {
    fail("узел вытесненного разговора остался в панели: " + text(pin).slice(0, 300));
  }
  // Вытесненный возвращается обычным путём, сборкой заново.
  await open("chat1-0000");
  if (!shown(pin).includes("разговор номер 1")) {
    fail("вытесненный разговор не собрался заново: " + shown(pin).slice(0, 300));
  }
}

console.log("ok: уходящий разговор прячется content-visibility и живёт в пуле, возврат " +
  "виден тем же ходом, спрятанного никто не опрашивает, давние вытесняются");

// Стенд прогрева списка разговоров на возврате из пула (DK-872).
//
// Живой случай с шагами от человека: переключаюсь между двумя активными
// разговорами, открываю список чатов и не нахожу в нём второго, хотя он
// живой. Сворачиваю список и открываю заново, разговор появляется.
//
// Возврат в открытый разговор показывает готовый узел из пула и в сеть за
// состоянием не ходит, поэтому список разговоров слота оставался тем, каким
// его собрала прошлая сборка панели. Открытие списка рисовало состав из этой
// памяти, а перечитка тем же окном приезжала позже первого взгляда: ручка
// списка отвечает сотнями миллисекунд, у человека дашборд за внешним входом,
// и разговоры родятся мимо вкладки. Отсюда «свернул-открыл, тогда появился».
//
// Предмет стенда: возврат из пула сам перечитывает список в фон, ответ
// ложится в состояние слота, пока человек ведёт курсор к шапке, и первый
// взгляд на список видит настоящий состав. Перечитка самого открытия, пока
// она в полёте, говорит о себе строкой под строками и уходит с ответом.
//
// Сеть моделируется настоящей задержкой (LAT миллисекунд на запрос): гонка
// «первый взгляд против ответа» живёт сотнями миллисекунд, и обороты
// микрозадач её не показывают.
//
// Зовётся: node testdata/poc_chatwarm.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";
import { deepFind, hasClass } from "./poc_css.mjs";

const LAT = 300;

const A = "aaaa1111-1111-1111-1111-111111111111";
const B = "bbbb2222-2222-2222-2222-222222222222";

// Состав на сервере: стенд меняет его между сборками панели, как это делает
// разговор, заведённый или поднятый позже первого.
let chats = [
  { id: A, project: "demo", title: "первый разговор", state: "dead", idle: true,
    mtime: "2026-09-07T10:00:00+03:00" },
];

const { sandbox } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  return {};
}, { realTimers: true, latency: LAT });

const pin = sandbox.document.getElementById("cpin");

const idle = (ms) => new Promise((go) => setTimeout(go, ms));

// Сборка панели стоит сетевых ответов, и микрозадач до неё не добраться:
// условия ждутся на настоящих часах.
async function until(what, cap) {
  for (let i = 0; i < (cap || 1200); i += 1) {
    if (what()) return true;
    await idle(5);
  }
  return false;
}

// Показанный слот пула: на экране один разговор, остальные лежат рядом
// спрятанными.
const shown = () => (pin.children || []).filter((kid) => {
  const cls = String(kid.className || "");
  return cls.includes("cslot") && !cls.split(" ").includes("off");
})[0] || null;

const open = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  sandbox.window.fire("hashchange", {});
  await settle();
};

// --- первый разговор собран, когда на сервере был только он ---
await open("#demo/chat/" + A);
if (!await until(() => dump(shown()).includes("первый разговор"))) {
  fail("панель не собрала первый разговор: " + dump(pin).slice(0, 300));
}

// Сервер меняет состав: заведён позже второй, живой.
chats = [
  { id: B, project: "demo", title: "второй разговор", state: "live", idle: true,
    mtime: "2026-09-07T11:00:00+03:00" },
  { id: A, project: "demo", title: "первый разговор", state: "dead", idle: true,
    mtime: "2026-09-07T10:00:00+03:00" },
];

// --- соседняя панель собирается со свежим составом, возврат в первый идёт из
// пула, без пересборки
await open("#demo/chat/" + B);
if (!await until(() => dump(shown()).includes("второй разговор"))) {
  fail("панель не собрала второй разговор: " + dump(pin).slice(0, 300));
}
await open("#demo/chat/" + A);
// Возврат из пула показывается тем же ходом, сети за ним нет.
if (!dump(shown()).includes("первый разговор")) {
  fail("возврат в первый разговор не показал готовую панель: " + dump(pin).slice(0, 300));
}

// Пауза человеческого масштаба: прогрев возврата обязан положить ответ в
// состояние слота, пока человек ведёт курсор от панели к шапке.
await idle(LAT + 150);

// Список открывается кнопкой шапки, как у человека.
const pick = deepFind(shown(), hasClass("cdpick"))[0];
if (!pick) fail("кнопки списка чатов в шапке нет: " + dump(shown()).slice(0, 200));
pick.handlers.click({ stopPropagation: () => {} });
await settle();
const box = deepFind(shown(), hasClass("cdrop"))[0];
if (!box) fail("список чатов не открылся: " + dump(shown()).slice(0, 200));

// Первый взгляд читается до ответа перечитки самого открытия: состав настоящий,
// а перечитка в полёте и говорит о себе строкой под строками.
const first = dump(box);
if (!first.includes("второй разговор")) {
  fail("первый взгляд на список увидел прошлый состав: " + first.slice(0, 300));
}
if (!first.includes("перечитывается")) {
  fail("список промолчал об идущей перечитке: " + first.slice(0, 300));
}

// Ответ перечитки приехал: строка о ней ушла, состав остался настоящим.
if (!await until(() => !dump(box).includes("перечитывается"))) {
  fail("строка о перечитке пережила свой ответ: " + dump(box).slice(0, 300));
}
if (!dump(box).includes("второй разговор")) {
  fail("после ответа перечитки состав потерял второй разговор: " + dump(box).slice(0, 300));
}

// Живые потоки панели держат цикл событий узла настоящими таймерами: стенд
// уходит из разговора тем же способом, каким это делает браузер.
sandbox.closeChat();
sandbox.closeAgentLive();
await settle();

console.log("ок: возврат из пула встречает открытие списка настоящим составом, " +
  "а перечитка открытия говорит о себе строкой и уходит с ответом");

// Стенд закрытия панели разговора крестиком (DK-1546).
//
// На телефоне панель закрывает экран целиком, и крестик был единственным
// способом увидеть доску. Прежний крестик стирал память последнего разговора,
// и повторное открытие кнопкой шапки вело в общий чат доски: недописанная
// реплика оставалась в списке чатов, который ещё надо найти. Теперь крестик
// память держит, внизу встаёт полоска «Вернуться в разговор», а кнопка шапки
// при живой памяти ведёт в тот же разговор. На широком экране панель стоит
// сбоку, и поведение прежнее: закрытие снимает память, повторное открытие
// начинается с общего чата доски.
//
// Предмет стенда: настоящая closeChat, настоящая кнопка шапки и настоящая
// полоска на моке дерева, с памятью браузера и историей, какие есть у вкладки.
// Отдельная песочница проверяет загрузку страницы с живой памятью: на телефоне
// адрес не дорисовывается хвостом разговора (доска, ради которую открывали,
// видна), на широком экране дорисовывается, как прежде.
//
// Зовётся: node testdata/poc_chatclose.mjs static/app.js

import { makeSandbox, settle, dump, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID1 = "aaaa1111-1111";
const SID2 = "bbbb2222-2222";
const DRAFT = "недописанный ответ про роутер";
const chats = [
  { id: SID1, title: "разговор с черновиком", mtime: "2026-11-02T10:00:00+03:00",
    tasks: ["XR-1"], model: "opus", state: "live" },
  { id: SID2, title: "соседний разговор", mtime: "2026-11-02T10:05:00+03:00",
    tasks: ["XR-1"], model: "opus", state: "live" },
];
const board = { prefix: "XR", sections: [
  { key: "in-progress", rows: [{ id: "XR-1", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
] };
const feeds = {
  [SID1]: [
    { key: SID1 + ":1", seq: 1, role: "user", time: "2026-11-02T10:00:00+03:00",
      text: "реплика первого разговора" },
  ],
  [SID2]: [
    { key: SID2 + ":1", seq: 1, role: "user", time: "2026-11-02T10:05:00+03:00",
      text: "реплика второго разговора" },
  ],
};

const answers = (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/chats")) return { chats, models: [{ model: "opus", tier: "M", harness: "claude-code" }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    const items = feeds[sid] || [];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
};

// Кнопка полоски возврата: подпись стоит span-ом внутри кнопки.
const backBtn = (sandbox) => {
  for (const kid of sandbox.document.getElementById("cback").children || []) {
    if (kid.tagName === "BUTTON") return kid;
  }
  return null;
};

// Живой слот панели и его поле ввода: панель держит открытый разговор слотом
// пула, спрятанные стоят с классом off.
const liveSlot = (sandbox) => {
  const pin = sandbox.document.getElementById("cpin");
  return allByClass(pin, "cslot").find((s) => !String(s.className).includes("off")) || null;
};
const slotText = (node) => {
  const out = [];
  const walk = (n) => {
    for (const kid of n.children || []) {
      if (kid.tagName === "TEXTAREA") out.push(kid);
      walk(kid);
    }
  };
  walk(node);
  return out[0] || null;
};

// Хвост истории отработал назад: мок history.back() адрес не меняет, и стенд
// доигрывает его руками, как это делает браузер.
const backLanded = async (sandbox, hash) => {
  sandbox.location.hash = hash;
  sandbox.window.fire("hashchange");
  await settle();
};

// --- телефон: загрузка с живой памятью не прячет доску за разговором ---
const draftKey = "devkit.chat.draft." + SID1;
const phone = makeSandbox(app, answers, { narrow: true, store: {
  "devkit.chat.last": SID1,
  [draftKey]: DRAFT,
} });
if (phone.sandbox.location.hash !== "") {
  fail("телефон: загрузка дорисовала адрес хвостом разговора, доска спрятана: " +
    phone.sandbox.location.hash);
}
await phone.sandbox.refresh();
await settle();
if (phone.sandbox.document.getElementById("cback").hidden) {
  fail("телефон: после загрузки с живой памятью полоски возврата нет");
}
if (!dump(phone.sandbox.document.getElementById("cback")).includes("Вернуться в разговор")) {
  fail("телефон: полоска возврата не названа: " +
    dump(phone.sandbox.document.getElementById("cback")));
}

// --- возврат полоской: тот же разговор, черновик на месте ---
backBtn(phone.sandbox).handlers.click({ stopPropagation: () => {} });
await settle();
const slot1 = liveSlot(phone.sandbox);
if (!slot1) fail("телефон: полоска не открыла панель разговора");
if (!phone.sandbox.document.getElementById("cback").hidden) {
  fail("телефон: полоска возврата не ушла после открытия разговора");
}
const ta1 = slotText(slot1);
if (!ta1) fail("телефон: в возвращённом разговоре нет поля ввода: " + dump(slot1));
if (ta1.value !== DRAFT) {
  fail("телефон: черновик реплики не на месте: " + JSON.stringify(ta1.value));
}

// --- крестик: память держится, панель уходит, полоска встаёт сразу ---
const moves1 = phone.moves;
phone.sandbox.closeChat();
if (phone.store.get("devkit.chat.last") !== SID1) {
  fail("телефон: крестик стёр память последнего разговора");
}
if (!phone.sandbox.document.getElementById("cpanel").hidden) {
  fail("телефон: крестик не увёл панель с экрана");
}
if (phone.sandbox.document.getElementById("cback").hidden) {
  fail("телефон: полоска возврата не встала за крестиком тем же ходом");
}
if (!moves1.some((m) => m[0] === "back")) {
  fail("телефон: крестик не пошёл историей назад: " + JSON.stringify(moves1));
}
// История дошла назад, экран перерисовался: полоска обязана устоять и в этом
// обходе, а не только в мгновенной постановке.
await backLanded(phone.sandbox, "");
if (phone.sandbox.document.getElementById("cback").hidden) {
  fail("телефон: полоска возврата пропала после обхода экрана");
}

// --- кнопка шапки при живой памяти ведёт в последний разговор ---
phone.sandbox.document.getElementById("chats").handlers.click({ stopPropagation: () => {} });
await backLanded(phone.sandbox, "demo/chat/" + SID1);
const slot2 = liveSlot(phone.sandbox);
if (!slot2) fail("телефон: кнопка шапки не открыла панель разговора");
const ta2 = slotText(slot2);
if (!ta2 || ta2.value !== DRAFT) {
  fail("телефон: кнопка шапки вернула не тот разговор, черновика нет: " +
    (ta2 ? JSON.stringify(ta2.value) : "поля ввода нет"));
}

// --- переход в другой чат обновляет память ---
phone.sandbox.switchChat(SID2);
await settle();
if (phone.store.get("devkit.chat.last") !== SID2) {
  fail("телефон: переключение разговора не обновило память");
}

// --- без памяти кнопка шапки открывает общий чат доски ---
phone.store.delete("devkit.chat.last");
await backLanded(phone.sandbox, "demo");
phone.sandbox.document.getElementById("chats").handlers.click({ stopPropagation: () => {} });
if (!phone.sandbox.location.hash.endsWith("/chat/board")) {
  fail("телефон: без памяти кнопка шапки ведёт не в общий чат доски: " +
    phone.sandbox.location.hash);
}

// --- широкий экран: прежний порядок, закрытие снимает память ---
const desk = makeSandbox(app, answers, { store: { "devkit.chat.last": SID1 } });
if (!desk.sandbox.location.hash.endsWith("/chat/" + SID1)) {
  fail("широкий экран: загрузка не дорисовала адрес последним разговором: " +
    desk.sandbox.location.hash);
}
await desk.sandbox.refresh();
await settle();
if (desk.sandbox.document.getElementById("cpanel").hidden) {
  fail("широкий экран: дорисованный разговор не открыл панель");
}
desk.sandbox.closeChat();
if (desk.store.has("devkit.chat.last")) {
  fail("широкий экран: крестик не снял память разговора");
}
if (!desk.sandbox.document.getElementById("cback").hidden) {
  fail("широкий экран: полоска возврата встала там, где панели нет сбоку");
}
desk.sandbox.document.getElementById("chats").handlers.click({ stopPropagation: () => {} });
await settle();
if (!desk.sandbox.location.hash.endsWith("/chat/board")) {
  fail("широкий экран: после закрытия кнопка шапки ведёт не в общий чат доски: " +
    desk.sandbox.location.hash);
}

console.log("poc_chatclose: ok");

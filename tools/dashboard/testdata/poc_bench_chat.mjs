// Замер отзывчивости панели разговора (ветка poc-chat).
//
// Меряется то, на что жалуется человек: сколько проходит от нажатия до первого
// отклика на экране при переключении чатов и при закрытии панели. Сеть
// моделируется настоящей задержкой (LAT миллисекунд на запрос), потому что
// именно её и ждали: до правки переключение чата тянуло за собой полный обход
// экрана (список проектов, подписки, доска), а закрытие панели ходило за тем
// же самым, хотя экран под панелью уже нарисован.
//
// Зовётся: node testdata/poc_bench_chat.mjs static/app.js [задержка]

import { makeSandbox, appPathArg, fail } from "./poc_dom.mjs";

const app = appPathArg();
const LAT = Number(process.argv[3]) || 60;

const chats = [
  { id: "aaaa1111-1111", title: "первый разговор", mtime: "2026-08-22T10:00:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-1", state: "live", sock: "/tmp/1.sock", pid: 11 },
  { id: "bbbb2222-2222", title: "второй разговор", mtime: "2026-08-22T10:05:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-2", state: "live", sock: "/tmp/2.sock", pid: 12 },
];
// Доска настоящего размера: её пересборка и есть та тяжесть, которую человек
// ждал на каждом переключении чата.
const rows = [];
for (let i = 1; i <= 80; i += 1) {
  rows.push({ id: "XR-" + i, title: "строка доски номер " + i, sect: "backlog",
    r: 40 - (i % 30), r_parts: [25, 5, 3, 0, 2], cost: "M", type: "task" });
}
const board = { prefix: "XR", sections: [
  { key: "in-progress", rows: [{ id: "XR-1", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
  { key: "backlog", rows },
] };
// Лента разговора длинная: её сборка тоже стоит времени.
const items = [];
for (let i = 0; i < 120; i += 1) {
  items.push({ role: i % 2 ? "assistant" : "user", time: "2026-08-22T10:0" + (i % 10) + ":00+03:00",
    text: "реплика номер " + i + ", в ней сколько-то слов про доску и про агента" });
}

const { sandbox, asked } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/chats")) return { chats, models: [{ model: "opus", tier: "M", harness: "claude-code" }] };
  if (path.includes("/sessions/")) {
    return { session: "aaaa1111-1111", head: { id: "aaaa1111-1111" }, items, total: items.length };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
}, { realTimers: true, latency: LAT });

const pin = sandbox.document.getElementById("cpin");
const panel = sandbox.document.getElementById("cpanel");
const groups = sandbox.document.getElementById("groups");

const idle = (ms) => new Promise((go) => setTimeout(go, ms));
const filled = () => (pin.children || []).length > 0;
const text = (node) => {
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(text)].join(" ");
};

// Ждём условия, отмечая момент, когда оно стало правдой.
async function until(what, cap) {
  const t0 = Date.now();
  for (let i = 0; i < (cap || 400); i += 1) {
    if (what()) return Date.now() - t0;
    await idle(5);
  }
  return -1;
}

sandbox.location.hash = "#demo/chat/aaaa1111-1111";
await sandbox.refresh();
// Ждём именно собранный чат, а не первый же узел: пока стоит слово ожидания,
// панель ещё не готова, и замер от неё считал бы не то.
await until(() => text(pin).includes("первый разговор"), 800);

// --- переключение на соседний чат ---
const before = text(pin);
const n0 = asked.length;
const t1 = Date.now();
sandbox.switchChat("bbbb2222-2222");
// Отклик в тот же ход это либо готовое содержимое (разговор лежит в пуле), либо
// полоска хода над панелью: словами о переходе панель больше не говорит, они
// мелькали поверх живого разговора (жалоба пользователя).
const loading = () => String(panel.className || "").includes("cload");
const sync = text(pin) !== before || loading();
// Первый отклик это любая смена того, что человек видит: очистка ленты со
// словом ожидания либо уже собранный чат.
const react = await until(() => text(pin) !== before || loading());
const done = await until(() => text(pin).includes("второй разговор"));
const all = Date.now() - t1;

const nSwitch = asked.length - n0;

// --- закрытие панели ---
const n1 = asked.length;
const t2 = Date.now();
sandbox.closeChat();
const shut = await until(() => panel.hidden === true);

console.log("задержка сети: " + LAT + " мс на запрос");
console.log("переключение чата: отклик в тот же ход " + (sync ? "да" : "нет") +
  ", первый отклик " + react + " мс, готовый чат " + done +
  " мс (всего " + all + " мс), запросов " + nSwitch);
await idle(80);
console.log("закрытие панели: " + shut + " мс, запросов " + (asked.length - n1));
console.log("строк доски на экране: " + (text(groups).match(/строка доски/g) || []).length);

// Замер заодно сторожит найденное: отклик переключения в тот же ход, а закрытие
// панели без единого запроса. Обе цифры давались полным обходом экрана на
// каждое движение панели, и вернуть его назад легко.
if (!sync) fail("переключение чата не отвечает в тот же ход: человек ждёт сеть");
if (asked.length - n1 > 0) {
  fail("закрытие панели сходило в сеть " + (asked.length - n1) + " раз: под панелью и так всё нарисовано");
}
if (shut > 5) fail("панель закрывалась " + shut + " мс: закрытие ничего ждать не должно");

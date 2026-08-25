// Замер открытия формы задачи по ссылке из ленты (ветка poc-chat).
//
// Человек жалуется, что форма по ссылке открывается две-три секунды, а
// серверные ручки при этом отвечают за десятки миллисекунд. Значит ждёт
// клиент: переход собирает экран заново и ходит за списком проектов, доской и
// самой задачей по очереди, каждый раз ожидая сеть, и до первого отклика
// человек видит прежний экран. Меряется тут то же, что у переключения чатов:
// отвечает ли переход в тот же ход, сколько запросов уходит и сколько времени
// проходит до готовой формы.
//
// Зовётся: node testdata/poc_bench_task.mjs static/app.js [задержка]

import { makeSandbox, appPathArg, fail } from "./poc_dom.mjs";

const app = appPathArg();
const LAT = Number(process.argv[3]) || 60;

const chats = [
  { id: "aaaa1111-1111", title: "первый разговор", mtime: "2026-08-22T10:00:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-1", state: "live", sock: "/tmp/1.sock", pid: 11 },
];

// Доска настоящего размера: её пересборка это та тяжесть, которую переход тянет
// за собой на каждый шаг.
const rows = [];
for (let i = 1; i <= 80; i += 1) {
  rows.push({ id: "XR-" + i, title: "строка доски номер " + i, sect: "backlog",
    r: 40 - (i % 30), r_parts: [25, 5, 3, 0, 2], cost: "M", type: "task" });
}
const board = { prefix: "XR", sections: [
  { key: "in-progress", rows: [{ id: "XR-1", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
  { key: "backlog", rows },
] };

const items = [];
for (let i = 0; i < 60; i += 1) {
  items.push({ seq: i, key: "main-" + i, role: i % 2 ? "assistant" : "user",
    time: "2026-08-22T10:0" + (i % 10) + ":00+03:00",
    text: "реплика номер " + i + " про доску и про агента" });
}
// Последняя реплика с упоминанием задачи: по нему и жмут.
items.push({ seq: 60, key: "main-60", role: "assistant", time: "2026-08-22T10:30:00+03:00",
  text: "Завёл XR-42, ранг посчитан." });

let mark = 0;
const trail = [];
const { sandbox, asked } = makeSandbox(app, (path) => {
  if (mark) trail.push(String(path).replace("/api/projects/demo", "").replace("/api/", "") +
    " +" + (Date.now() - mark));
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/tasks/")) {
    return { project: "demo", id: "XR-42",
      row: { id: "XR-42", title: "задача по ссылке из ленты", sect: "backlog", type: "task",
        p: "P2", r: 41, r_parts: [25, 5, 3, 0, 2], cost: "M" },
      after: [], blocks: [], file: "docs/tasks/XR-42.md",
      text: "# XR-42\n\nПостановка задачи.\n" };
  }
  if (path.includes("/chats")) return { chats, models: [{ model: "opus", tier: "M", harness: "claude-code" }] };
  if (path.includes("/sessions/")) {
    return { session: "aaaa1111-1111", head: { id: "aaaa1111-1111" }, items, total: items.length };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
}, { realTimers: true, latency: LAT });

const pin = sandbox.document.getElementById("cpin");
const groups = sandbox.document.getElementById("groups");

const idle = (ms) => new Promise((go) => setTimeout(go, ms));
const text = (node) => {
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(text)].join(" ");
};
const find = (node, what) => {
  if (String(node.className || "").includes(what)) return node;
  for (const kid of node.children || []) {
    const hit = find(kid, what);
    if (hit) return hit;
  }
  return null;
};

async function until(what, cap) {
  const t0 = Date.now();
  for (let i = 0; i < (cap || 400); i += 1) {
    if (what()) return Date.now() - t0;
    await idle(5);
  }
  return -1;
}

// Панель открыта на разговоре: ровно так человек и читает ленту, из которой
// потом жмёт ссылку.
sandbox.location.hash = "#demo/chat/aaaa1111-1111";
await sandbox.refresh();
await until(() => text(pin).includes("XR-42"), 800);

const link = find(pin, "mdgo");
if (!link) fail("в ленте нет автоссылки на задачу: " + text(pin).slice(0, 300));

// --- переход по ссылке из ленты ---
const before = text(groups);
const n0 = asked.length;
const t1 = Date.now();
mark = t1;
link.handlers.click({ preventDefault: () => {}, stopPropagation: () => {} });
// Браузер на смену адреса поднимает hashchange, и переход живёт его
// обработчиком: в стенде его поднимает сам стенд, там же, где браузер.
sandbox.window.fire("hashchange", {});
const sync = text(groups) !== before;
const react = await until(() => text(groups) !== before);
// Готовая форма это не оболочка: у неё есть кнопки правки строки.
const done = await until(() => text(groups).includes("Отменить правку"), 3000);
const all = Date.now() - t1;
const asks = asked.slice(n0);

console.log("задержка сети: " + LAT + " мс на запрос");
console.log("открытие формы по ссылке: отклик в тот же ход " + (sync ? "да" : "нет") +
  ", первый отклик " + react + " мс, готовая форма " + done + " мс (всего " + all +
  " мс), запросов " + asks.length);
console.log("запросы перехода: " + trail.join(" -> "));

// Замер сторожит найденное: переход отвечает в тот же ход, заказ строки уходит
// тогда же, доска на экран задачи больше не ходит, а готовая форма стоит в один
// круг по сети. До правки переход шёл лесенкой из пяти кругов (список
// проектов, уведомления, квота, подписки, доска и только потом сама строка), и
// человек всё это время смотрел на прежний экран.
if (!sync) fail("переход по ссылке не отвечает в тот же ход: человек ждёт сеть");
if (!trail.length || !trail[0].includes("/tasks/XR-42")) {
  fail("первым запросом перехода ушла не строка задачи: " + trail.join(", "));
}
if (trail.some((t) => t.includes("/board"))) {
  fail("переход на экран задачи сходил за доской: " + trail.join(", "));
}
if (done < 0 || done > LAT + 80) {
  fail("форма собралась за " + done + " мс при круге по сети в " + LAT +
    " мс: переход ждёт лишние круги (" + trail.join(", ") + ")");
}

// Опрос вопроса клиента и хвост ленты держат живые таймеры: замер сделан, и
// ждать их нечего.
console.log("poc_bench_task: ok");
process.exit(0);

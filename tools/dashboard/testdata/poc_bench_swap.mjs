// Замер возврата в уже открытый разговор (ветка poc-chat).
//
// Меряется то, что делает человек в разборе замечаний: ходит между двумя-тремя
// чатами туда и обратно. Прежде каждый заход пересобирал панель с нуля и ждал
// сеть, хотя разговор был открыт минуту назад. Сеть тут моделируется настоящей
// задержкой (LAT миллисекунд на запрос), потому что её и ждали.
//
// Зовётся: node testdata/poc_bench_swap.mjs static/app.js [задержка]

import { makeSandbox, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const LAT = Number(process.argv[3]) || 60;

const chats = [
  { id: "aaaa1111-1111", title: "первый разговор", mtime: "2026-08-22T10:00:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-1", state: "live", sock: "/tmp/1.sock", pid: 11 },
  { id: "bbbb2222-2222", title: "второй разговор", mtime: "2026-08-22T10:05:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-2", state: "live", sock: "/tmp/2.sock", pid: 12 },
];
const rows = [];
for (let i = 1; i <= 80; i += 1) {
  rows.push({ id: "XR-" + i, title: "строка доски номер " + i, sect: "backlog",
    r: 40 - (i % 30), r_parts: [25, 5, 3, 0, 2], cost: "M", type: "task" });
}
const board = { prefix: "XR", sections: [
  { key: "in-progress", rows: [{ id: "XR-1", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
  { key: "backlog", rows },
] };
const feedOf = (who) => {
  const items = [];
  for (let i = 0; i < 120; i += 1) {
    items.push({ key: who + ":" + i, seq: i,
      role: i % 2 ? "assistant" : "user", time: "2026-08-22T10:0" + (i % 10) + ":00+03:00",
      text: "реплика номер " + i + " в разговоре " + who + ", сколько-то слов про доску и агента" });
  }
  return items;
};

const { sandbox, asked } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/chats")) return { chats, models: [{ model: "opus", tier: "M", harness: "claude-code" }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    const items = feedOf(sid.slice(0, 4));
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
}, { realTimers: true, latency: LAT });

const pin = sandbox.document.getElementById("cpin");
const idle = (ms) => new Promise((go) => setTimeout(go, ms));
const text = (node) => {
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(text)].join(" ");
};
// Видно только показанное: спрятанный слот в пуле стоит с классом off, и
// считать его содержимое увиденным нельзя.
const shown = (node) => {
  if (String(node.className || "").includes("off")) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(shown)].join(" ");
};

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
await until(() => text(pin).includes("первый разговор"), 800);

// Уходим во второй разговор и ждём его готовности: теперь первый это «уже
// открытый чат», в который человек вернётся.
sandbox.switchChat("bbbb2222-2222");
await until(() => shown(pin).includes("второй разговор"), 800);
await idle(LAT * 2);

// --- возврат в уже открытый разговор ---
const asks = asked.length;
const t0 = Date.now();
sandbox.switchChat("aaaa1111-1111");
// Мгновенный показ это тот же ход: содержимое первого разговора на экране
// сразу после нажатия, без единого ответа сети.
const sync = shown(pin).includes("первый разговор");
const back = await until(() => shown(pin).includes("первый разговор"), 800);
const seen = Date.now() - t0;
console.log("задержка сети: " + LAT + " мс на запрос");
console.log("возврат в открытый чат: тем же ходом " + (sync ? "да" : "нет") +
  ", содержимое на экране " + back + " мс (всего " + seen + " мс), запросов до показа " +
  (sync ? 0 : asked.length - asks));
await idle(LAT * 3);
console.log("слотов в пуле: " + (text(pin).match(/разговор/g) ? "два" : "нет") +
  ", запросов за весь возврат " + (asked.length - asks));
// Панель закрывается: живые опросы разговора держали бы процесс замера.
sandbox.closeChat();
process.exit(0);

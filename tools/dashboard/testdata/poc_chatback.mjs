// Стенд возврата в уже открытый разговор (ветка poc-chat).
//
// Переключение чатов идёт пулом панелей: готовый узел показывается тем же
// ходом. Следом, однако, ехала полная пересборка панели, и человек видел два
// движения на одно нажатие: мгновенный показ и перерисовку с лентой, собранной
// заново (жалоба пользователя). Плюс над живым содержимым мелькала надпись об
// открытии, которая в мгновенном переходе просто врала.
//
// Предмет стенда: показ из пула окончателен (разметка не пересобирается),
// приехавшая дельта дописывается в ту же ленту по месту, а слов об открытии в
// панели нет.
//
// Зовётся: node testdata/poc_chatback.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID1 = "aaaa1111-1111";
const SID2 = "bbbb2222-2222";
const chats = [
  { id: SID1, title: "первый разговор", mtime: "2026-08-22T10:00:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-1", state: "live" },
  { id: SID2, title: "второй разговор", mtime: "2026-08-22T10:05:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "chat-XR-1-2", state: "live" },
];
const board = { prefix: "XR", sections: [
  { key: "in-progress", rows: [{ id: "XR-1", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
] };

// Лента разговора: у каждого своя, и в первую по ходу стенда доедет ещё одна
// запись, пока человек сидит во втором.
const feeds = {
  [SID1]: [
    { key: SID1 + ":1", seq: 1, role: "user", time: "2026-08-22T10:00:00+03:00",
      text: "первая реплика первого разговора" },
    { key: SID1 + ":2", seq: 2, role: "assistant", time: "2026-08-22T10:01:00+03:00",
      text: "ответ агента в первом разговоре" },
  ],
  [SID2]: [
    { key: SID2 + ":1", seq: 1, role: "user", time: "2026-08-22T10:05:00+03:00",
      text: "первая реплика второго разговора" },
  ],
};

const { sandbox, byId } = makeSandbox(app, (path) => {
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
});

// Узлы разметки заводит сам app.js, спрашивая их у документа: до первой
// отрисовки их в стенде нет вовсе.
const pin = sandbox.document.getElementById("cpin");
const panel = sandbox.document.getElementById("cpanel");
// Показанный слот один: спрятанные стоят с классом off и на экране их нет.
const slotOf = () => allByClass(pin, "cslot").find((s) => !String(s.className).includes("off"));
const bubbles = (node) => allByClass(node, "mlist")
  .flatMap((list) => Array.from(list.children || []));

sandbox.location.hash = "#demo/chat/" + SID1;
await sandbox.refresh();
await settle();

const first = slotOf();
if (!first) fail("панель не собрала слот первого разговора: " + dump(pin));
if (!dump(first).includes("ответ агента в первом разговоре")) {
  fail("лента первого разговора не собралась: " + dump(first));
}
const wasBubbles = bubbles(first);
if (wasBubbles.length < 2) fail("в ленте меньше двух записей: " + wasBubbles.length);

// Считаем перерисовки панели: пересборка это замена содержимого слота, её и
// делал прежний возврат вторым движением после мгновенного показа.
let repaints = 0;
const wasReplace = first.replaceChildren;
first.replaceChildren = (...kids) => {
  repaints += 1;
  return wasReplace.apply(first, kids);
};

// --- уходим во второй разговор ---
sandbox.switchChat(SID2);
await settle();
if (!dump(slotOf()).includes("первая реплика второго разговора")) {
  fail("второй разговор не открылся: " + dump(pin));
}
// Пока человек сидит во втором, в первый приезжает ещё одна запись.
feeds[SID1].push({ key: SID1 + ":3", seq: 3, role: "assistant",
  time: "2026-08-22T10:07:00+03:00", text: "запись, приехавшая в отсутствие" });

// --- возврат в первый: показ окончателен ---
sandbox.switchChat(SID1);
// Содержимое на экране тем же ходом, без единого ответа сети.
if (!dump(slotOf()).includes("ответ агента в первом разговоре")) {
  fail("возврат в открытый разговор не показал его тем же ходом: " + dump(pin));
}
if (dump(pin).includes("открывается")) {
  fail("панель сказала об открытии там, где всё уже нарисовано: " + dump(pin));
}
if (String(panel.className || "").includes("cload")) {
  fail("возврат в открытый разговор зажёг полоску ожидания: ждать тут нечего");
}
await settle();

if (repaints) fail("показ из пула не окончателен: панель пересобрана " + repaints + " раз");
if (slotOf() !== first) fail("возврат подменил сам узел слота");
const now = bubbles(first);
// Старые записи стоят теми же узлами: дельта дописывает недостающее по месту,
// а не пересобирает ленту целиком. Последняя запись прежнего хода тут не в
// счёт: приехавший ответ агента перенял у неё пометку конца хода, и ей положено
// перерисоваться.
for (let i = 0; i < wasBubbles.length - 1; i++) {
  if (now[i] !== wasBubbles[i]) {
    fail("запись " + i + " пересобрана заново: лента мигнула вместо дописывания");
  }
}
if (now.length !== wasBubbles.length + 1) {
  fail("дельта не дописалась: записей " + now.length + ", было " + wasBubbles.length);
}
if (!dump(first).includes("запись, приехавшая в отсутствие")) {
  fail("приехавшая запись не встала в ленту: " + dump(first));
}

// --- живое поднято заново: очередь своих реплик принимает ответ ---
// Уход с разговора гасит его потоки, и вернувшийся человек писал бы в мёртвую
// очередь, не поднимись она вместе с показом.
const ta = allByClass(first, "chatwrap").flatMap((w) => Array.from(w.children || []))
  .find((k) => k.tagName === "TEXTAREA")
  || (function find(node) {
    for (const kid of node.children || []) {
      if (kid.tagName === "TEXTAREA") return kid;
      const hit = find(kid);
      if (hit) return hit;
    }
    return null;
  })(first);
if (!ta) fail("в возвращённой панели нет поля ввода: " + dump(first));
if (ta.disabled) fail("поле ввода возвращённой панели заперто");
const send = allByClass(first, "btn").find((b) => b.textContent === "Отправить");
if (!send) fail("в возвращённой панели нет кнопки отправки");
ta.value = "реплика после возврата";
send.handlers.click({ stopPropagation: () => {} });
await settle();
if (!dump(first).includes("реплика после возврата")) {
  fail("реплика после возврата не встала в панель: очередь не поднялась вместе с показом");
}

sandbox.closeChat();
console.log("poc_chatback: ok");

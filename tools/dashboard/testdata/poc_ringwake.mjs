// Стенд переподъёма кольца хода работ после возврата в разговор из пула (бага
// пользователя: работа в чате закончена, а кружок продолжал крутиться и
// показывал оставшиеся пункты, и честным состояние становилось только
// обновлением страницы). Опрос кольца живёт в chatLive и умирает любым уходом
// из разговора, а возврат из пула поднимал заново только живое панели. Стенд
// меряет, что возврат снова спрашивает пульс и кольцо дорисовывает пришедшее.
//
// Зовётся: node testdata/poc_ringwake.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const SID = "aaaa1111-1111-4111-8111-111111111111";
const OTHER = "bbbb2222-2222-4222-8222-222222222222";

const chats = [
  { id: SID, project: "demo", title: "разговор работы", tasks: ["XR-1"], state: "dead",
    model: "opus", mtime: "2026-09-01T10:00:00+03:00" },
  { id: OTHER, project: "demo", title: "соседний разговор", tasks: [], state: "dead",
    mtime: "2026-09-01T10:05:00+03:00" },
];
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "ход работы виден кольцом", sect: "in-progress" },
] }] };

const now = Math.floor(Date.now() / 1000);

// Два снимка одной работы: идёт с открытыми пунктами и кончилась с закрытым
// целиком планом. Второй снимок и есть та свежесть, которая прежде не доезжала
// до кольца без обновления страницы.
let mood = "working";
const pulses = {
  working: {
    task: "XR-1", state: "working", count: 1, working: 1, idle: 0, quiet: 60,
    phase: "код", tool: "Bash", about: "правка кольца", since: now - 15,
    plan: [
      { text: "разобрать механизм", state: "completed" },
      { text: "написать тест", state: "in_progress" },
      { text: "исправить", state: "pending" },
    ],
    own: { session: SID, name: "chat-XR-1", state: "working", own: true,
      tool: "Bash", about: "правка кольца", since: now - 15 },
    agents: [{ session: SID, name: "chat-XR-1", title: "разговор работы", own: true,
      state: "working", tool: "Bash", about: "правка кольца", since: now - 15 }],
  },
  done: {
    task: "XR-1", state: "silent", count: 1, working: 0, idle: 1, quiet: 60,
    phase: "выкат", tool: "Bash", about: "go test", since: now - 900,
    plan: [
      { text: "разобрать механизм", state: "completed" },
      { text: "написать тест", state: "completed" },
      { text: "исправить", state: "completed" },
    ],
    own: { session: SID, name: "chat-XR-1", state: "idle", own: true,
      tool: "Bash", about: "go test", since: now - 900 },
    agents: [{ session: SID, name: "chat-XR-1", title: "разговор работы", own: true,
      state: "idle", tool: "Bash", about: "go test", since: now - 900 }],
  },
};

const { sandbox, asked } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/pulse")) return pulses[mood];
  if (path.includes("/chats")) return { chats, models: [] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, total: 1,
      items: [{ key: sid + ":1", seq: 1, role: "user", time: "2026-09-01T10:00:00+03:00",
        text: "слова разговора " + sid }] };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const pin = sandbox.document.getElementById("cpin");

// Показанный слот пула: скрытые стоят классом off, и кольцо читается только у
// стоящего на экране.
const shownSlot = () => (pin.children || []).find((k) => {
  const cls = String(k.className || "").split(" ");
  return cls.includes("cslot") && !cls.includes("off");
});

const ring = () => {
  const slot = shownSlot();
  if (!slot) fail("показанного слота нет: панель не открыта");
  const wrap = byClass(slot, "ringwrap");
  if (!wrap) fail("кольца в показанном разговоре нет");
  return wrap;
};

const fraction = (wrap) => allByClass(wrap, "rnum").map((n) => String(n.textContent));

const open = async (id) => {
  sandbox.location.hash = "#demo/chat/" + id;
  await sandbox.refresh();
  await settle();
  await settle();
};

// --- работа идёт: кольцо крутится с открытым пунктом ---
await open(SID);
const was = ring();
if (!String(was.className).includes("r-working")) {
  fail("кольцо работающего разговора не назвало работу классом: " + was.className);
}
if (allByClass(was, "here").length !== 1) {
  fail("идущего пункта нет на кольце: " + allByClass(was, "here").length);
}
if (fraction(was).join("/") !== "1/3") {
  fail("дробь плана не встала в середину: " + JSON.stringify(fraction(was)));
}

// --- уход в соседний разговор и конец работы за спиной ---
await open(OTHER);
if (ring() === was) fail("переход в соседний разговор не сменил панель");
const mark = asked.length;
mood = "done";

// --- возврат тем же ходом: кольцо спрашивает пульс и дорисовывает ---
sandbox.location.hash = "#demo/chat/" + SID;
sandbox.repaintChatOnly();
await settle();
await settle();
const again = ring();
if (again !== was) {
  fail("возврат пересобрал кольцо новым узлом вместо обновления на месте");
}
if (!asked.slice(mark).some((p) => p.includes("/pulse") && p.includes("sid=" + SID))) {
  fail("возврат в разговор не спросил пульс кольца: " + JSON.stringify(asked.slice(mark)));
}
if (String(again.className).includes("r-working")) {
  fail("кончившаяся работа всё крутится на кольце: " + again.className);
}
if (allByClass(again, "here").length) {
  fail("идущий пункт остался на кольце кончившейся работы");
}
if (fraction(again).join("/") !== "3/3") {
  fail("дробь плана не закрылась целиком: " + JSON.stringify(fraction(again)));
}

console.log("ok: возврат в разговор снова спрашивает пульс, кольцо дорисовывает " +
  "кончившуюся работу на том же узле");

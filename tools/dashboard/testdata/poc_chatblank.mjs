// Стенд незачатого разговора (ветка poc-chat). Жалоба пользователя: «новый чат
// создаётся по сути после первой реплики». Разговор и сессия клиента были одним
// и тем же, поэтому до первой реплики разговора не существовало нигде, кроме
// адреса вкладки, а адрес этот был один на всю вкладку: набранный текст
// оставался в следующем новом чате, в списке чата было не видно, а завести
// рядом второй было нечем.
//
// Предмет проверки тут собранная панель и её обработчики: нажатие «+» заводит
// разговор на сервере, два таких разговора живут порознь со своими
// черновиками, а первая реплика поднимает сессию и пришивает её к записи.
//
// Зовётся: node testdata/poc_chatblank.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "дашборд без дёрганья", sect: "in-progress" },
] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];

// Записи разговоров живут у стенда, как и на настоящем сервере: заведение,
// черновик и подъём правят один и тот же набор.
const chats = [];
let born = 0;
const bodies = {};
const { sandbox, store, timers, posted } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    const body = init.body ? JSON.parse(init.body) : null;
    bodies[path] = body;
    if (path.endsWith("/chats/blank")) {
      born += 1;
      const id = "blank-" + born;
      chats.unshift({ id, project: "demo", blank: true, state: "blank",
        model: (body && body.model) || "opus", mtime: new Date().toISOString(),
        tasks: body && body.id ? [body.id] : [] });
      return { id, model: (body && body.model) || "opus", task: (body && body.id) || "" };
    }
    if (path.endsWith("/draft")) {
      const id = path.split("/chats/")[1].split("/")[0];
      const hit = chats.find((c) => c.id === id);
      if (hit) hit.draft = body ? body.text : "";
      return { kept: true };
    }
    if (path.endsWith("/chats")) return { tmux: "chat-1", model: "opus" };
    return {};
  }
  if (path.includes("/chats")) return { chats, models, days: 3, older: false };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

// tick прокручивает отложенные записи черновика: панель пишет его не на каждую
// букву, а спустя паузу.
const tick = async () => {
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

// --- «+» заводит разговор до всякой реплики ---
sandbox.location.hash = "#demo/chat/board";
let st = await sandbox.chatState("demo", "board", board);
const plus = deepBtn(sandbox.chatHead("demo", st), "cdbtn");
if (!plus) fail("кнопки «+» нет в шапке панели");
plus.handlers.click({ stopPropagation: () => {} });
await settle();
if (!posted.some((p) => p.endsWith("/chats/blank"))) {
  fail("нажатие «+» не завело разговор на сервере: " + JSON.stringify(posted));
}
if (chats.length !== 1 || chats[0].id !== "blank-1") {
  fail("записи разговора не появилось: " + JSON.stringify(chats));
}

// --- заведённый разговор виден в списке и открыт для реплики ---
st = await sandbox.chatState("demo", "blank-1", board);
if (st.blank !== "blank-1") fail("панель не узнала незачатый разговор: " + JSON.stringify(st.blank));
if (st.sid) fail("у незачатого разговора взялась сессия: " + st.sid);
if (!st.fresh) fail("панель не встретила разговор экраном нового чата");
const way = sandbox.chatWay(st);
if (way.kind !== "new" || way.off) {
  fail("в незачатый разговор нельзя написать: " + JSON.stringify(way));
}
const head = sandbox.chatHead("demo", st);
const line = byClass(head, "chline");
line.children[0].handlers.click({ stopPropagation: () => {} });
const drop = line.children[line.children.length - 1];
const rows = byClass(drop, "cdrows");
if (!dump(rows).includes("Новый чат") || !dump(rows).includes("не начат")) {
  fail("незачатого разговора нет строкой списка: " + dump(rows).slice(0, 300));
}

// --- второй разговор заводится рядом, а не поверх первого ---
const plusAgain = deepBtn(sandbox.chatHead("demo", st), "cdbtn");
plusAgain.handlers.click({ stopPropagation: () => {} });
await settle();
if (chats.length !== 2) fail("второй разговор не завёлся: " + JSON.stringify(chats));

// --- у каждого свой набранный текст ---
const typeInto = async (id, text) => {
  const state = await sandbox.chatState("demo", id, board);
  const ta = tag(sandbox.chatPanel("demo", state), "TEXTAREA");
  if (!ta) fail("в панели " + id + " нет поля ввода");
  ta.value = text;
  ta.handlers.input();
  await tick();
  return ta;
};
await typeInto("blank-1", "разберись с расходом подписки");
const second = await typeInto("blank-2", "посмотри поезд слияния");
if (second.value !== "посмотри поезд слияния") {
  fail("поле второго разговора взяло чужой текст: " + JSON.stringify(second.value));
}
const draftOf = (id) => (chats.find((c) => c.id === id) || {}).draft;
if (draftOf("blank-1") !== "разберись с расходом подписки" ||
    draftOf("blank-2") !== "посмотри поезд слияния") {
  fail("черновики уехали не по своим разговорам: " + JSON.stringify(chats));
}

// Черновик живёт при самом разговоре: вкладка, которая про него не знает,
// всё равно встречает человека набранным текстом.
store.clear();
const back = tag(sandbox.chatPanel("demo", await sandbox.chatState("demo", "blank-1", board)), "TEXTAREA");
if (back.value !== "разберись с расходом подписки") {
  fail("набранное не пережило чистой вкладки: " + JSON.stringify(back.value));
}

// --- первая реплика поднимает сессию и называет свою запись ---
st = await sandbox.chatState("demo", "blank-1", board);
sandbox.chatRaise("demo", st, "почему поезд встал", "opus", () => {}).catch(() => {});
await settle();
const order = bodies["/api/projects/demo/chats"];
if (!order || order.chat !== "blank-1") {
  fail("подъём не назвал запись, из которой пишут: " + JSON.stringify(order));
}

// --- поднявшаяся сессия забирает разговор себе ---
chats.find((c) => c.id === "blank-1").grown = "sess-777";
chats.unshift({ id: "sess-777", project: "demo", title: "почему поезд встал",
  mtime: new Date().toISOString(), state: "live", idle: true, tasks: [], model: "opus" });
st = await sandbox.chatState("demo", "blank-1", board);
if (st.sid !== "sess-777" || st.addr !== "sess-777") {
  fail("панель не переехала на выросшую сессию: " + JSON.stringify([st.addr, st.sid]));
}
if (st.blank) fail("панель осталась при записи, хотя разговор уже идёт сессией");
if (st.chats.some((c) => c.id === "blank-1")) {
  fail("выросшая запись осталась второй строкой рядом со своим разговором");
}

console.log("ok");

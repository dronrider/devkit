// Стенд второго захода подъёма сессии (DK-1011).
//
// Первая реплика нового чата уезжает клиенту аргументом запуска. Клиент иногда
// умирает тут же: истёкший вход, занятая машина, сброшенная подписка. Прежде
// панель ставила пузырь «не доставлено» с кнопкой повтора, и поднимать сессию
// заново приходилось человеку. Кнопок в чате нет, и второй заход панель делает
// сама.
//
// Предмет стенда: смерть первого подъёма ведёт ко второму заходу, а вторая
// смерть подряд встаёт причиной на пузыре без кнопок.
//
// Зовётся: node testdata/poc_chatlift2.mjs static/app.js

import { makeSandbox, settle, dump, tag, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "задача", sect: "in-progress" }] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const blank = { id: "blank-1", project: "demo", blank: true, state: "not-started",
  idle: true, model: "opus", mtime: "2026-08-29T12:00:00+03:00", tasks: [] };

// Подъёмы по счёту: первый умирает всегда, судьбу второго стенд меняет сам.
let raises = 0;
let secondLives = true;
const born = { id: "aaaa1111-0002", project: "demo", title: "живой разговор",
  state: "live", tmux: "chat-2", own: true, tasks: [] };

const { sandbox, store, timers } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST") {
    if (p.endsWith("/chats")) {
      raises += 1;
      return { tmux: "chat-" + raises, model: "opus" };
    }
    return {};
  }
  if (p.includes("?tmux=chat-1")) {
    return { chats: [], dead: { why: "клиент умер на входе", tmux: "chat-1" } };
  }
  if (p.includes("?tmux=chat-2")) {
    if (secondLives) return { chats: [born] };
    return { chats: [], dead: { why: "клиент умер второй раз", tmux: "chat-2" } };
  }
  if (p.includes("/sessions/")) {
    const sid = p.slice(p.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (p.includes("/chats")) return { chats: [blank], models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});

// Опрос реестра идёт по таймеру: стенд прокручивает его руками.
const spin = async (rounds) => {
  for (let i = 0; i < (rounds || 8); i += 1) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
};

async function sendFirst() {
  store.clear();
  const st = await sandbox.chatState("demo", "blank-1", board);
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = tag(panel, "TEXTAREA");
  ta.value = "подними работу по XR-1";
  ta.handlers.input();
  await settle();
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  return panel;
}

// --- первая смерть: панель поднимает сессию вторым заходом сама ---
{
  raises = 0;
  secondLives = true;
  const panel = await sendFirst();
  await spin(10);
  if (raises < 2) {
    fail("после смерти подъёма второго захода не было, подъёмов всего " + raises);
  }
  if (raises > 2) fail("панель крутит подъёмы без конца, их уже " + raises);
  const said = dump(panel);
  if (said.includes("клиент умер на входе")) {
    fail("первая смерть встала причиной на пузыре, хотя второй заход удался: " + said.slice(0, 300));
  }
}

// --- вторая смерть подряд: причина строкой, кнопок нет ---
{
  raises = 0;
  secondLives = false;
  const panel = await sendFirst();
  await spin(12);
  if (raises !== 2) fail("заходов подъёма " + raises + ", ожидал два");
  const said = dump(panel);
  if (!said.includes("подними работу по XR-1")) {
    fail("две смерти унесли с собой текст человека: " + said.slice(0, 300));
  }
  if (!said.includes("умер")) {
    fail("вторая смерть подряд не названа на пузыре: " + said.slice(0, 300));
  }
  for (const word of ["повторить", "отменить", "открыть живой чат"]) {
    if (deepBtn(panel, word)) fail("у пузыря осталась кнопка «" + word + "»");
  }
}

console.log("ok: смерть подъёма ведёт ко второму заходу панели, вторая смерть подряд " +
  "встаёт причиной на пузыре без кнопок");

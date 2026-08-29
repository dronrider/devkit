// Стенд первой реплики нового разговора (POC DK-397, ветка poc-chat).
//
// Живой случай: «в новом чате первое отправленное сообщение не сразу ложится в
// чат, сначала оно просто улетает в никуда, а потом, когда сессия поднялась,
// подгружается» (замечание пользователя). Пузырь в ленте стоял и раньше, а
// «в никуда» читалась сама лента: поверх отправленной реплики она продолжала
// просить написать первую реплику, и экран спорил сам с собой.
//
// Предмет стенда: с первой же отправкой лента перестаёт просить то, что уже
// сделано; отказ виден на самом пузыре, а не молчит; порядок реплик, ушедших до
// подъёма сессии, остаётся порядком отправки; отменённая последняя реплика
// возвращает ленте её слова.
//
// Зовётся: node testdata/poc_chatfirst.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "задача", sect: "in-progress" }] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const chats = [{ id: "blank-1", project: "demo", blank: true, state: "not-started", idle: true,
  model: "opus", mtime: "2026-08-29T12:00:00+03:00", tasks: [] }];

// Подъём сессии тут держат руками: те самые секунды, за которые человек и
// решает, что реплика пропала.
let held = null;
let raiseFails = false;
const { sandbox, store } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    if (String(path).endsWith("/chats")) {
      if (raiseFails) return { raw: { status: 502, statusText: "Bad Gateway",
        text: JSON.stringify({ error: "клиент не поднялся" }) } };
      return { tmux: "chat-1", model: "opus" };
    }
    return {};
  }
  if (String(path).includes("/sessions/")) {
    const sid = String(path).slice(String(path).indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (String(path).includes("/chats")) return { chats, models, days: 3, older: false };
  if (String(path).endsWith("/board")) return { board, works: [] };
  return {};
});

// Настоящий подъём висит, пока стенд его не отпустит: иначе ответ приходит в
// том же обороте, и промежутка, о котором говорит человек, не бывает вовсе.
const netTo = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init && init.method === "POST" && String(path).endsWith("/chats") && !raiseFails) {
    return new Promise((go) => { held = () => go(netTo(path, init)); });
  }
  return netTo(path, init);
};

sandbox.location.hash = "#demo/chat/blank-1";

// Каждый заход начинается с чистой памяти вкладки: неушедшее переживает
// перерисовку нарочно, и без чистки пузыри прошлого случая приезжали бы в
// следующий.
async function openPanel() {
  store.clear();
  const st = await sandbox.chatState("demo", "blank-1", board);
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  return panel;
}

async function sendFrom(panel, text) {
  const ta = tag(panel, "TEXTAREA");
  if (!ta) fail("в панели нет поля ввода");
  ta.value = text;
  ta.handlers.input();
  await settle();
  const send = deepBtn(panel, "Отправить");
  if (!send) fail("в панели нет кнопки отправки: " + dump(panel).slice(0, 300));
  send.handlers.click({ stopPropagation: () => {} });
  await settle();
}

const bubbles = (panel) => allByClass(panel, "m-local");
const feedOf = (panel) => byClass(panel, "chatfeed");
const asks = (panel) => {
  const feed = feedOf(panel);
  return feed ? dump(feed).includes("напишите первую реплику") : false;
};

// --- первая реплика: пузырь стоит, а лента больше не просит её написать ---
{
  const panel = await openPanel();
  if (!asks(panel)) fail("пустая лента нового чата молчит о том, с чего начать");
  await sendFrom(panel, "почему поезд встал");
  const mine = bubbles(panel);
  if (mine.length !== 1) fail("своей реплики в панели нет: " + mine.length);
  if (!dump(mine[0]).includes("почему поезд встал")) {
    fail("пузырь стоит без текста реплики: " + dump(mine[0]).slice(0, 200));
  }
  if (asks(panel)) {
    fail("лента просит написать первую реплику поверх уже отправленной: " +
      dump(feedOf(panel)).slice(0, 200));
  }
}

// --- вторая реплика до подъёма: путаться порядку не в чем ---
//
// Пока первая реплика в полёте, отправка заперта: перепутать порядок нечем,
// потому что второй реплики в это время не бывает. Набранное при этом остаётся
// в поле, а не пропадает вместе с несостоявшимся нажатием.
{
  const panel = await openPanel();
  await sendFrom(panel, "первая реплика");
  const send = deepBtn(panel, "Отправить");
  if (!send.disabled) fail("отправка открыта, пока первая реплика ещё летит");
  const ta = tag(panel, "TEXTAREA");
  ta.value = "вторая реплика";
  ta.handlers.input();
  await settle();
  send.handlers.click({ stopPropagation: () => {} });
  await settle();
  const mine = bubbles(panel);
  if (mine.length !== 1) fail("до ответа на первую реплику встало ещё что-то: " + mine.length);
  if (!dump(mine[0]).includes("первая реплика")) {
    fail("в панели стоит не первая реплика: " + dump(mine[0]).slice(0, 120));
  }
  if (ta.value !== "вторая реплика") {
    fail("набранное на запертой отправке пропало: " + JSON.stringify(ta.value));
  }
}

// --- отказ подъёма: пузырь говорит, что реплика не ушла ---
{
  raiseFails = true;
  const panel = await openPanel();
  await sendFrom(panel, "реплика в упавший подъём");
  await settle();
  const mine = bubbles(panel);
  if (mine.length !== 1) fail("после отказа своей реплики на экране нет: " + mine.length);
  const said = dump(mine[0]);
  if (!said.includes("реплика в упавший подъём")) {
    fail("отказ унёс с собой набранное: " + said.slice(0, 200));
  }
  if (!said.includes("не ушло")) {
    fail("пузырь после отказа выглядит доставленным: " + said.slice(0, 200));
  }
  if (!said.includes("повторить") || !said.includes("отменить")) {
    fail("после отказа нечем ни повторить, ни отменить: " + said.slice(0, 200));
  }
  raiseFails = false;
}

// --- отменённая последняя реплика возвращает ленте её слова ---
{
  raiseFails = true;
  const panel = await openPanel();
  await sendFrom(panel, "передумал");
  await settle();
  const undo = deepBtn(bubbles(panel)[0], "отменить");
  if (!undo) fail("у недоставленного пузыря нет отмены");
  undo.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (bubbles(panel).length) fail("отменённая реплика осталась в панели");
  if (!asks(panel)) {
    fail("после отмены лента молчит: пустой чат обязан сказать, с чего начать: " +
      dump(feedOf(panel)).slice(0, 200));
  }
  raiseFails = false;
}

// --- перезагрузка между отправкой и подъёмом: реплика на месте ---
//
// Вкладка, поднятая заново, читает неушедшее из памяти браузера: сама отправка
// в этот момент уже уехала на сервер, и показывать пустоту значило бы уверять
// человека, что реплики не было. Причину такому пузырю панель называет сама:
// доставка не подтверждена, эха из транскрипта ещё нет.
{
  const panel = await openPanel();
  await sendFrom(panel, "реплика перед перезагрузкой");
  if (!bubbles(panel).length) fail("реплики нет ещё до перезагрузки");
  const kept = Object.fromEntries(store.entries());
  const fresh = makeSandbox(app, (path, init) => {
    if (init && init.method === "POST") return {};
    if (String(path).includes("/sessions/")) {
      const sid = String(path).slice(String(path).indexOf("/sessions/") + 10).split("?")[0];
      return { session: sid, head: { id: sid }, items: [], total: 0 };
    }
    if (String(path).includes("/chats")) return { chats, models, days: 3, older: false };
    if (String(path).endsWith("/board")) return { board, works: [] };
    return {};
  }, { store: kept });
  fresh.sandbox.location.hash = "#demo/chat/blank-1";
  const st = await fresh.sandbox.chatState("demo", "blank-1", board);
  const back = fresh.sandbox.chatPanel("demo", st);
  await settle();
  const mine = allByClass(back, "m-local");
  if (!mine.length) fail("перезагрузка унесла показанную реплику: " + dump(back).slice(0, 300));
  if (!dump(mine[0]).includes("реплика перед перезагрузкой")) {
    fail("после перезагрузки в пузыре не тот текст: " + dump(mine[0]).slice(0, 200));
  }
  const feed = byClass(back, "chatfeed");
  if (feed && dump(feed).includes("напишите первую реплику")) {
    fail("после перезагрузки лента снова просит написать уже отправленное");
  }
}

if (held) held();
console.log("poc_chatfirst: ok");

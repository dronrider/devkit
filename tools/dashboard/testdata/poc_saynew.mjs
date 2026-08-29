// Стенд курсора в поле ввода (POC DK-397, ветка poc-chat).
//
// Живой случай: «при создании чата фокус для ввода текста не ставится в поле
// ввода» (замечание пользователя). Человек нажал «+», получил пустой разговор и
// должен был ткнуть в поле ещё раз, прежде чем писать, хотя заведение чата это
// и есть намерение писать.
//
// Предмет стенда это две стороны одного правила. Свежезаведённый разговор
// отдаёт курсор полю ввода сразу, а открытый прежний разговор фокуса не
// перехватывает: туда приходят и читать, а на телефоне выехавшая ради курсора
// клавиатура закрыла бы ленту, ради которой человек и пришёл (тот же разбор,
// что у поиска по списку разговоров).
//
// Зовётся: node testdata/poc_saynew.mjs static/app.js

import { makeSandbox, settle, focused, focusDrop, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "задача", sect: "in-progress" }] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const chats = [
  { id: "blank-9", project: "demo", blank: true, state: "not-started", idle: true,
    model: "opus", mtime: "2026-08-29T12:00:00+03:00", tasks: [] },
  { id: "old-1", project: "demo", title: "прежний разговор", state: "dead",
    mtime: "2026-08-28T12:00:00+03:00", tasks: [] },
];

const { sandbox } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST" && p.endsWith("/blank")) return { id: "blank-9" };
  if (p.includes("/sessions/")) {
    const sid = p.slice(p.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (p.includes("/chats")) return { chats, models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
sandbox.location.hash = "#demo/chat/old-1";
await settle();

function said(node) {
  return node ? String(node.tagName) + "." + String(node.className || "") : "фокуса нет вовсе";
}

// Открытый прежний разговор курсора не забирает: человек мог прийти читать.
focusDrop();
await sandbox.paintChat("demo", "old-1", board, []);
await settle();
if (focused()) {
  fail("открытие прежнего разговора перехватило фокус: " + said(focused()));
}

// Заведение разговора кнопкой «+»: курсор стоит в поле ввода сразу.
focusDrop();
const id = await sandbox.chatBlankMake("demo", "");
if (id !== "blank-9") fail("разговор не завёлся: " + id);
// Панель рисуется тем же ходом, каким её рисует переключение адреса: у стенда
// нет экрана под ней, и обход шапки ему ни к чему.
await sandbox.paintChat("demo", "blank-9", board, []);
await settle();
const at = focused();
if (!at) fail("после заведения чата курсор нигде не стоит: писать по-прежнему некуда");
if (String(at.tagName) !== "TEXTAREA" || !String(at.className).includes("csay")) {
  fail("курсор ушёл мимо поля ввода: " + said(at));
}

// Второе открытие того же разговора это уже обычный приход: курсор не дёргается.
focusDrop();
await sandbox.paintChat("demo", "blank-9", board, []);
await settle();
if (focused()) {
  fail("повторное открытие свежего разговора снова забрало фокус: " + said(focused()));
}

console.log("poc_saynew: ok");

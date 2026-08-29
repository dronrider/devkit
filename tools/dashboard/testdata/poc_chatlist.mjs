// Стенд свежести списка разговоров (ветка poc-chat, DK-577).
//
// Живой случай с шагами от пользователя: «новый чат не попадает в список без
// перезагрузки страницы. Создать новый чат и написать там что угодно,
// переключиться в другой чат, открыть список чатов и убедиться, что нового чата
// там нет. После перезагрузки новый чат появится».
//
// Перечень читался один раз при сборке панели и жил в памяти вкладки. Разговор
// же родится мимо неё: свой заводится тут же в панели, соседний в другой
// вкладке или с телефона, заголовок дописывает фоновая работа, а закрытая
// запись пропадает у сервера. Возврат в открытый разговор берёт панель из пула
// готовой, и старый перечень оставался на экране до перезагрузки.
//
// Предмет стенда: открытие списка спрашивает перечень заново, поэтому в нём
// виден и свой новый разговор, и заведённый на стороне, и свежий заголовок, а
// закрытая крестиком запись уходит сразу.
//
// Зовётся: node testdata/poc_chatlist.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";
import { deepFind, hasClass } from "./poc_css.mjs";

const app = appPathArg();

// Перечень на сервере: стенд меняет его между открытиями списка, как это
// делают заведение разговора, соседняя вкладка и фоновая работа.
let chats = [
  { id: "aaaa1111-1111-1111-1111-111111111111", title: "старый разговор",
    project: "demo", state: "dead", idle: true },
];
const dropped = [];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    const m = /\/chats\/([^/]+)\/drop$/.exec(path);
    if (m) {
      dropped.push(m[1]);
      chats = chats.filter((c) => c.id !== m[1]);
      return { ok: true };
    }
    return {};
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  return {};
});

// Состояние панели читается один раз, как его читает сама панель, и дальше
// живёт в памяти вкладки: возврат в открытый разговор берёт её из пула готовой.
const board = { prefix: "XR", sections: [] };
const st = await sandbox.chatState("demo", "aaaa1111-1111-1111-1111-111111111111", board, []);
const head = sandbox.chatHead("demo", st);
const pick = deepFind(head, hasClass("cdpick"))[0];
if (!pick) fail("кнопки списка чатов в шапке нет: " + dump(head).slice(0, 200));

// Список открывается той же кнопкой, что у человека, и закрывается ею же.
const openList = async () => {
  if (deepFind(head, hasClass("cdrop"))[0]) {
    pick.handlers.click({ stopPropagation: () => {} });
    await settle();
  }
  pick.handlers.click({ stopPropagation: () => {} });
  await settle();
  const box = deepFind(head, hasClass("cdrop"))[0];
  if (!box) fail("список чатов не открылся: " + dump(head).slice(0, 200));
  return box;
};

// --- новый разговор виден без перезагрузки ---
{
  chats = chats.concat([{ id: "bbbb2222-2222-2222-2222-222222222222",
    title: "новый чат", project: "demo", state: "live", idle: true }]);
  const box = await openList();
  if (!dump(box).includes("новый чат")) {
    fail("нового разговора в списке нет, а перезагрузки быть не должно: " +
      dump(box).slice(0, 300));
  }
}

// --- свежий заголовок доезжает до открытого списка ---
{
  chats = chats.map((c) => (c.id.startsWith("bbbb") ?
    Object.assign({}, c, { title: "разбор очереди слияния" }) : c));
  const box = await openList();
  if (!dump(box).includes("разбор очереди слияния")) {
    fail("свежий заголовок до списка не доехал: " + dump(box).slice(0, 300));
  }
}

// --- разговор, заведённый на стороне, тоже виден ---
{
  chats = chats.concat([{ id: "cccc3333-3333-3333-3333-333333333333",
    title: "чат с телефона", project: "demo", state: "live", idle: true }]);
  const box = await openList();
  if (!dump(box).includes("чат с телефона")) {
    fail("разговора, заведённого на стороне, в списке нет: " + dump(box).slice(0, 300));
  }
}

// --- закрытая крестиком запись уходит сразу ---
{
  // Незачатая запись зовётся в списке «Новый чат»: своих слов в ней ещё нет.
  chats = chats.concat([{ id: "dddd4444-4444-4444-4444-444444444444",
    project: "demo", state: "dead", idle: true, blank: true }]);
  const box = await openList();
  if (!deepFind(box, hasClass("cdrop-x"))[0]) {
    fail("незачатой записи в списке нет: " + dump(box).slice(0, 300));
  }
  const shut = deepFind(box, hasClass("cdrop-x"))[0];
  if (!shut) fail("крестика у пустой записи нет: " + dump(box).slice(0, 300));
  shut.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!dropped.includes("dddd4444-4444-4444-4444-444444444444")) {
    fail("закрытие не ушло на сервер: " + JSON.stringify(dropped));
  }
  const live = deepFind(head, hasClass("cdrop"))[0] || box;
  if (deepFind(live, hasClass("cdrop-x"))[0]) {
    fail("закрытая запись осталась в списке: " + dump(live).slice(0, 300));
  }
}

console.log("ок: список читается заново каждым открытием, поэтому новый чат, " +
  "свежий заголовок и чужой разговор видны без перезагрузки, а закрытая " +
  "запись уходит сразу");

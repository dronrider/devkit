// Стенд курсора в списке разговоров (POC DK-397, ветка poc-chat).
//
// Живой случай: список чатов открывался с курсором в поле поиска, и на телефоне
// вслед за курсором выезжала клавиатура. Она закрывает пол-экрана раньше, чем
// человек успел взглянуть на список, и первым его движением становится не
// выбор разговора, а уборка клавиатуры (замечание пользователя).
//
// На ноутбуке курсор к месту: клавиатура там стоит на столе и ничего не
// закрывает, а руки уже на ней. Поэтому предмет стенда это разница по ширине, а
// не снятый курсор: порог берётся тот же, каким отличается весь мобильный вид.
//
// Зовётся: node testdata/poc_chatfocus.mjs static/app.js

import { makeSandbox, makeNode, settle, tag, focused, focusDrop, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "своя задача", sect: "in-progress" },
] }] };
const chats = [
  { id: "aaaa1111-1111", project: "demo", title: "разговор по XR-1",
    mtime: "2026-08-13T10:00:00+03:00", state: "dead", tasks: ["XR-1"] },
  { id: "bbbb2222-2222", project: "demo", title: "свободный разговор",
    mtime: "2026-08-12T10:00:00+03:00", state: "dead", tasks: [] },
];

const { sandbox } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models: [] };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});

sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);
await settle();

const wide = () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} });
const phone = (q) => ({ matches: String(q).includes("max-width:900px"),
  addEventListener: () => {}, removeEventListener: () => {} });

// Список открывается заново на каждой ширине: курсор ставится при сборке, и
// повторное открытие это ровно то движение, которым его видит человек.
function openDrop() {
  focusDrop();
  const anchor = makeNode("div");
  sandbox.chatDropOpen("demo", st, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  return { drop, find: tag(drop, "INPUT") };
}

// --- ноутбук: курсор встаёт в поле поиска, как и раньше ---
{
  sandbox.window.matchMedia = wide;
  const { find } = openDrop();
  if (!find) fail("в списке разговоров нет поля поиска вовсе");
  if (focused() !== find) {
    fail("на широком экране курсор не встал в поле поиска: набирать пришлось бы вторым движением");
  }
}

// --- телефон: курсора в поле нет, и клавиатура не выезжает ---
{
  sandbox.window.matchMedia = phone;
  const { drop, find } = openDrop();
  if (!find) fail("на телефоне у списка разговоров пропало поле поиска");
  if (focused() === find) {
    fail("на телефоне курсор всё же встал в поле поиска: клавиатура закроет список");
  }
  // Курсор не переехал на соседний узел списка: экран открывается без него
  // вовсе, а не отдаёт его первой попавшейся кнопке.
  if (focused()) {
    fail("на телефоне курсор ушёл на другой узел списка: " + String(focused().className));
  }
  if (!drop.children.length) fail("список разговоров на телефоне собрался пустым");
}

console.log("poc_chatfocus: ok");

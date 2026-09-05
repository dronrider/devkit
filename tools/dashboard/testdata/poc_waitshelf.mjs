// Стенд полки ждущих (DK-696): место, где видно всех, кто ждёт ответа, и
// дорога от строки полки до самого разговора. Проверяется число на кнопке
// шапки, состав строки (проект, задача, вопрос текстом, давность молчания),
// порядок строк тем же, каким его прислал сервер, переход в разговор своего и
// чужого проекта, парковка отдельной дорогой на строку доски (приёмка
// человека, 2026-09-05: DK-565), а также два молчаливых случая: пустая полка
// и нечитаемая доска.
//
// Зовётся: node testdata/poc_waitshelf.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const now = Math.floor(Date.now() / 1000);

// Порядок записей выбирает сервер, дольше всех ждущий первым. Клиент его не
// пересортировывает: правило одно, и второй раз оно разъезжалось бы.
const items = [
  { project: "demo", id: "XR-8", title: "Слияние встало", addr: "aaa-1",
    waiting: { state: "ждёт ответа", source: "ask", note: "спросил агент",
      questions: ["куда катить дальше"], since: now - 1800, session: "aaa-1" } },
  { project: "other", id: "ZZ-7", title: "Чужая доска", addr: "bbb-2",
    waiting: { state: "ждёт ответа", source: "ask", note: "спросил агент",
      questions: ["брать ли вторую подписку"], since: now - 300, session: "bbb-2" } },
  { project: "demo", id: "XR-007", title: "Припаркована вопросом", addr: "XR-007",
    waiting: { state: "припаркована вопросом", source: "parked", note: "парковка",
      questions: ["чинить сейчас или строкой"] } },
];

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path.startsWith("/api/waiting")) return { items, errors: [] };
  return {};
});

sandbox.location.hash = "#demo";

// --- список приезжает машинной ручкой, а не проектной ---
await sandbox.refreshWaits();
await settle();

const btn = byId.get("waits");
const num = byId.get("waits-n");
if (!num || num.textContent !== "3" || num.hidden) {
  fail("число ждущих на кнопке шапки не встало: " + JSON.stringify({
    text: num && num.textContent, hidden: num && num.hidden }));
}
if (!String(btn.title).includes("3 ждут ответа")) {
  fail("подпись кнопки не называет ждущих: " + btn.title);
}

// --- полка открывается с любого экрана и держит все доски разом ---
const host = makeNode("div");
sandbox.waitShelfOpen(btn, host);
await settle();
const shelf = byClass(host, "wshelf");
if (!shelf) fail("полка не открылась: " + dump(host).slice(0, 200));
const rows = allByClass(shelf, "wsrow");
if (rows.length !== 3) fail("на полке " + rows.length + " строк, ожидал три");

const said = dump(shelf).replace(/\s+/g, " ");
for (const want of ["demo", "other", "XR-8", "ZZ-7", "куда катить дальше",
  "брать ли вторую подписку", "припаркована вопросом", "спросил агент"]) {
  if (!said.includes(want)) fail("на полке нет «" + want + "»: " + said.slice(0, 400));
}
// Вопрос стоит текстом в самой строке: за ним сюда и приходят, и открывать
// разговор ради одной фразы человек не должен.
if (!allByClass(shelf, "wsq").length) fail("вопроса текстом в строке полки нет");
// Давность молчания видна словами: без неё полка не говорит, кому отвечать
// первым, а порядок строк один этого не объясняет.
if (!said.includes("без ответа")) fail("давность молчания на полке не названа: " + said.slice(0, 300));

// --- порядок сервера сохранён: дольше всех ждущий первым ---
if (!dump(rows[0]).includes("XR-8")) fail("первой на полке не дольше всех ждущая строка");
if (!dump(rows[2]).includes("XR-007")) fail("парковка ушла не в конец полки");

// --- строка ведёт в разговор ждущего ---
rows[0].handlers.click({});
await settle();
if (sandbox.location.hash !== "#demo/chat/aaa-1") {
  fail("строка своего проекта увела не в разговор ждущей сессии: " + sandbox.location.hash);
}
if (byClass(host, "wshelf")) fail("полка осталась открытой после перехода");

// --- парковка ведёт на строку доски, а не в чат: вопрос лежит в причине
// блока самой строки, а не в ленте разговора, и открывать там нечего ---
rows[2].handlers.click({});
await settle();
if (String(sandbox.location.hash).replace(/^#/, "") !== "demo/XR-007") {
  fail("парковка увела не на строку доски: " + sandbox.location.hash);
}
if (byClass(host, "wshelf")) fail("полка осталась открытой после перехода по парковке");

// --- чужой проект едет в самом адресе, доску менять не нужно ---
sandbox.location.hash = "#demo";
sandbox.waitShelfOpen(btn, host);
await settle();
const again = allByClass(byClass(host, "wshelf"), "wsrow");
again[1].handlers.click({});
await settle();
if (!String(sandbox.location.hash).includes("other~bbb-2")) {
  fail("разговор чужой доски открылся без проекта в адресе: " + sandbox.location.hash);
}

// --- пустая полка и нечитаемая доска молчат по-разному ---
const quiet = makeSandbox(app, (path) => {
  if (path.startsWith("/api/waiting")) {
    return { items: [], errors: ["demo: taskctl list --json: доска не читается"] };
  }
  return {};
});
quiet.sandbox.location.hash = "#demo";
await quiet.sandbox.refreshWaits();
await settle();
const quietNum = quiet.byId.get("waits-n");
if (!quietNum.hidden) fail("на пустой полке горит число: " + quietNum.textContent);
const quietHost = makeNode("div");
quiet.sandbox.waitShelfOpen(quiet.byId.get("waits"), quietHost);
await settle();
const quietSaid = dump(byClass(quietHost, "wshelf")).replace(/\s+/g, " ");
if (!quietSaid.includes("никто не ждёт ответа")) {
  fail("пустая полка молчит без слов: " + quietSaid.slice(0, 300));
}
if (!quietSaid.includes("доска не прочиталась")) {
  fail("отказ доски на полке не назван, и пустота читается тишиной: " + quietSaid.slice(0, 300));
}

console.log("poc_waitshelf: полка собирает ждущих всех досок, вопрос виден текстом, " +
  "порядок сервера сохранён, строка ведёт в разговор ждущей сессии, парковка ведёт на строку " +
  "доски, чужой проект едет в адресе, пустая полка и нечитаемая доска говорят разное");

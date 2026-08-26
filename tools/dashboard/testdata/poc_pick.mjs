// Стенд пикера мест экрана (требование пользователя: человек тычет в место
// вместо описания словами, а исполнитель находит это место в app.js грепом по
// говорящим классам).
//
// Предмет проверки: кнопка включает и выключает режим, наведение подсвечивает
// элемент, нажатие гасится и кладёт описатель, панель разговора из зоны выбора
// исключена, фишки снимаются крестиком, Esc выходит, а в реплику уезжает
// структурный блок со своим видом.
//
// Зовётся: node testdata/poc_pick.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, allByClass, tag, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const chat = { id: "aaaa1111-1111", project: "demo", title: "разбор замечаний",
  state: "live", idle: true, tasks: ["XR-1"] };
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };

let sent = null;
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST" && path.endsWith("/say")) {
    sent = JSON.parse(init.body || "{}");
    return { way: "say", tmux: "chat-1" };
  }
  if (path.includes("/sessions/")) return { items: [], total: 0, start: true };
  if (path.includes("/chats")) return { chats: [chat], models: [], days: 3, older: false };
  return {};
});

sandbox.location.hash = "#demo/board";
const st = { project: "demo", addr: "aaaa1111-1111", sid: "aaaa1111-1111",
  chats: [chat], entry: chat, models: [] };
const panel = sandbox.chatPanel("demo", st);
await settle();

const doc = sandbox.document;
const row = byClass(panel, "crow");
const clips = byClass(panel, "cclips");
const pickBtn = (byClass(panel, "crow").children || []).find(
  (n) => String((n.attrs && n.attrs.title) || n.title || "").includes("место на экране"));
if (!pickBtn) fail("кнопки пикера нет в строке отправки: " + dump(row).slice(0, 300));

// Место на экране: строка задачи со своим ID в data-атрибуте, внутри карточки.
const card = makeNode("section");
card.className = "card card-backlog";
const body = makeNode("div");
body.className = "tbody";
const trow = makeNode("div");
trow.className = "trow task";
trow.dataset.task = "XR-7";
trow.id = "row-XR-7";
trow.textContent = "XR-7 строка задачи с рангом слева";
body.append(trow);
card.append(body);
trow.parentNode = body;
body.parentNode = card;
// closest у мока ищет по своему классу: панели над строкой доски нет.
trow.closest = () => null;

const fire = (name, ev) => {
  const bag = doc.handlers[name];
  for (const fn of Array.isArray(bag) ? bag : [bag].filter(Boolean)) fn(ev);
};

// --- выключенный режим ничего не ловит ---
{
  fire("click", { target: trow, preventDefault: () => fail("выключенный пикер гасит нажатия"),
    stopPropagation: () => {} });
  if (allByClass(clips, "cpickchip").length) fail("выключенный пикер набрал место");
}

// --- включение: наведение подсвечивает, нажатие гасится и кладёт описатель ---
pickBtn.handlers.click({ stopPropagation: () => {} });
if (!String(pickBtn.className).split(" ").includes("on")) {
  fail("включённый пикер не виден по кнопке: " + pickBtn.className);
}
fire("mouseover", { target: trow });
if (!String(trow.className).split(" ").includes("pickhi")) {
  fail("наведение не подсветило элемент: " + trow.className);
}
let swallowed = false;
let stopped = false;
fire("click", { target: trow, preventDefault: () => { swallowed = true; },
  stopPropagation: () => { stopped = true; } });
if (!swallowed || !stopped) fail("нажатие не погашено: сработает само место под курсором");
const chip = allByClass(clips, "cpickchip");
if (chip.length !== 1) fail("описатель не встал фишкой: " + dump(clips).slice(0, 300));
const said = dump(chip[0]) + " " + String(chip[0].children.map((n) => n.title || "").join(" "));
for (const want of ["trow", "XR-7"]) {
  if (!said.includes(want)) fail("в описателе нет «" + want + "»: " + said.slice(0, 300));
}

// --- панель разговора из зоны выбора исключена ---
{
  const inPanel = makeNode("div");
  inPanel.className = "cbox";
  inPanel.closest = (sel) => (String(sel) === "#cpanel" ? inPanel : null);
  fire("click", { target: inPanel, preventDefault: () => fail("пикер ловит нажатия в самой панели"),
    stopPropagation: () => {} });
  if (allByClass(clips, "cpickchip").length !== 1) fail("панель попала в набор мест");
}

// --- крестик снимает фишку, Esc выходит из режима ---
{
  const off = byClass(chip[0], "cclipx");
  if (!off) fail("у фишки места нет крестика: " + dump(chip[0]));
  fire("keydown", { key: "Escape" });
  if (String(pickBtn.className).split(" ").includes("on")) fail("Esc не вышел из режима выбора");
  if (String(trow.className).split(" ").includes("pickhi")) fail("выход не снял подсветку");
  off.handlers.click({ stopPropagation: () => {} });
  if (allByClass(clips, "cpickchip").length) fail("крестик не снял фишку места");
}

// --- описатель уезжает в реплику структурным блоком своего вида ---
{
  pickBtn.handlers.click({ stopPropagation: () => {} });
  fire("click", { target: trow, preventDefault: () => {}, stopPropagation: () => {} });
  const ta = tag(panel, "TEXTAREA");
  ta.value = "вот тут ранг не читается";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sent || !sent.text) fail("реплика не ушла: " + JSON.stringify(sent));
  if (!/^<picked screen="demo\/board">/.test(sent.text)) {
    fail("блок мест уехал не своим видом: " + sent.text.slice(0, 200));
  }
  if (!sent.text.includes("data-task=XR-7") || !sent.text.includes("div.trow.task")) {
    fail("в блоке нет описателя места: " + sent.text.slice(0, 300));
  }
  if (!sent.text.includes("внутри div.tbody < section.card.card-backlog")) {
    fail("в описателе нет цепочки родителей: " + sent.text.slice(0, 300));
  }
  if (!sent.text.includes("вот тут ранг не читается")) {
    fail("слова человека потерялись из реплики: " + sent.text.slice(0, 300));
  }
  if (allByClass(clips, "cpickchip").length) fail("после отправки фишки остались в строке");
  if (String(pickBtn.className).split(" ").includes("on")) {
    fail("после отправки экран остался в режиме выбора");
  }
}

console.log("ok: пикер включается кнопкой, подсвечивает наведённое, гасит нажатие, " +
  "снимает описатель с цепочкой родителей и ID задачи, фишки снимаются, блок уезжает своим видом");

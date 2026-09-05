// Стенд полосы действий формы задачи и признака живого разговора (третья
// приёмка DK-716).
//
// Живой случай: человек зашёл в чат по строке в работе, попросил продолжить,
// агент пошёл работать. В списке не изменилось ничего, перезагрузка не помогла,
// а на форме той же задачи не было ни кнопки выполнения, ни кнопки
// продолжения. Слово человека про итог: «мутно как-то».
//
// Предмет стенда:
//   полоса действий формы не пустеет на секции In progress и даёт ту же
//   кнопку, что строка списка при тех же данных;
//   чужой ход гасит кнопку с причиной вместо пустого места;
//   свой разговор за строкой уводит продолжение в тот же разговор;
//   живой разговор виден точкой у строки, чипом на форме и подсказкой кнопки
//   запуска, а самой кнопки не отбирает (защита DK-460).
//
// Зовётся: node testdata/poc_rowtalk.mjs static/app.js

import { makeSandbox, settle, dump, byClass, makeNode, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const calls = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  const way = init && init.method ? init.method : "GET";
  if (way !== "GET") calls.push({ way, path, body: init.body ? JSON.parse(init.body) : null });
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return { id: "XR-9", message: "готово" };
});
await settle();

// Строка в работе, за которой дашборд не видит ни сессии, ни разговора.
const plain = { id: "XR-9", title: "строка в работе", sect: "in-progress" };
// Та же строка, за которой остались наши кончившиеся сессии: чату есть что
// показать, и продолжение идёт в него.
const talked = { ...plain, run: "gone" };
// Ход идёт в чужой сессии: снимать нечем, продолжать некуда.
const foreign = { ...plain, run: "session", run_busy: true, run_state: "busy" };
// По строке идёт живой разговор, а работы за ней никто не заявлял: ровно то,
// что человек видел на XR-034.
const talking = { ...plain, talk_state: "busy", talk_chat: "sid-живой" };

const main = (box) => byClass(box, "rmain");
// Полоса действий формы приходит списком узлов, и кнопка запуска лежит в нём
// внутри обёртки: собираем их в коробку и спрашиваем кнопку у неё.
const box = (nodes) => {
  const holder = makeNode("div");
  holder.append(...nodes);
  return holder;
};
const barBtn = (nodes) => byClass(box(nodes), "btn");
const press = (node) => node.handlers.click({ stopPropagation: () => {} });
const last = () => calls[calls.length - 1];
const words = (node) => [dump(node), node.title || "", node.attrs["aria-label"] || ""].join(" ");

// --- полоса действий формы не пустеет и совпадает со строкой списка ---
//
// Прежде ветка секции «в работе» возвращала пустой набор кнопок безусловно:
// расчёт был на кнопку продолжения в панели разговора, а до неё от пустого
// места ни подсказки, ни намёка.
for (const row of [plain, talked, foreign]) {
  const act = barBtn(sandbox.taskActions("demo", row.id, row));
  if (!act) fail("полоса действий формы пуста: " + JSON.stringify(row));
  const listed = main(sandbox.rowAction("demo", row, row.sect));
  if (!listed) fail("в списке пропала кнопка работы: " + JSON.stringify(row));
  // Оба экрана обещают одно: подпись действия и то, доступна ли кнопка.
  if (!words(act).includes("Продолжить")) {
    fail("форма назвала действие иначе, чем список: " + words(act));
  }
  if (Boolean(act.disabled) !== Boolean(listed.disabled)) {
    fail("форма и список разошлись доступностью кнопки: " + JSON.stringify(row));
  }
}

// Чужой ход: кнопка стоит погашенной с причиной, а не пропадает.
{
  const act = barBtn(sandbox.taskActions("demo", foreign.id, foreign));
  if (!act.disabled) fail("форма предложила продолжение поверх чужого хода");
  if (!String(act.title || "").includes("не наша")) {
    fail("погашенная кнопка формы молчит о причине: " + act.title);
  }
}

// Свой разговор за строкой: продолжение уходит в него же, а не конвейером.
{
  calls.length = 0;
  press(barBtn(sandbox.taskActions("demo", talked.id, talked)));
  await settle();
  if (!last() || last().way !== "POST" || !last().path.includes("/tasks/XR-9/continue")) {
    fail("продолжение с формы пошло не той ручкой: " + JSON.stringify(calls));
  }
}

// --- живой разговор виден, а кнопку запуска не отбирает ---
{
  const dot = sandbox.rowDot("demo", talking);
  if (!dot || !String(dot.className || "").includes("sd-talk")) {
    fail("строка молчит о живом разговоре: " + (dot ? dot.className : "точки нет"));
  }
  if (!String(dot.title || "").includes("Работой строки это не считается")) {
    fail("точка разговора выдаёт его за работу: " + dot.title);
  }
  // С точки есть дорога в сам разговор: посмотреть, чем занят агент, человек
  // идёт именно туда.
  press(dot);
  if (!sandbox.location.hash.includes("sid-живой")) {
    fail("точка разговора ведёт не в разговор: " + sandbox.location.hash);
  }
  const chip = sandbox.talkChip(talking);
  if (!chip || !dump(chip).includes("разговор идёт")) {
    fail("форма задачи молчит о живом разговоре: " + (chip ? dump(chip) : "чипа нет"));
  }
  // Кнопка запуска на месте, и нажатие поднимает исполнителя конвейером:
  // разговор работы за собой не заявлял, и отбирать у строки запуск нельзя
  // (защита DK-460, развилка 1).
  const listed = main(sandbox.rowAction("demo", talking, "in-progress"));
  if (!listed || listed.disabled) fail("живой разговор отобрал у строки кнопку запуска");
  if (!String(listed.title || "").includes("поднимет исполнителя рядом с ним")) {
    fail("кнопка запуска молчит о живом разговоре: " + listed.title);
  }
  calls.length = 0;
  press(listed);
  await settle();
  if (!last() || last().way !== "POST" || !last().path.endsWith("/runs")) {
    fail("запуск при живом разговоре пошёл не той ручкой: " + JSON.stringify(calls));
  }
  const act = barBtn(sandbox.taskActions("demo", talking.id, talking));
  if (!act || act.disabled) fail("живой разговор опустошил полосу действий формы");
  if (!String(act.title || "").includes("поднимет исполнителя рядом с ним")) {
    fail("кнопка формы молчит о живом разговоре: " + act.title);
  }
}

// --- ждущий разговор не удваивает чип ожидания ---
//
// Оба чипа сказали бы одно и то же разными словами, а чип ожидания знает
// больше: источник вопроса и срок.
{
  const asking = { ...plain, talk_state: "waiting", talk_chat: "sid-живой" };
  if (!sandbox.talkChip(asking)) fail("ждущий разговор пропал с формы вовсе");
  const waited = { ...asking, waiting: { state: "ждёт ответа", note: "спросил агент" } };
  if (sandbox.talkChip(waited)) {
    fail("рядом с чипом ожидания встал второй про то же: " + dump(sandbox.talkChip(waited)));
  }
}

console.log("poc_rowtalk: ok");

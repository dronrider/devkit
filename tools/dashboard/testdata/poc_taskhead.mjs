// Стенд шапки экрана задачи (ветка poc-chat).
//
// Одно и то же стояло на экране по два и по три раза. Номер задачи: припиской
// в шапке страницы, мелким в крошках и крупно над заголовком. Ссылка на доску:
// названием проекта в шапке и первой крошкой «Доска devkit». Состояние строки
// («In progress») стояло третьей крошкой, отдельно от полосы, где живут тип,
// цена и бакет. Под полосой действий висела плашка с пересказом устройства:
// «Конвейер получит заказ «Выполни DK-452» в tmux-сессии task-DK-452. Поедет
// на такую-то подписку» (разбор пользователя).
//
// Предмет стенда: номер стоит один раз, дорога на доску одна и живёт названием
// проекта в шапке, состояние строки стоит первым чипом полосы, а пересказов
// устройства на экране нет ни одного. Заказ при этом не потерян: он остался
// подсказкой самой кнопки, где его и спрашивают.
//
// Зовётся: node testdata/poc_taskhead.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Стили читаются тем же стендом: кегль ссылки живёт только в них, а мок
// разметки о них не знает вовсе.
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "..", "static", "style.css"), "utf8");
const html = readFileSync(join(here, "..", "static", "index.html"), "utf8");
const cssRule = (sel) => {
  const at = css.indexOf(sel + "{");
  if (at < 0) return "";
  return css.slice(at + sel.length + 1, css.indexOf("}", at));
};

const app = appPathArg();

const row = {
  id: "XR-6", title: "шапка экрана задачи", type: "task", cost: "M",
  p: "P2", r: 30, r_parts: [25, 3, 1, 0, 1], sect: "backlog", section: "Backlog",
  order: "Выполни XR-6",
};

const harnesses = [
  { name: "подписка-раз", bin: "one", default: true, models: [{ tier: "pro", model: "opus" }] },
  { name: "подписка-два", bin: "two", models: [{ tier: "pro", model: "sonnet" }] },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: row.id, row, after: [], blocks: [],
      file: "docs/tasks/XR-6.md", text: "# XR-6\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
// Узлы шапки берутся у самого дерева, а не из памяти стенда: карта заполняется
// по первому запросу из app.js, и до первой отрисовки их там ещё нет.
const pname = () => sandbox.document.getElementById("pname");
const psub = () => sandbox.document.getElementById("psub");

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

await settle();
await sandbox.loadHarnesses();
await go("#demo/XR-6");

const said = dump(groups).replace(/\s+/g, " ");

// --- крошек нет вовсе ---
if (byClass(groups, "crumb")) {
  fail("крошки вернулись на экран задачи: " + dump(byClass(groups, "crumb")).slice(0, 200));
}
if (said.includes("Доска demo")) {
  fail("ссылка на доску вернулась в тело экрана: " + said.slice(0, 300));
}

// --- номер стоит один раз ---
{
  const nums = allByClass(groups, "idbig").concat(allByClass(groups, "idsm"));
  if (nums.length !== 1 || nums[0].textContent !== "XR-6") {
    fail("номер задачи стоит " + nums.length + " раз(а): " +
      JSON.stringify(nums.map((n) => n.textContent)));
  }
  // Приписки в шапке нет вовсе, ни узла в разметке, ни присвоений в статике:
  // номер задачи стоял в ней третьим разом, а на прочих экранах она
  // пересказывала подсвеченный таб (решение пользователя).
  if (html.includes('id="psub"')) fail("узел приписки вернулся в разметку шапки");
  if (psub().textContent !== "") {
    fail("приписка шапки страницы снова что-то говорит: " + psub().textContent);
  }
}

// --- шапка это вход на доску, и это ссылка, а не заголовок ---
{
  // Слова называют переход, а не место: подробности дороги и стрелки разбирает
  // свой стенд, poc_headback.
  if (dump(pname()).replace(/\s+/g, " ").trim() !== "Назад на доску") {
    fail("шапка экрана задачи читается не «Назад на доску»: " + dump(pname()));
  }
  // Кегль ссылки: заголовком прежним кеглем она забирала первую строку экрана
  // под слово, которое человек и так знает (замечание пользователя). Порог
  // тринадцать точек, это кегль подписей шапки.
  const link = cssRule(".bhead h2.hgo");
  if (!link) fail("правила ссылки на доску в стилях нет");
  // Своего кегля у правила может и не быть, и тогда ссылка наследует кегль
  // заголовка: считается именно тот, каким её увидит человек.
  const px = (rule) => Number((/(?:^|;)\s*font(?:-size)?:[^;]*?(\d+(?:\.\d+)?)px/.exec(rule) || [])[1]);
  const size = px(link) || px(cssRule(".bhead h2"));
  if (!size) fail("кегль ссылки на доску не считается ни из её правила, ни из заголовка");
  if (size > 13) fail("ссылка на доску набрана кеглем " + size + ": это заголовок, а не ссылка");
  // Черта живёт под словами, а не под всей ссылкой: протянутая заодно и под
  // стрелкой, она читалась зачёркнутым значком.
  const words = cssRule(".bhead h2.hgo .hgot");
  if (!words.includes("text-decoration:underline")) {
    fail("ссылка на доску ничем не показывает, что она ведёт: " + JSON.stringify(words));
  }
  if (!String(pname().className).split(" ").includes("hgo")) {
    fail("заголовок шапки не помечен как ведущий: " + pname().className);
  }
  pname().handlers.click({});
  await settle();
  if (String(sandbox.location.hash).replace("#", "") !== "demo") {
    fail("нажатие на название проекта увело не на доску: " + sandbox.location.hash);
  }
  await go("#demo/XR-6");
}

// --- состояние строки стоит первым чипом полосы ---
{
  const chips = byClass(groups, "tchips");
  if (!chips) fail("полосы чипов на экране задачи нет: " + said.slice(0, 300));
  const first = chips.children[0];
  if (!first || first.textContent !== "Backlog") {
    fail("первым в полосе чипов стоит не состояние строки: " +
      dump(chips).replace(/\s+/g, " ").slice(0, 200));
  }
  // Тип с ценой и бакетом остались там же, следом за состоянием.
  const band = dump(chips).replace(/\s+/g, " ");
  for (const want of ["тип", "цена", "P2"]) {
    if (!band.includes(want)) fail("из полосы чипов пропало «" + want + "»: " + band.slice(0, 200));
  }
}

// --- пересказов устройства на экране нет ---
{
  for (const gone of ["Конвейер получит заказ", "tmux-сессии task-XR-6",
    "Поедет на подписка-раз", "подписок на машине", "headless-сессия конвейера"]) {
    if (said.includes(gone)) fail("на экране задачи осталась плашка «" + gone + "»: " +
      said.slice(0, 400));
  }
  // Заказ не потерян: его называет подсказка самой кнопки запуска.
  const run = allByClass(groups, "btn").find((b) => String(b.title).includes("Выполни XR-6"));
  if (!run) {
    fail("заказ пропал и с подсказки кнопки: " +
      JSON.stringify(allByClass(groups, "btn").map((b) => b.title)));
  }
}

console.log("poc_taskhead: ok");

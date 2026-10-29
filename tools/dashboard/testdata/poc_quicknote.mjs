// Стенд главной после DK-1503: набранная мысль переживает перерисовку экрана
// и перезагрузку страницы, главная возвращается с прежним обзором, а карточки
// главной несут вертикальные отступы.
//
// Мысль проверяется по видимому полю, а не по памяти: перерисовка главной
// собирает поле заново, и стенд судит о том, что человек увидит после
// возврата к вкладке. Перезагрузка моделируется вторым подъёмом app.js с той
// же памятью браузера, какой её оставила первая страница. Обзор проверяется
// возвратом с доски: прежние строки остаются на месте, свежие приезжают фоном,
// а пустой заглушки на экране нет. Отступы это правила стилей по классу
// колонки главной: раскладку мок дерева не считает.
//
// Зовётся: node testdata/poc_quicknote.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg, focused }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "..", "static", "style.css"), "utf8");
const app = appPathArg();
const cssRule = (sel) => {
  const at = css.indexOf(sel + "{");
  if (at < 0) return "";
  return css.slice(at + sel.length + 1, css.indexOf("}", at));
};

const projects = [{ name: "demo", prefix: "XR", sections: { backlog: 2 }, works: [] }];
const board = { sections: [{ key: "backlog", rows: [
  { id: "XR-1", title: "строка доски", sect: "backlog" },
] }] };
const ovRow = (id, title) => ({ project: "demo", id, title, r: 30 });
// Свежая строка обзора включается флагом: до него всякий обход отвечает
// прежним составом, и возврат на главную обязан показать прежние данные сразу,
// а свежие докатить фоном после своего обхода.
let ovFresh = false;

const reply = (path, init) => {
  if (path === "/api/projects") return { projects };
  if (path === "/api/overview") {
    const top = [ovRow("XR-21", "прежняя вершина")];
    if (ovFresh) top.push(ovRow("XR-22", "свежая вершина"));
    return { top, overdue: [], today: [], milestones: [], goals: [], drafts: [], done: [] };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  if (path === "/api/notifications") return { items: [] };
  if (init && init.method === "POST") return { id: "XR-901" };
  return {};
};

const first = makeSandbox(app, reply);
const groups = first.byId.get("groups");
await settle();

const qtext = () => byClass(groups, "qtext");
const qcard = () => byClass(groups, "quickcard");

// --- поле записи есть и держит фокус при наборе ---
if (!qcard()) fail("карточки быстрой записи на главной нет");
if (!qtext()) fail("поля записи в карточке нет");
{
  const field = qtext();
  field.focus();
  if (focused() !== field) fail("поле записи не приняло фокус");
  field.value = "мысль о выкате главной";
  if (!field.handlers.input) fail("набор в поле записи никуда не записывается");
  field.handlers.input();
  if (first.store.get("devkit.quick.draft") !== "мысль о выкате главной") {
    fail("набранная мысль не легла в память браузера: " +
      JSON.stringify(Object.fromEntries(first.store)));
  }
}

// --- обновление доски не затирает набор ---
await first.sandbox.refresh();
await settle();
{
  const field = qtext();
  if (!field) fail("после обновления поле записи пропало");
  if (field.value !== "мысль о выкате главной") {
    fail("обновление доски затёрло набор: " + JSON.stringify(field.value));
  }
  if (focused() !== field) fail("перерисовка главной уронила фокус поля записи");
}

// --- перезагрузка страницы возвращает мысль в поле ---
{
  const again = makeSandbox(app, (path) => {
    if (path === "/api/projects") return { projects };
    if (path === "/api/overview") {
      return { top: [], overdue: [], today: [], milestones: [], goals: [], drafts: [], done: [] };
    }
    if (path.endsWith("/board")) return { board, works: [] };
    return {};
  }, { store: Object.fromEntries(first.store) });
  await settle();
  const field = byClass(again.byId.get("groups"), "qtext");
  if (!field || field.value !== "мысль о выкате главной") {
    fail("после перезагрузки страницы мысль не вернулась в поле: " +
      JSON.stringify(field && field.value));
  }
}

// --- удачная запись очищает и поле, и память ---
{
  const save = deepBtn(qcard(), "Записать в накопитель");
  if (!save) fail("кнопки записи в накопитель нет");
  save.handlers.click();
  await settle();
  if (qtext().value !== "") fail("удачная запись не очистила поле");
  if (first.store.get("devkit.quick.draft")) {
    fail("удачная запись оставила мысль в памяти браузера");
  }
}

// --- обзор приезжает на главную и не пустует ---
{
  const said = dump(groups);
  for (const want of ["Верх по рангу", "XR-21"]) {
    if (!said.includes(want)) fail("обзор не приехал на главную: нет «" + want + "»");
  }
}

// --- возврат на главную держит прежний обзор, свежий докатывается фоном ---
{
  first.sandbox.location.hash = "demo";
  await first.sandbox.refresh();
  await settle();
  if (!dump(groups).includes("XR-1")) fail("доска проекта не открылась");
  first.sandbox.location.hash = "";
  await first.sandbox.refresh();
  // Картина сразу после отрисовки: прежняя вершина на месте, заглушки нет.
  const painted = dump(groups).replace(/\s+/g, " ");
  if (!painted.includes("XR-21")) {
    fail("возврат на главную открыл её без прежнего обзора: " + painted.slice(0, 300));
  }
  if (painted.includes("обзор собирается")) {
    fail("возврат на главную показал заглушку вместо прежнего обзора");
  }
  // Свежий обход приносит новую вершину, и она доезжает до открытой главной.
  ovFresh = true;
  await first.sandbox.refresh();
  await settle();
  if (!dump(groups).includes("XR-22")) {
    fail("свежая строка обзора не доехала до главной фоном");
  }
}

// --- карточки главной несут вертикальные отступы ---
{
  if (!cssRule(".groups.home")) fail("правила отступов колонки главной нет в стилях");
  const card = cssRule(".groups.home > .card + .card");
  if (!card || !card.includes("margin-top")) {
    fail("соседние карточки главной не несут отступ сверху: " + JSON.stringify(card));
  }
  if (!/var\(--(sp\d|fgap)\)/.test(card)) {
    fail("отступ карточек главной пишет своё число вместо лестницы: " + JSON.stringify(card));
  }
  if (!String(groups.className).split(" ").includes("home")) {
    fail("колонка главной не помечена классом home: " + groups.className);
  }
  // Прежняя заглушка обзора рисовала виджеты с пустыми списками и врала
  // «очередь пуста», пока сбор шёл: строка обязана не возвращаться.
  const src = readFileSync(app, "utf8");
  if (src.includes('ovGrid({ errors: ["обзор ещё не собран"] })')) {
    fail("заглушка обзора снова рисует виджеты с пустыми списками");
  }
}

// --- прочие экраны не наследуют раскладку главной ---
{
  first.sandbox.location.hash = "demo";
  await first.sandbox.refresh();
  await settle();
  if (String(groups.className).split(" ").includes("home")) {
    fail("доска проекта унаследовала класс главной: " + groups.className);
  }
}

console.log("poc_quicknote: ok");

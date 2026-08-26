// Стенд главной (ветка poc-chat): чем заводят задачу с общей страницы и что
// помнит экран про выбранный проект.
//
// Предмет проверки два: на главной больше нет полосы проектных кнопок, а
// заведение живёт плюсом у карточки проекта и разворачивается меню из двух
// дорог; и выбранный проект переживает уход на главную, откуда раздел «Доска»
// ведёт обратно на него, а не на первый проект списка.
//
// Зовётся: node testdata/poc_home.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const projects = [
  { name: "alpha", prefix: "AL", sections: { backlog: 2 }, works: [] },
  { name: "beta", prefix: "BT", sections: { backlog: 1 }, works: [] },
];
const board = { sections: [{ key: "backlog", rows: [
  { id: "BT-1", title: "первая строка беты", sect: "backlog" },
] }] };

const app = appPathArg();
const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

const groups = byId.get("groups");
await settle();

// Что заводится, видно по самой форме: переключателя над ней больше нет, вид
// выбран до неё (пунктом меню плюса либо экраном выбора), и форма собрана
// только своими полями. Различает их кнопка сохранения.
function madeAs() {
  if (deepBtn(groups, "Сохранить и грумить")) return "Черновик";
  if (deepBtn(groups, "Завести задачу")) return "Задача";
  return "формы нет";
}

// --- на главной нет проектных кнопок ---
await go("");
{
  const said = dump(groups);
  for (const gone of ["Новая задача", "Черновики"]) {
    if (said.includes(gone)) {
      fail("проектная кнопка осталась на главной: " + gone + " в " + said.slice(0, 300));
    }
  }
  if (byClass(groups, "nbar")) fail("полоса проектных кнопок на главной осталась");
}

// --- у каждой карточки свой плюс, и он разворачивает два пункта ---
{
  const rows = allByClass(groups, "prow");
  if (rows.length !== projects.length) {
    fail("карточек проектов не столько, сколько проектов: " + rows.length);
  }
  for (const row of rows) {
    if (!byClass(row, "pplus")) fail("у карточки нет плюса: " + dump(row));
  }
  const plus = byClass(rows[1], "pplus");
  if (!String(plus.title).includes("beta")) {
    fail("плюс не называет свой проект: " + plus.title);
  }
  plus.handlers.click({ stopPropagation: () => {} });
  const menu = byClass(rows[1], "pmenu");
  if (!menu) fail("плюс не развернул меню");
  const opts = allByClass(menu, "pmrow").map((o) => o.textContent);
  // Черновик стоит первым: мысль записывают чаще, чем заводят готовую строку.
  if (opts.join("|") !== "Черновик|Задача") {
    fail("в меню не два ожидаемых пункта: " + JSON.stringify(opts));
  }
  // Повторное нажатие складывает меню обратно, а не собирает второе.
  plus.handlers.click({ stopPropagation: () => {} });
  if (byClass(rows[1], "pmenu")) fail("повторное нажатие не сложило меню");
}

// --- пункты ведут на заведение своего проекта, черновик переключателем ---
{
  const rows = allByClass(groups, "prow");
  const plus = byClass(rows[1], "pplus");
  plus.handlers.click({ stopPropagation: () => {} });
  const opts = allByClass(byClass(rows[1], "pmenu"), "pmrow");
  opts[0].handlers.click({ stopPropagation: () => {} });
  await settle();
  // Плюс на карточке ведёт сразу в свою форму: вид выбран самим пунктом меню,
  // и переспрашивать его вторым экраном незачем.
  if (sandbox.location.hash.replace(/^#/, "") !== "beta/new/draft") {
    fail("черновик увёл не на заведение беты: " + sandbox.location.hash);
  }
  await sandbox.refresh();
  await settle();
  if (madeAs() !== "Черновик") fail("форма открылась задачей, а не черновиком: " + madeAs());
}
{
  await go("");
  const rows = allByClass(groups, "prow");
  const plus = byClass(rows[0], "pplus");
  plus.handlers.click({ stopPropagation: () => {} });
  const opts = allByClass(byClass(rows[0], "pmenu"), "pmrow");
  opts[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "alpha/new/task") {
    fail("задача увела не на заведение альфы: " + sandbox.location.hash);
  }
  await sandbox.refresh();
  await settle();
  if (madeAs() !== "Задача") fail("форма открылась черновиком, а не задачей: " + madeAs());
}

// --- заход на главную не сбрасывает выбранный проект ---
{
  await go("beta");
  if (!dump(groups).includes("BT-1")) fail("доска беты не собралась: " + dump(groups).slice(0, 200));
  await go("");
  byId.get("nav-board").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "beta") {
    fail("возврат в «Доску» открыл не последний проект: " + sandbox.location.hash);
  }
}

console.log("poc_home: ok");

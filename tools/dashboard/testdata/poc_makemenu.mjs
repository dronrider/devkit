// Стенд входов в заведение (ветка poc-chat).
//
// Заводить дашборд умеет двумя видами: черновик и строку доски. Спрашивает он
// это выпадашкой у самой кнопки, и такая кнопка есть в трёх местах: значок в
// шапке доски, плюс у карточки проекта на главной и плавающий плюс телефона.
// Кнопка шапки выпадашку потеряла: она была прописана в общий список переходов
// («make-btn» ведёт на «/new») и открывала форму задачи сразу, а завести
// черновик с доски стало нечем вовсе (замечание пользователя).
//
// Предмет стенда: все входы в заведение открывают одно и то же меню из двух
// пунктов, каждый пункт ведёт в свою форму, и ни один вход не открывает форму
// сам.
//
// Зовётся: node testdata/poc_makemenu.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const projects = [{ name: "demo", prefix: "XR", works: [], sections: { backlog: 2 } }];

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
const click = (node) => { node.handlers.click({ stopPropagation: () => {}, preventDefault: () => {} }); };

// Меню лежит внутри той кнопки, у которой раскрыто, либо рядом с ней: ищется
// оно от корня экрана, потому что кнопки живут в разных местах дерева.
const menuIn = (node) => byClass(node, "pmenu");
const rows = (menu) => allByClass(menu, "pmrow").map((r) => r.textContent);

// Пункт меню по подписи: их всего два, и путать их нельзя.
const pick = (menu, label) => allByClass(menu, "pmrow").find((r) => r.textContent === label);

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Браузер на смену адреса поднимает hashchange, и переход живёт его
// обработчиком: в стенде его поднимает сам стенд, там же, где браузер.
const moved = async () => {
  sandbox.window.fire("hashchange", {});
  await settle();
};

// --- кнопка шапки: меню, а не форма ---
{
  await go("#demo");
  const btn = byId.get("make-btn");
  const was = sandbox.location.hash;
  click(btn);
  await settle();
  if (sandbox.location.hash !== was) {
    fail("кнопка шапки увела с доски, не спросив вида: " + sandbox.location.hash);
  }
  const menu = menuIn(btn) || menuIn(byId.get("bhead") || groups);
  if (!menu) fail("кнопка шапки не открыла меню заведения: " + dump(btn).slice(0, 300));
  const said = rows(menu);
  if (said.length !== 2 || !said.includes("Черновик") || !said.includes("Задача")) {
    fail("в меню кнопки шапки не два вида: " + JSON.stringify(said));
  }
  // Пункт ведёт сразу в свою форму, промежуточного экрана выбора нет.
  click(pick(menu, "Черновик"));
  await moved();
  if (!String(sandbox.location.hash).includes("demo/new/draft")) {
    fail("пункт «Черновик» увёл не на форму черновика: " + sandbox.location.hash);
  }
  if (!dump(groups).includes("Черновику доступен только груминг")) {
    fail("открылась не форма черновика: " + dump(groups).replace(/\s+/g, " ").slice(0, 300));
  }
}

// --- та же кнопка ведёт и на форму задачи ---
{
  await go("#demo");
  const btn = byId.get("make-btn");
  click(btn);
  await settle();
  click(pick(menuIn(btn), "Задача"));
  await moved();
  if (!String(sandbox.location.hash).includes("demo/new/task")) {
    fail("пункт «Задача» увёл не на форму задачи: " + sandbox.location.hash);
  }
  // Форма задачи узнаётся подписью своей кнопки и отсутствием пометки про
  // груминг: подсказка поля живёт в placeholder, а он в текст не попадает.
  const said = dump(groups).replace(/\s+/g, " ");
  if (!said.includes("Завести задачу") || said.includes("Черновику доступен только груминг")) {
    fail("открылась не форма задачи: " + said.slice(0, 300));
  }
}

// --- повторное нажатие закрывает меню, а не собирает второе ---
{
  await go("#demo");
  const btn = byId.get("make-btn");
  click(btn);
  await settle();
  if (!menuIn(btn)) fail("меню не раскрылось");
  click(btn);
  await settle();
  if (menuIn(btn)) fail("повторное нажатие не закрыло меню кнопки шапки");
}

// --- плюс карточки проекта на главной: то же меню ---
{
  await go("#");
  const plus = byClass(groups, "pplus");
  if (!plus) fail("на главной нет плюса у карточки проекта: " + dump(groups).slice(0, 300));
  click(plus);
  await settle();
  const menu = menuIn(groups);
  if (!menu) fail("плюс карточки не открыл меню заведения");
  const said = rows(menu);
  if (said.length !== 2) fail("в меню плюса не два вида: " + JSON.stringify(said));
  click(pick(menu, "Черновик"));
  await moved();
  if (!String(sandbox.location.hash).includes("demo/new/draft")) {
    fail("плюс карточки увёл не на форму черновика: " + sandbox.location.hash);
  }
}

// --- плавающий плюс телефона: то же меню ---
{
  await go("#demo");
  const fab = byClass(groups, "fab");
  if (!fab) fail("на доске нет плавающего плюса: " + dump(groups).slice(0, 300));
  click(fab);
  await settle();
  const menu = menuIn(fab) || menuIn(groups);
  if (!menu) fail("плавающий плюс не открыл меню заведения");
  if (rows(menu).length !== 2) fail("в меню плавающего плюса не два вида: " + JSON.stringify(rows(menu)));
}

console.log("poc_makemenu: ok");

// Стенд выбора при заведении (ветка poc-chat).
//
// Кнопка заведения открывала одну форму на оба случая: поля черновика стояли в
// ней вперемешку с полями строки доски, а гашеные чипы объясняли, чего у
// черновика нет («каша полей», замечание пользователя). Теперь кнопка
// раскрывает выпадашку с двумя пунктами, и выбор ведёт сразу в свою форму;
// отдельного экрана выбора нет, он стоил лишнего перехода. Предмет стенда:
// меню у кнопки, две формы со своими полями, запись в свою ручку и поведение
// меню на узком экране.
//
// Зовётся: node testdata/poc_makepick.mjs static/app.js

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const posted = [];
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-9", message: "заведено" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const said = () => dump(groups).replace(/\s+/g, " ");
const hashNow = () => sandbox.location.hash.replace(/^#/, "");

// --- кнопка заведения раскрывает меню, а не экран выбора ---
{
  await go("#demo");
  // На доске заведение живёт плавающим плюсом (на телефоне он же главная
  // дорога), в накопителе кнопкой со словами: меню у обеих одно и то же.
  const btn = byClass(byId.get("bmain") || byId.get("groups"), "fab") ||
    deepBtn(byId.get("groups"), "Новая задача");
  if (!btn) fail("кнопки заведения на доске нет: " + said().slice(0, 200));
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
  const menu = byClass(btn.parentNode, "pmenu");
  if (!menu) fail("кнопка заведения не развернула меню");
  const opts = allByClass(menu, "pmrow").map((o) => o.textContent);
  if (opts.join("|") !== "Черновик|Задача") {
    fail("в меню не два ожидаемых пункта: " + JSON.stringify(opts));
  }
  // Промежуточного экрана выбора нет вовсе: адрес не меняется, пока пункт не
  // выбран.
  if (hashNow() !== "demo") fail("кнопка увела с доски до выбора: " + hashNow());
  if (byClass(byId.get("groups"), "mkrow")) fail("экран выбора вернулся: " + said().slice(0, 200));

  // Пункт ведёт сразу в свою форму.
  allByClass(menu, "pmrow")[0].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (hashNow() !== "demo/new/draft") fail("черновик увёл не в свою форму: " + hashNow());
}

// --- меню закрывается тремя путями и держится в границах узкого экрана ---
{
  await go("#demo");
  const btn = byClass(byId.get("bmain") || byId.get("groups"), "fab") ||
    deepBtn(byId.get("groups"), "Новая задача");
  const open = () => {
    btn.handlers.click({ stopPropagation: () => {} });
    return byClass(btn.parentNode, "pmenu");
  };
  // Второе нажатие по той же кнопке.
  if (!open()) fail("меню не раскрылось");
  if (open()) fail("второе нажатие меню не закрыло");
  // Клик мимо.
  if (!open()) fail("меню не раскрылось второй раз");
  sandbox.document.handlers.click({ target: byId.get("groups") });
  await settle();
  if (byClass(btn.parentNode, "pmenu")) fail("клик мимо меню не закрыл");
  // Escape.
  if (!open()) fail("меню не раскрылось третий раз");
  sandbox.document.handlers.keydown({ key: "Escape", target: {} });
  await settle();
  if (byClass(btn.parentNode, "pmenu")) fail("Escape меню не закрыл");

  // Узкий экран: меню держится в границах и пункт крупнее под палец.
  const css = readFileSync(join(dirname(app), "style.css"), "utf8");
  if (!/\.pmenu\{[^}]*max-width:calc\(100vw/.test(css)) {
    fail("меню не ограничено шириной экрана: уедет за край на телефоне");
  }
  const narrow = css.slice(css.indexOf("@media (max-width:900px){", css.indexOf(".rcard{display:block}") - 400));
  if (!/\.pmrow\{padding:1[0-9]/.test(narrow.slice(0, 400))) {
    fail("на узком экране пункт меню не подрос под палец: " + narrow.slice(0, 200));
  }
  // Кнопка заведения на доске стоит с якорем: без него меню уедет к правому
  // краю экрана, а не встанет под кнопкой.
  if (!/\.bhead \.btn-acc\{position:relative\}/.test(css)) {
    fail("у кнопки заведения нет якоря для меню");
  }
}

// Поля формы ищутся по подписи для чтения с экрана: разделов четыре, лежат они
// одинаковыми узлами, и различать их классом было бы гаданием.
function fields(root) {
  const out = [];
  (function walk(node) {
    if (node.tagName === "TEXTAREA") out.push(node);
    for (const kid of node.children || []) if (typeof kid === "object") walk(kid);
  })(root);
  return out;
}

function write(root, label, text) {
  const area = fields(root).find((n) => String(n.attrs["aria-label"] || "") === label);
  if (!area) fail("поля «" + label + "» на форме нет");
  area.value = text;
  area.handlers.input({});
  return area;
}

// --- форма черновика несёт только свои поля ---
{
  await go("#demo/new/draft");
  const shown = said();
  if (byClass(groups, "accbox")) fail("в форме черновика стоит карточка приёмки: " + shown.slice(0, 300));
  if (byClass(groups, "rrow")) fail("в форме черновика стоят слагаемые ранга: " + shown.slice(0, 300));
  for (const gone of ["серьёзность", "вид приёмки", "барьер"]) {
    if (shown.includes(gone)) fail("в форме черновика поле задачи «" + gone + "»: " + shown.slice(0, 300));
  }
  // Разделы SCQA спрашиваются порознь, каждый своим полем с подсказкой: одним
  // полем человек писал в черновик простынёй (просьба пользователя).
  const areas = fields(groups);
  const heads = areas.map((n) => String(n.attrs["aria-label"] || ""));
  for (const want of ["ситуация черновика", "осложнение черновика", "вопрос черновика",
    "гипотеза черновика"]) {
    if (!heads.includes(want)) fail("в форме черновика нет поля «" + want + "»: " + JSON.stringify(heads));
  }
  for (const area of areas) {
    if (!String(area.placeholder || "").trim()) {
      fail("поле «" + area.attrs["aria-label"] + "» стоит без подсказки, что в нём писать");
    }
  }
  for (const want of ["Сохранить", "Сохранить и грумить"]) {
    if (!deepBtn(groups, want)) fail("кнопки «" + want + "» на форме черновика нет: " + shown.slice(0, 200));
  }
}

// --- форма задачи несёт свои поля и не говорит про груминг ---
{
  await go("#demo/new/task");
  const shown = said();
  for (const want of ["вид приёмки", "серьёзность"]) {
    if (!shown.includes(want)) fail("в форме задачи нет поля «" + want + "»: " + shown.slice(0, 300));
  }
  if (shown.includes("Ранга у черновика нет") || shown.includes("выдаст груминг")) {
    fail("в форме задачи осталась пометка черновика: " + shown.slice(0, 300));
  }
  if (!deepBtn(groups, "Завести задачу")) fail("кнопки заведения задачи нет: " + shown.slice(0, 200));
}

// --- запись уезжает в свою ручку ---
{
  await go("#demo/new/draft");
  const field = byClass(groups, "ftitle") || allByClass(groups, "ftitle")[0];
  // Обязательного в черновике трое: заголовок, ситуация и осложнение. Вопрос с
  // гипотезой уезжают пустыми, и записи это не мешает.
  write(groups, "заголовок черновика", "ссылка на черновик из чата не открывается");
  write(groups, "ситуация черновика", "ссылку из чата человек открывает вручную.");
  write(groups, "осложнение черновика", "разговор теряется, пока ищут запись.");
  await settle();
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.endsWith("/drafts")) {
    fail("черновик уехал не в свою ручку: " + JSON.stringify(posted));
  }
  if (last.body.type || last.body.r_parts) {
    fail("вместе с черновиком уехали поля задачи: " + JSON.stringify(last.body));
  }
  // Уезжает текст тем же порядком, каким его пишет taskctl draft: заголовок
  // первой строкой, дальше подразделы третьего уровня. Пустой раздел едет
  // пустым заголовком, а не пропадает.
  const want = "ссылка на черновик из чата не открывается\n\n" +
    "### Ситуация\n\nссылку из чата человек открывает вручную.\n\n" +
    "### Осложнение\n\nразговор теряется, пока ищут запись.\n\n" +
    "### Вопрос\n\n### Гипотеза\n";
  if (last.body.text !== want) {
    fail("черновик уехал не разделами SCQA: " + JSON.stringify(last.body.text));
  }
  void field;
}

console.log("poc_makepick: ok");

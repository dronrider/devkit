// Стенд формы заведения: разделы черновика и выход с формы (ветка poc-chat).
//
// Форма черновика спрашивала одно поле, и человек писал в него простынёй, а
// раскладывать её по SCQA приходилось потом грумеру. Дорога назад стояла на
// экране дважды: крошкой «Доска demo» над заголовком и той же дорогой в шапке
// страницы. Выхода у формы не было вовсе, ни крестика, ни отмены: передумав,
// человек оставался на ней (разбор пользователя).
//
// Предмет стенда: у черновика поле заголовка и четыре поля разделов с
// подсказками, записанное уезжает тем же порядком, каким пишет taskctl draft;
// пустые вопрос и гипотеза записи не мешают, а пустая ситуация мешает.
// Дублирующей крошки на форме нет ни у черновика, ни у задачи. Выход у формы
// виден рядом с записью, Escape закрывает пустую форму сразу, а набранная
// сперва спрашивает и уходит только по второму нажатию, туда же, откуда
// пришли: черновик в накопитель, задача на доску.
//
// Зовётся: node testdata/poc_newform.mjs static/app.js

import { makeSandbox, settle, dump, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const posted = [];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-D7", message: "Черновик XR-D7 записан" };
  }
  if (path.endsWith("/drafts")) return { drafts: [], works: [] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
const doc = sandbox.document;
const said = () => dump(groups).replace(/\s+/g, " ");

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Поля формы различаются подписью для чтения с экрана: своего класса у них
// нет, а лежат они одинаковыми узлами.
const areas = () => {
  const out = [];
  (function walk(node) {
    if (node.tagName === "TEXTAREA") out.push(node);
    for (const kid of node.children || []) if (typeof kid === "object" && kid) walk(kid);
  })(groups);
  return out;
};

const put = (label, text) => {
  const area = areas().find((n) => String(n.attrs["aria-label"] || "") === label);
  if (!area) fail("поля «" + label + "» на форме нет: " + said().slice(0, 300));
  area.value = text;
  area.handlers.input({});
  return area;
};

const press = (key) => { doc.handlers.keydown({ key, preventDefault: () => {} }); };
const quit = () => {
  const btn = deepBtn(groups, "bquit");
  if (!btn) fail("выхода с формы на экране нет: " + said().slice(0, 400));
  return btn;
};

await settle();

// --- у черновика заголовок и четыре раздела, у каждого своя подсказка ---
{
  await go("#demo/new/draft");
  const heads = areas().map((n) => String(n.attrs["aria-label"] || ""));
  for (const want of ["заголовок черновика", "ситуация черновика", "осложнение черновика",
    "вопрос черновика", "гипотеза черновика"]) {
    if (!heads.includes(want)) fail("на форме черновика нет поля «" + want + "»: " + JSON.stringify(heads));
  }
  for (const area of areas()) {
    if (!String(area.placeholder || "").trim()) {
      fail("поле «" + area.attrs["aria-label"] + "» стоит без подсказки, что в нём писать");
    }
  }
  // Разделы подписаны теми же словами, что стоят в файле черновика.
  for (const want of ["Ситуация", "Осложнение", "Вопрос", "Гипотеза"]) {
    if (!said().includes(want)) fail("раздел «" + want + "» на форме не подписан: " + said().slice(0, 400));
  }
}

// --- дублирующей дороги на доску на форме нет ---
{
  for (const hash of ["#demo/new/draft", "#demo/new/task"]) {
    await go(hash);
    if (said().includes("Доска demo")) {
      fail("дорога на доску вернулась в тело формы " + hash + ": " + said().slice(0, 300));
    }
  }
}

// --- ситуация с осложнением обязательны, вопрос с гипотезой нет ---
{
  await go("#demo/new/draft");
  put("заголовок черновика", "ссылка на черновик из чата не открывается");
  await settle();
  if (!deepBtn(groups, "Сохранить").disabled) {
    fail("черновик с одним заголовком записывается: ситуацию с осложнением никто не спросит");
  }
  put("ситуация черновика", "ссылку из чата человек открывает вручную.");
  put("осложнение черновика", "разговор теряется, пока ищут запись.");
  await settle();
  if (deepBtn(groups, "Сохранить").disabled) {
    fail("черновик с ситуацией и осложнением записываться отказывается: " +
      said().slice(0, 400));
  }
}

// --- записанное уезжает разделами, пустые разделы едут пустыми ---
{
  posted.length = 0;
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.endsWith("/drafts")) fail("запись уехала не в свою ручку: " + JSON.stringify(posted));
  const want = "ссылка на черновик из чата не открывается\n\n" +
    "### Ситуация\n\nссылку из чата человек открывает вручную.\n\n" +
    "### Осложнение\n\nразговор теряется, пока ищут запись.\n\n" +
    "### Вопрос\n\n### Гипотеза\n";
  if (last.body.text !== want) {
    fail("черновик уехал не тем порядком, каким пишет taskctl draft: " +
      JSON.stringify(last.body.text));
  }
}

// --- пустая форма закрывается сразу, и кнопкой, и клавишей ---
{
  await go("#demo/new/draft");
  if (!quit()) fail("выхода с формы черновика нет");
  quit().handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("выход с формы черновика увёл не в накопитель: " + sandbox.location.hash);
  }
  await go("#demo/new/task");
  press("Escape");
  await settle();
  if (String(sandbox.location.hash).replace("#", "") !== "demo") {
    fail("Escape с формы задачи увёл не на доску: " + sandbox.location.hash);
  }
}

// --- набранная форма сперва спрашивает ---
{
  await go("#demo/new/draft");
  put("заголовок черновика", "мысль с телефона");
  await settle();
  press("Escape");
  await settle();
  if (!String(sandbox.location.hash).includes("new")) {
    fail("Escape съел написанное без вопроса: " + sandbox.location.hash);
  }
  // Вопрос стоит словами на самой кнопке, а не в невидимом состоянии.
  if (!dump(quit()).includes("Черновик не записан, выйти?")) {
    fail("взведённый выход ничего не спрашивает: " + dump(quit()));
  }
  press("Escape");
  await settle();
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("второй Escape не увёл с формы: " + sandbox.location.hash);
  }
  // Уйдя, форма забывает написанное: следующее заведение начинается с чистой.
  await go("#demo/new/draft");
  const title = areas().find((n) => String(n.attrs["aria-label"] || "") === "заголовок черновика");
  if (String(title.value || "")) {
    fail("брошенная форма вернулась с прежним текстом: " + JSON.stringify(title.value));
  }
}

console.log("poc_newform: ok");

// Стенд формы заведения: разделы черновика и выход с формы (ветка poc-chat).
//
// Форма черновика спрашивала одно пустое поле, и человек писал в него
// простынёй, не вспоминая про разделы. Заход с полем на каждый раздел человек
// забраковал: «нужно было просто в поле редактирования вставить шаблон с
// разделами, а пользователь сам заполнит их, так гораздо гибче». Дорога назад
// стояла на экране дважды: крошкой «Доска demo» над заголовком и той же
// дорогой в шапке страницы. Выхода у формы не было вовсе, ни крестика, ни
// отмены: передумав, человек оставался на ней (разбор пользователя).
//
// Предмет стенда: поле у черновика одно, и в нём с самого начала лежит шаблон
// разделов; правленый рукой шаблон (снятый раздел, добавленный свой) уезжает
// как есть, и запись без части разделов проходит. Дублирующей крошки на форме
// нет ни у черновика, ни у задачи. Выход у формы виден рядом с записью, Escape
// закрывает нетронутую форму сразу, а набранная сперва спрашивает и уходит
// только по второму нажатию, туда же, откуда пришли: черновик в накопитель,
// задача на доску.
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

// --- поле одно, и в нём лежит шаблон разделов ---
{
  await go("#demo/new/draft");
  const fields = areas();
  if (fields.length !== 1) {
    fail("полей на форме черновика " + fields.length + ", а надо одно: " +
      JSON.stringify(fields.map((n) => n.attrs["aria-label"])));
  }
  const text = String(fields[0].value || "");
  for (const want of ["### Ситуация", "### Осложнение", "### Вопрос", "### Гипотеза"]) {
    if (!text.includes(want)) fail("в шаблоне поля нет раздела «" + want + "»: " + JSON.stringify(text));
  }
  // Первая строка пуста: она под заголовок, туда же встаёт курсор.
  if (text.split("\n", 1)[0] !== "") {
    fail("шаблон занял первую строку, а она под заголовок: " + JSON.stringify(text));
  }
  if (String(fields[0].attrs["aria-label"] || "") !== "текст черновика") {
    fail("поле подписано не текстом черновика: " + fields[0].attrs["aria-label"]);
  }
}

// --- шаблон принадлежит черновику и на форму задачи не уезжает ---
{
  await go("#demo/new/draft");
  await go("#demo/new/task");
  const said = String(areas()[0].value || "");
  if (said.includes("### Ситуация")) {
    fail("шаблон черновика встал заголовком задачи: " + JSON.stringify(said));
  }
  // А вернувшись на черновик, человек снова застаёт шаблон.
  await go("#demo/new/draft");
  if (!String(areas()[0].value || "").includes("### Ситуация")) {
    fail("шаблон не вернулся на форму черновика: " + JSON.stringify(areas()[0].value));
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

// --- запись без части разделов проходит, а без заголовка нет ---
{
  await go("#demo/new/draft");
  const field = areas()[0];
  if (!deepBtn(groups, "Сохранить").disabled) {
    fail("один шаблон без заголовка записывается: заголовка никто не спросит");
  }
  // Человек правит шаблон руками: заголовок написан, «Вопрос» с «Гипотезой»
  // сняты, а свой раздел дописан. Запись этого не отбивает.
  const said = "ссылка на черновик из чата не открывается\n\n" +
    "### Ситуация\n\nссылку из чата человек открывает вручную.\n\n" +
    "### Осложнение\n\nразговор теряется, пока ищут запись.\n\n" +
    "### Что уже пробовали\n\nпоиск по накопителю, не помогает.\n";
  field.value = said;
  field.handlers.input({});
  await settle();
  if (deepBtn(groups, "Сохранить").disabled) {
    fail("правленый рукой шаблон записываться отказывается: " + said);
  }

  // Уезжает написанное как есть, без потерь: ни снятый раздел, ни свой не
  // мешают, и порядок остаётся авторским.
  posted.length = 0;
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.endsWith("/drafts")) {
    fail("запись уехала не в свою ручку: " + JSON.stringify(posted));
  }
  if (last.body.text !== said.trim()) {
    fail("черновик уехал не тем, что человек написал: " + JSON.stringify(last.body.text));
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
  put("текст черновика", "мысль с телефона");
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
  const title = areas()[0];
  if (String(title.value || "").includes("мысль с телефона")) {
    fail("брошенная форма вернулась с прежним текстом: " + JSON.stringify(title.value));
  }
}

console.log("poc_newform: ok");

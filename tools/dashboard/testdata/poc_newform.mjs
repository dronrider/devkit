// Стенд формы заведения: шаблон разделов и выход с формы (ветка poc-chat,
// вид по макету DK-447).
//
// Форма черновика спрашивала одно пустое поле, и человек писал в него
// простынёй, не вспоминая про разделы. Заход с полем на каждый раздел человек
// забраковал: «нужно было просто в поле редактирования вставить шаблон с
// разделами, а пользователь сам заполнит их, так гораздо гибче». Дорога назад
// стояла на экране дважды: крошкой «Доска demo» над заголовком и той же
// дорогой в шапке страницы. Выхода у формы не было вовсе, ни крестика, ни
// отмены: передумав, человек оставался на ней (разбор пользователя).
//
// Предмет стенда: наименование с описанием стоят раздельными полями в рамках
// с подписью в верхней грани, и в описании с самого начала лежит шаблон
// разделов; правленый рукой шаблон (снятый раздел, добавленный свой) уезжает
// как есть, и запись без части разделов проходит. Дублирующей крошки на форме
// нет ни у черновика, ни у задачи. Выход у черновика это крестик в углу, у
// задачи кнопка в полосе действий; Escape закрывает нетронутую форму сразу, а
// набранная сперва спрашивает и уходит только по второму нажатию, туда же,
// откуда пришли: черновик в накопитель, задача на доску.
//
// Зовётся: node testdata/poc_newform.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

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

await settle();

// --- наименование с описанием раздельны, шаблон лежит в описании ---
{
  await go("#demo/new/draft");
  const fields = areas();
  const labels = fields.map((n) => String(n.attrs["aria-label"] || ""));
  if (labels.length !== 2 || labels[0] !== "наименование черновика" ||
    labels[1] !== "описание черновика") {
    fail("поля формы черновика не пара «наименование и описание»: " + JSON.stringify(labels));
  }
  if (String(fields[0].value || "") !== "") {
    fail("наименование не пустое при входе: " + JSON.stringify(fields[0].value));
  }
  const text = String(fields[1].value || "");
  for (const want of ["### Ситуация", "### Осложнение", "### Вопрос", "### Гипотеза"]) {
    if (!text.includes(want)) fail("в шаблоне описания нет раздела «" + want + "»: " + JSON.stringify(text));
  }
  // Подписи обоих полей стоят в верхней грани рамок, подсказок внутри полей
  // нет (макет DK-447).
  for (const want of ["Наименование черновика", "Описание"]) {
    if (!said().includes(want)) fail("подпись «" + want + "» не встала в рамку поля");
  }
  if (fields[0].placeholder || fields[1].placeholder) {
    fail("подсказка внутри поля черновика не снята");
  }
}

// --- шаблон принадлежит черновику и на форму задачи не уезжает ---
{
  await go("#demo/new/draft");
  await go("#demo/new/task");
  const taskTitle = String(areas()[0].value || "");
  if (taskTitle.includes("### Ситуация")) {
    fail("шаблон черновика встал наименованием задачи: " + JSON.stringify(taskTitle));
  }
  // А вернувшись на черновик, человек снова застаёт шаблон.
  await go("#demo/new/draft");
  if (!String(areas()[1].value || "").includes("### Ситуация")) {
    fail("шаблон не вернулся в описание черновика: " + JSON.stringify(areas()[1].value));
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

// --- запись без части разделов проходит, а без наименования нет ---
{
  await go("#demo/new/draft");
  // Один шаблон без наименования не записывается: кнопки гаснут, и записи
  // никто не спросит.
  if (!deepBtn(groups, "Сохранить").disabled) {
    fail("один шаблон без наименования записывается: наименования никто не спросит");
  }
  // Человек правит шаблон руками: наименование написано, «Вопрос» с
  // «Гипотезой» сняты, а свой раздел дописан. Запись этого не отбивает.
  const title = "ссылка на черновик из чата не открывается";
  const body = "### Ситуация\n\nссылку из чата человек открывает вручную.\n\n" +
    "### Осложнение\n\nразговор теряется, пока ищут запись.\n\n" +
    "### Что уже пробовали\n\nпоиск по накопителю, не помогает.\n";
  put("наименование черновика", title);
  put("описание черновика", body);
  await settle();
  if (deepBtn(groups, "Сохранить").disabled) {
    fail("правленый рукой шаблон записываться отказывается: " + body);
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
  if (last.body.title !== title || last.body.body !== body) {
    fail("черновик уехал не тем, что человек написал: " + JSON.stringify(last.body));
  }
}

// --- пустая форма закрывается сразу, и крестиком, и клавишей ---
{
  await go("#demo/new/draft");
  const x = deepBtn(groups, "xbtn");
  if (!x) fail("крестика выхода на форме черновика нет");
  x.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("крестик с пустой формы увёл не в накопитель: " + sandbox.location.hash);
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
  put("наименование черновика", "мысль с телефона");
  await settle();
  press("Escape");
  await settle();
  if (!String(sandbox.location.hash).includes("new")) {
    fail("Escape съел написанное без вопроса: " + sandbox.location.hash);
  }
  // Вопрос стоит карточкой под крестиком, а не в невидимом состоянии кнопки
  // (макет DK-447).
  const scrim = byClass(groups, "xscrim");
  if (!scrim || scrim.hidden || !dump(scrim).includes("Текст черновика будет утерян. Продолжить?")) {
    fail("взведённый выход ничего не спрашивает: " + (scrim ? dump(scrim) : "вопроса нет"));
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

// Стенд выбора при заведении (ветка poc-chat).
//
// Кнопка заведения открывала одну форму на оба случая: поля черновика стояли в
// ней вперемешку с полями строки доски, а гашеные чипы объясняли, чего у
// черновика нет («каша полей», замечание пользователя). Теперь нажатие сперва
// спрашивает, что заводят, и по выбору открывается своя форма. Предмет стенда:
// выбор виден, каждая форма несёт только свои поля, запись уезжает в свою
// ручку.
//
// Зовётся: node testdata/poc_makepick.mjs static/app.js

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

// --- нажатие заведения спрашивает, что заводят ---
{
  await go("#demo/new");
  const rows = allByClass(groups, "mkrow");
  if (rows.length !== 2) fail("выбора на экране нет: " + said().slice(0, 300));
  const names = rows.map((r) => (r.children[0] || {}).textContent);
  if (JSON.stringify(names) !== JSON.stringify(["Черновик", "Задача"])) {
    fail("двери названы не так: " + JSON.stringify(names));
  }
  // По каждой двери сказано, что за ней: человек выбирает со знанием, а не
  // наугад.
  if (!said().includes("SCQA") || !said().includes("метаданными")) {
    fail("выбор не объясняет, чем формы разные: " + said().slice(0, 300));
  }
  // Полей формы на экране выбора нет вовсе: слова про приёмку в подписи двери
  // это объяснение, а не поле, поэтому ищутся сами узлы формы.
  if (byClass(groups, "accbox") || byClass(groups, "rrow") || byClass(groups, "nfbody")) {
    fail("на экране выбора уже стоит форма: " + said().slice(0, 300));
  }
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
  // Подсказка поля говорит, как писать черновик, а не оставляет человека
  // гадать про четыре буквы. Стоит она в самом поле, поэтому читается у него.
  const ta = (function find(node) {
    if (node.tagName === "TEXTAREA") return node;
    for (const kid of node.children || []) {
      const got = typeof kid === "object" && find(kid);
      if (got) return got;
    }
    return null;
  })(groups);
  if (!ta || !String(ta.placeholder).includes("SCQA")) {
    fail("форма черновика молчит про SCQA: " + (ta ? ta.placeholder : "поля нет"));
  }
  if (byId.get("psub").textContent !== "новый черновик") {
    fail("шапка не назвала, что заводим: " + byId.get("psub").textContent);
  }
  if (!deepBtn(groups, "Записать черновик")) fail("кнопки записи черновика нет: " + shown.slice(0, 200));
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
  const area = (function find(node) {
    if (node.tagName === "TEXTAREA") return node;
    for (const kid of node.children || []) {
      const got = typeof kid === "object" && find(kid);
      if (got) return got;
    }
    return null;
  })(groups);
  if (!area) fail("поля текста в форме черновика нет");
  area.value = "ссылка на черновик из чата не открывается";
  area.handlers.input({});
  await settle();
  deepBtn(groups, "Записать черновик").handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.endsWith("/drafts")) {
    fail("черновик уехал не в свою ручку: " + JSON.stringify(posted));
  }
  if (last.body.type || last.body.r_parts) {
    fail("вместе с черновиком уехали поля задачи: " + JSON.stringify(last.body));
  }
  void field;
}

console.log("poc_makepick: ok");

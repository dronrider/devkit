// Стенд свежей строки и свежей записи (ветка poc-chat).
//
// Заведённое с формы человек ищет глазами в списке, а список у него прежний:
// доска и накопитель перерисовывались только по событию адреса, и, заводя со
// своего же экрана, приходилось обновлять страницу (замечание пользователя).
// Предмет стенда две дороги: строка задачи и запись накопителя. После записи
// экран сам идёт за свежими данными, новая строка стоит в списке и помечена,
// а метка держится ровно до следующей перерисовки.
//
// Зовётся: node testdata/poc_freshrow.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const rows = [{ id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-" }];
const drafts = [{ id: "XR-D1", title: "первая мысль", age_words: "вчера" }];
const asked = [];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  asked.push(((init && init.method) || "GET") + " " + path);
  if (init && init.method === "POST" && path.endsWith("/tasks")) {
    rows.push({ id: "XR-010", title: "свежая строка", sect: "backlog", r: 30, cost: "-" });
    return { id: "XR-010", file: "docs/tasks/XR-010.md", message: "строка XR-010 заведена" };
  }
  if (init && init.method === "POST" && path.endsWith("/drafts")) {
    drafts.push({ id: "XR-D2", title: "свежая мысль", age_words: "только что" });
    return { id: "XR-D2", file: "docs/tasks/drafts/XR-D2.md" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows: rows.slice() }] },
      works: [] };
  }
  if (path.endsWith("/drafts")) return { drafts: drafts.slice() };
  if (path.includes("/tasks/")) {
    const row = rows.find((r) => path.includes(r.id)) || {};
    return { row, text: "", file: "docs/tasks/" + (row.id || "x") + ".md" };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
const fill = (text) => {
  const area = tag(groups, "TEXTAREA") || tag(groups, "INPUT");
  if (!area) fail("на форме нет поля заголовка: " + dump(groups).slice(0, 200));
  area.value = text;
  if (area.handlers.input) area.handlers.input({});
  return area;
};

// Разделы черновика различаются подписью для чтения с экрана: своего класса у
// полей нет, а лежат они одинаковыми узлами.
const fillPart = (label, text) => {
  const area = (function find(node) {
    if (node.tagName === "TEXTAREA" && String(node.attrs["aria-label"] || "") === label) return node;
    for (const kid of node.children || []) {
      const got = typeof kid === "object" && kid && find(kid);
      if (got) return got;
    }
    return null;
  })(groups);
  if (!area) fail("на форме нет поля «" + label + "»: " + dump(groups).slice(0, 200));
  area.value = text;
  area.handlers.input({});
  return area;
};

// --- задача: после заведения экран сам идёт за свежими данными ---
// Вид заводимого стоит в адресе: заведение сперва спрашивает, что заводят, и
// форма открывается своя.
sandbox.location.hash = "#demo/new/task";
await sandbox.refresh();
await settle();
fill("свежая строка");
const save = deepBtn(groups, "Завести задачу");
if (!save) fail("кнопки заведения на форме нет: " + dump(groups).slice(0, 300));
const wasAsked = asked.length;
save.handlers.click({ stopPropagation: () => {} });
await settle();
const after = asked.slice(wasAsked);
if (!after.some((a) => a.startsWith("POST") && a.endsWith("/tasks"))) {
  fail("заведение не ушло на сервер: " + JSON.stringify(after));
}
// Свежие данные это те, что показывает экран после заведения: с формы он
// уходит на саму заведённую строку, и читается она своей ручкой. Доска сюда
// больше не входит: экрану задачи она не нужна, а на доске строка окажется
// свежей, когда человек на неё вернётся (это проверяет шаг ниже).
if (!after.some((a) => a.startsWith("GET") && a.includes("/tasks/XR-010"))) {
  fail("после заведения экран не пошёл за свежей строкой: " + JSON.stringify(after) +
    " (строку увидел бы только тот, кто обновил страницу)");
}

// --- строка стоит на доске и помечена свежей ---
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();
{
  const fresh = allByClass(groups, "fresh");
  if (!dump(groups).includes("XR-010")) {
    fail("свежей строки на доске нет: " + dump(groups).replace(/\s+/g, " ").slice(0, 400));
  }
  if (!fresh.length || !dump(fresh[0]).includes("XR-010")) {
    fail("свежая строка ничем не помечена: искать её глазами по всей очереди");
  }
}

// --- метка одноразовая: следующая перерисовка оставляет строку обычной ---
await sandbox.refresh();
await settle();
if (allByClass(groups, "fresh").length) {
  fail("метка свежей строки не снялась следующей перерисовкой");
}

// --- черновик: «Сохранить» само возвращает в накопитель ---
//
// Промежуточной карточки с кнопкой «В накопитель» между формой и списком
// больше нет (DK-370): запись уводит туда сама.
sandbox.location.hash = "#demo/new/draft";
await sandbox.refresh();
await settle();
fill("свежая мысль");
// Черновику мало заголовка: ситуация с осложнением обязательны, и без них
// запись отбивается самой формой.
fillPart("ситуация черновика", "замечено на прогоне.");
fillPart("осложнение черновика", "мешает работать.");
const write = deepBtn(groups, "Сохранить");
if (!write) fail("кнопки записи черновика нет: " + dump(groups).slice(0, 300));
write.handlers.click({ stopPropagation: () => {} });
await settle();

// --- запись стоит в накопителе и помечена свежей ---
{
  const shown = dump(groups).replace(/\s+/g, " ");
  if (!shown.includes("XR-D2")) fail("свежей записи в накопителе нет: " + shown.slice(0, 400));
  const fresh = allByClass(groups, "fresh");
  if (!fresh.length || !dump(fresh[0]).includes("XR-D2")) {
    fail("свежая запись ничем не помечена среди соседей по накопителю");
  }
  if (!byClass(groups, "ktabs")) fail("накопитель нарисован без полосы табов: " + shown.slice(0, 200));
}

console.log("poc_freshrow: ok");

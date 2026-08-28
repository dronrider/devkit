// Стенд пары кнопок на форме записи (DK-370, LLD DK-354 решение 5, ветка
// poc-chat).
//
// Прежде запись кончалась одной кнопкой «Записать черновик» и промежуточным
// экраном «Черновик записан» с кнопками «Записать ещё», «В накопитель» и «На
// доску». Кнопок теперь две, и расходятся они дорогой после записи:
// «Сохранить» возвращает в накопитель, «Сохранить и грумить» той же ручкой
// пишет запись, поднимает разбор и открывает экран записи. Предмет стенда это
// обе дороги, отсутствие промежуточного экрана и общий рубеж пары: у пустого
// черновика гаснут обе кнопки.
//
// Зовётся: node testdata/poc_savegroom.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const posted = [];
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push(path);
    if (path.endsWith("/drafts")) {
      return { id: "XR-D7", file: "docs/tasks/drafts/XR-D7.md", message: "черновик записан" };
    }
    if (path.includes("/groom")) return { message: "груминг поднят" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/drafts")) {
    return { drafts: [{ id: "XR-D7", title: "свежая мысль", written: "2026-08-26",
      age_days: 0, order: "Проведи груминг XR-D7" }] };
  }
  if (path.includes("/drafts/XR-D7")) {
    return { id: "XR-D7", file: "docs/tasks/drafts/XR-D7.md", text: "свежая мысль",
      order: "Проведи груминг XR-D7" };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Поля формы черновика: своего класса у них нет, различаются подписью для
// чтения с экрана. Полей теперь пять, заголовок и четыре раздела SCQA.
const area = (label) => (function find(node) {
  if (node.tagName === "TEXTAREA" &&
    (!label || String(node.attrs["aria-label"] || "") === label)) return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && kid && find(kid);
    if (got) return got;
  }
  return null;
})(groups);

const put = (label, text) => {
  const ta = area(label);
  if (!ta) fail("поля «" + label + "» на форме нет: " + dump(groups).slice(0, 300));
  ta.value = text;
  ta.handlers.input({});
};

// Записи черновика хватает заголовка с ситуацией и осложнением: вопрос и
// гипотеза бывают пустыми, и записи это не мешает.
const fill = async (text) => {
  put("заголовок черновика", text);
  put("ситуация черновика", text.trim() ? "замечено на прогоне." : text);
  put("осложнение черновика", text.trim() ? "мешает работать." : text);
  await settle();
};

// --- на форме записи стоят обе кнопки, старой одиночной нет ---
{
  await go("#demo/new/draft");
  await fill("мысль с телефона");
  if (deepBtn(groups, "Записать черновик")) {
    fail("одиночная кнопка записи осталась на форме: " + dump(groups).slice(0, 300));
  }
  for (const want of ["Сохранить", "Сохранить и грумить"]) {
    if (!deepBtn(groups, want)) {
      fail("кнопки «" + want + "» на форме записи нет: " +
        dump(groups).replace(/\s+/g, " ").slice(0, 400));
    }
  }
  // Подписей под формой нет вовсе: они пересказывали устройство и объясняли
  // кнопки, чьи подписи их и называют (замечание пользователя). Одна пометка
  // остаётся, про груминг: чего лишён черновик, глазами на форме не видно.
  const said = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["вернёт в накопитель", "поднимет разбор", "Ляжет в docs/tasks/drafts/",
    "Бакет считается"]) {
    if (said.includes(gone)) fail("на форме осталась подпись «" + gone + "»: " + said.slice(0, 400));
  }
  if (!said.includes("Черновику доступен только груминг")) {
    fail("форма молчит про то, чего лишён черновик: " + said.slice(0, 400));
  }
}

// --- пустой черновик гасит обе кнопки разом ---
{
  await fill("   ");
  const save = deepBtn(groups, "Сохранить");
  const groom = deepBtn(groups, "Сохранить и грумить");
  if (!save.disabled || !groom.disabled) {
    fail("у пустого черновика кнопки живые: сохранить " + save.disabled +
      ", грумить " + groom.disabled);
  }
  await fill("мысль с телефона");
  if (deepBtn(groups, "Сохранить").disabled || deepBtn(groups, "Сохранить и грумить").disabled) {
    fail("написанный черновик оставил кнопки погашенными");
  }
}

// --- «Сохранить» пишет запись и возвращает в накопитель ---
{
  posted.length = 0;
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length !== 1 || !posted[0].endsWith("/drafts")) {
    fail("запись уехала не в свою ручку: " + JSON.stringify(posted));
  }
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("«Сохранить» вернуло не в накопитель: " + sandbox.location.hash);
  }
  const said = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["Черновик XR-D7 записан", "Записать ещё", "В накопитель", "На доску"]) {
    if (said.includes(gone)) fail("промежуточный экран остался: нашлось «" + gone + "»");
  }
  if (!said.includes("XR-D7")) fail("записи нет в накопителе, куда увела кнопка: " + said.slice(0, 300));
}

// --- «Сохранить и грумить» пишет, поднимает разбор и уводит на экран записи ---
{
  await go("#demo/new/draft");
  await fill("вторая мысль с телефона");
  posted.length = 0;
  deepBtn(groups, "Сохранить и грумить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length !== 2) fail("пара «запись и разбор» ушла не двумя запросами: " +
    JSON.stringify(posted));
  if (!posted[0].endsWith("/drafts")) fail("первой ушла не запись: " + JSON.stringify(posted));
  if (!posted[1].endsWith("/drafts/XR-D7/groom")) {
    fail("разбор поднялся не той ручкой: " + JSON.stringify(posted));
  }
  if (!String(sandbox.location.hash).includes("demo/draft/XR-D7")) {
    fail("«Сохранить и грумить» увело не на экран записи: " + sandbox.location.hash);
  }
}

// --- форма задачи своей кнопкой не задета ---
{
  await go("#demo/new/task");
  if (!deepBtn(groups, "Завести задачу")) {
    fail("кнопка заведения задачи пропала: " + dump(groups).slice(0, 300));
  }
  if (deepBtn(groups, "Сохранить и грумить")) {
    fail("пара кнопок черновика встала на форму задачи");
  }
  void byClass;
}

console.log("poc_savegroom: ok");

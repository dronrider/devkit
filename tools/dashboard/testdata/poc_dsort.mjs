// Стенд порядка накопителя (DK-353, ветка poc-chat).
//
// Накопитель печатался тем же порядком, каким его отдаёт taskctl: по
// возрастанию ID, и записанное с телефона пять минут назад лежало последней
// строкой под тремя десятками старых. Предмет стенда это три вещи: свежая
// запись стоит первой строкой, кнопка порядка в шапке переставляет список без
// похода на сервер и счётчик записей при этом остаётся на месте, а выбранный
// порядок берётся из хранилища при первой же отрисовке.
//
// Зовётся: node testdata/poc_dsort.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Три записи разных дней: номера растут по времени записи, поэтому порядок по
// ID и порядок по дате в накопителе совпадают, а по заголовку расходятся с
// обоими.
const drafts = [
  { id: "XR-D1", title: "автономный выкат", written: "2026-08-10", age_days: 16,
    moved: "2026-08-10", order: "Проведи груминг XR-D1" },
  { id: "XR-D2", title: "ясли для сессий", written: "2026-08-18", age_days: 8,
    moved: "2026-08-18", order: "Проведи груминг XR-D2" },
  { id: "XR-D3", title: "экран записи не открывается", written: "2026-08-25", age_days: 1,
    moved: "2026-08-25", order: "Проведи груминг XR-D3" },
];

// Счёт походов за накопителем: переключение порядка не имеет права ходить на
// сервер, данных для него в уже полученном ответе хватает.
let heapCalls = 0;

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/drafts")) {
    heapCalls += 1;
    return { drafts };
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

// Порядок строк на экране: ID по колонке номера, сверху вниз.
const shownIds = () => allByClass(groups, "dsrow")
  .map((row) => {
    const id = byClass(row, "id");
    return id ? String(id.textContent) : "?";
  });

// --- первый заход: свежая запись стоит первой строкой ---
{
  await go("#demo/drafts");
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D3", "XR-D2", "XR-D1"])) {
    fail("накопитель открылся не свежими сверху: " + JSON.stringify(got));
  }
}

// --- шапка несёт и счётчик, и кнопку порядка ---
let sortBtn = null;
{
  const head = byClass(groups, "chd");
  if (!head) fail("шапки карточки накопителя нет: " + dump(groups).slice(0, 300));
  const cnt = byClass(head, "cnt");
  if (!cnt || !String(cnt.textContent).includes("3 записи")) {
    fail("счётчик записей ушёл из шапки: " + dump(head).replace(/\s+/g, " "));
  }
  sortBtn = byClass(head, "dsort");
  if (!sortBtn) fail("кнопки порядка в шапке накопителя нет: " + dump(head).replace(/\s+/g, " "));
  if (!String(sortBtn.textContent).includes("свежие сверху")) {
    fail("кнопка порядка молчит про своё положение: " + dump(sortBtn));
  }
  // Кнопка живёт в шапке, а не под списком: счётчик стоит перед ней.
  const kids = (head.children || []).map((k) => String(k.className || ""));
  if (kids.indexOf("cnt") > kids.indexOf("dsort")) {
    fail("кнопка порядка выдавила счётчик: " + JSON.stringify(kids));
  }
}

// --- переключение перестраивает список и на сервер не ходит ---
{
  const before = heapCalls;
  sortBtn.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (heapCalls !== before) {
    fail("переключение порядка сходило на сервер: походов " + (heapCalls - before));
  }
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("порядок по заголовку не встал: " + JSON.stringify(got));
  }
  const now = byClass(byClass(groups, "chd"), "dsort");
  if (!now || !String(now.textContent).includes("по заголовку")) {
    fail("кнопка не назвала новое положение: " + dump(now || {}));
  }
}

// --- выбор пережил обновление страницы ---
{
  const saved = sandbox.localStorage.getItem("devkit.dash.drafts.sort");
  if (saved !== "title") fail("выбор порядка не записался в хранилище: " + saved);
  // Обновление страницы это чтение хранилища при первой отрисовке: уход с
  // экрана и возврат на него собирают накопитель заново.
  await go("#demo");
  await go("#demo/drafts");
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("после возврата на экран порядок сбросился: " + JSON.stringify(got));
  }
}

// --- строка накопителя работает как прежде ---
{
  const row = allByClass(groups, "dsrow")[0];
  row.handlers.click({ target: row, stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).includes("/draft/XR-D1")) {
    fail("касание строки увело не на экран записи: " + sandbox.location.hash);
  }
  await go("#demo/drafts");
  // Кнопка разбора приходит с первой отметкой: разбирают накопитель пачкой.
  const pick = byClass(allByClass(groups, "dsrow")[0], "dpick");
  if (!pick) fail("отметки выбора в строке нет: " + dump(groups).replace(/\s+/g, " ").slice(0, 300));
  pick.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!deepBtn(groups, "Провести груминг")) {
    fail("кнопка разбора ушла с накопителя: " + dump(groups).replace(/\s+/g, " ").slice(0, 300));
  }
}

console.log("poc_dsort: ok");

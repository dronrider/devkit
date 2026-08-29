// Стенд порядка накопителя (DK-353, табличный вид DK-397, ветка poc-chat).
//
// Накопитель печатался тем же порядком, каким его отдаёт taskctl: по
// возрастанию ID, и записанное с телефона пять минут назад лежало последней
// строкой под тремя десятками старых. Порядок правила кнопка о двух
// положениях, а над списком стояло слово «Черновики» с числом, повторявшее
// таб. Теперь тем же местом занята шапка колонок: нажатие на подпись правит
// порядок, повторное разворачивает его, а по важности записи список прежде не
// строился вовсе.
//
// Предмет стенда: свежая запись стоит первой строкой, слова «Черновики» с
// кнопкой порядка на экране нет, шапка колонок несёт «Приоритет», «Номер»,
// «Задачу» и «Дату», нажатие переставляет список без похода на сервер,
// направление видно у выбранной колонки, выбор живёт в хранилище и переживает
// уход с экрана, а прежнее значение хранилища читается как колонка даты.
//
// Зовётся: node testdata/poc_dsort.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Три записи разных дней: номера растут по времени записи, поэтому порядок по
// ID и порядок по дате в накопителе совпадают, а по заголовку и по важности
// расходятся с обоими.
const drafts = [
  { id: "XR-D1", title: "автономный выкат", written: "2026-08-10", age_days: 16,
    moved: "2026-08-10", prio: "high", order: "Проведи груминг XR-D1" },
  { id: "XR-D2", title: "ясли для сессий", written: "2026-08-18", age_days: 8,
    moved: "2026-08-18", prio: "low", order: "Проведи груминг XR-D2" },
  { id: "XR-D3", title: "экран записи не открывается", written: "2026-08-25", age_days: 1,
    moved: "2026-08-25", prio: "mid", order: "Проведи груминг XR-D3" },
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

// Подпись колонки в шапке накопителя.
const col = (label) => allByClass(byClass(groups, "tblh") || {}, "tblb")
  .find((btn) => dump(btn).includes(label)) || null;

const click = async (btn) => {
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
};

// --- первый заход: свежая запись стоит первой строкой ---
{
  await go("#demo/drafts");
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D3", "XR-D2", "XR-D1"])) {
    fail("накопитель открылся не свежими сверху: " + JSON.stringify(got));
  }
}

// --- слова «Черновики» с кнопкой порядка над списком больше нет ---
{
  const said = dump(groups).replace(/\s+/g, " ");
  if (byClass(groups, "chd")) {
    fail("заголовок карточки накопителя остался на экране: " + said.slice(0, 300));
  }
  if (byClass(groups, "dsort")) {
    fail("кнопка порядка о двух положениях осталась на экране: " + said.slice(0, 300));
  }
  // Число записей никуда не делось, оно стоит баджем таба.
  const tabs = byClass(groups, "ktabs");
  if (!tabs || !dump(tabs).includes("3")) {
    fail("число записей ушло с таба вместе с заголовком: " + dump(tabs || {}));
  }
}

// --- шапка колонок вместо заголовка ---
{
  const head = byClass(groups, "tblh");
  if (!head) fail("шапки колонок у накопителя нет: " + dump(groups).slice(0, 300));
  if (!String(head.className).split(" ").includes("h-drafts")) {
    fail("шапка накопителя не своей раскладки: " + head.className);
  }
  for (const label of ["Приоритет", "Номер", "Задача", "Дата"]) {
    if (!col(label)) fail("в шапке накопителя нет колонки «" + label + "»: " + dump(head));
  }
  // Открытая колонка подсвечена и несёт значок направления: без него порядок
  // читается только перебором строк.
  const now = col("Дата");
  if (!String(now.className).split(" ").includes("tblon")) {
    fail("колонка, по которой стоит список, не подсвечена: " + now.className);
  }
  if (!tag(now, "I")) fail("направление порядка не показано значком: " + dump(now));
  const idle = col("Задача");
  if (String(idle.className).split(" ").includes("tblon") || tag(idle, "I")) {
    fail("значок направления стоит у колонки, по которой список не стоит: " + dump(idle));
  }
  // Ячеек в шапке столько же, сколько в строке, и отметка выбора своей
  // колонки не занимает: врозь с приоритетом они занимали две, и подпись
  // «Приоритет» переставала влезать (замечание пользователя).
  const row = allByClass(groups, "dsrow")[0];
  if ((head.children || []).length !== (row.children || []).length) {
    fail("колонок в шапке " + (head.children || []).length + ", а ячеек в строке " +
      (row.children || []).length + ": подписи встанут мимо");
  }
  if (!byClass(row.children[0], "dpick")) {
    fail("отметка выбора не стоит в одной колонке с приоритетом: " + dump(row.children[0]));
  }
}

// --- сортировка по важности: её прежде не было вовсе ---
{
  const before = heapCalls;
  await click(col("Приоритет"));
  if (heapCalls !== before) {
    fail("переключение порядка сходило на сервер: походов " + (heapCalls - before));
  }
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("список не встал по важности, высокие сверху: " + JSON.stringify(got));
  }
  if (!String(col("Приоритет").className).split(" ").includes("tblon")) {
    fail("шапка не назвала новую колонку: " + dump(byClass(groups, "tblh")));
  }
}

// --- повторное нажатие разворачивает порядок ---
{
  await click(col("Приоритет"));
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D2", "XR-D3", "XR-D1"])) {
    fail("повторное нажатие не развернуло порядок: " + JSON.stringify(got));
  }
}

// --- порядок по заголовку остался доступен ---
{
  await click(col("Задача"));
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("порядок по заголовку не встал: " + JSON.stringify(got));
  }
}

// --- выбор пережил обновление страницы ---
{
  const saved = sandbox.localStorage.getItem("devkit.dash.drafts.sort");
  if (saved !== "title:asc") fail("выбор порядка не записался в хранилище: " + saved);
  // Обновление страницы это чтение хранилища при первой отрисовке: уход с
  // экрана и возврат на него собирают накопитель заново.
  await go("#demo");
  await go("#demo/drafts");
  const got = shownIds();
  if (JSON.stringify(got) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("после возврата на экран порядок сбросился: " + JSON.stringify(got));
  }
}

// --- прежнее значение хранилища читается как колонка ---
// Кнопка о двух положениях писала туда слово, и человек, у которого в браузере
// лежит «fresh», обязан открыть накопитель свежими сверху, а не получить сброс
// на умолчание.
{
  sandbox.localStorage.setItem("devkit.dash.drafts.sort", "fresh");
  await go("#demo");
  await go("#demo/drafts");
  if (JSON.stringify(shownIds()) !== JSON.stringify(["XR-D3", "XR-D2", "XR-D1"])) {
    fail("прежнее «fresh» не прочиталось колонкой даты: " + JSON.stringify(shownIds()));
  }
  sandbox.localStorage.setItem("devkit.dash.drafts.sort", "title");
  await go("#demo");
  await go("#demo/drafts");
  if (JSON.stringify(shownIds()) !== JSON.stringify(["XR-D1", "XR-D3", "XR-D2"])) {
    fail("прежнее «title» не прочиталось колонкой заголовка: " + JSON.stringify(shownIds()));
  }
}

// --- строка накопителя работает как прежде ---
{
  await go("#demo/drafts");
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
  if (!deepBtn(groups, "Грумить")) {
    fail("кнопка разбора ушла с накопителя: " + dump(groups).replace(/\s+/g, " ").slice(0, 300));
  }
}

console.log("poc_dsort: ok");

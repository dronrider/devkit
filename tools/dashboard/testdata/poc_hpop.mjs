// Стенд горизонтали списка подписок (POC DK-397, ветка poc-chat).
//
// Живой случай: на форме задачи и черновика кнопка запуска переносится на
// второй ряд, когда ширины не хватает, и встаёт у левого края экрана. Список
// подписок висит правым краем на кнопке и растёт влево, и у главной части
// экрана свой overflow: вылезший за её край список обрезается ею, и с экрана
// его видно наполовину (замечание пользователя).
//
// Стороной это не решается: у телефона список шире свободного места и слева от
// кнопки, и справа, и верного ответа среди двух сторон нет вовсе. Предмет
// стенда в том, что горизонталь считается числом и зажимается в границы.
// Раскладки в моке нет, поэтому место называется размерами узлов: стенд
// подкладывает их сам, как это делает браузер. Настоящий движок меряет то же
// самое в TestStaticFormPopFits.
//
// Зовётся: node testdata/poc_hpop.mjs static/app.js

import { makeSandbox, settle, byClass, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const rows = [{ id: "XR-1", title: "запуск с выбором подписки", sect: "backlog", type: "task",
  cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24" }];

// Подписок на машине две: с одной выбирать нечего, и списка нет вовсе.
const harnesses = [{ name: "подписка-раз", default: true, tiers: ["pro", "max"] },
  { name: "подписка-два", tiers: ["pro"] }];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: "XR-1", row: rows[0], after: [], blocks: [],
      file: "docs/tasks/XR-1.md", text: "# XR-1\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works: [] };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path.endsWith("/works")) return { works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
});

const groups = byId.get("groups");
await settle();
await sandbox.renderTask("demo", [], "XR-1");
await settle();

const acts = byClass(groups, "tacts");
if (!acts) fail("на форме задачи нет места под действия: " + dump(groups).slice(0, 300));
const split = byClass(acts, "split");
if (!split) fail("у кнопки запуска нет узкой части выбора подписки: " + dump(acts));
const more = deepBtn(split, "more2");
const pop = byClass(split, "hpop");
if (!more || !pop) fail("стрелки выбора или списка подписок в разметке нет: " + dump(split));

// Зазор до края: тем же числом меряет себя app.js, и списку, прижатому вплотную
// к границе, человек читает обрезанный край.
const EDGE = 8;
const WIDTH = 340;
// Главная часть экрана начинается за боковой колонкой: всё, что левее, режется
// её границей. Список широкий, кнопка узкая.
const box = (left, right) => ({ left, right, top: 300, bottom: 330,
  width: right - left, height: 30 });
pop.getBoundingClientRect = () => ({ width: WIDTH, height: 220, left: 0, right: WIDTH,
  top: 0, bottom: 220 });

// Место кнопки называется тремя числами разом: где стоит стрелка, где начинается
// коробка составной кнопки (от неё считается style.left) и какой ширины окно.
function place(arrowLeft, arrowRight, hostLeft, screen, mainLeft) {
  more.getBoundingClientRect = () => box(arrowLeft, arrowRight);
  split.getBoundingClientRect = () => box(hostLeft, arrowRight);
  groups.getBoundingClientRect = () => ({ left: mainLeft, top: 60, right: screen, bottom: 900 });
  sandbox.document.documentElement.clientWidth = screen;
}

const open = () => {
  if (!pop.hidden) more.handlers.click({ stopPropagation: () => {} });
  more.handlers.click({ stopPropagation: () => {} });
};
// Куда список встал на самом деле: style.left считается от коробки составной
// кнопки, а границы экрана живут в общих координатах.
const shown = () => {
  const left = parseFloat(pop.style.left);
  if (!Number.isFinite(left)) return null;
  return left + split.getBoundingClientRect().left;
};

// --- кнопка у левого края телефона: список стоит в границах, а не левее их ---
{
  place(250, 280, 210, 390, 16);
  open();
  if (pop.hidden) fail("список подписок не раскрылся вовсе");
  const at = shown();
  if (at === null) fail("горизонталь списка не посчитана вовсе: " + JSON.stringify(pop.style));
  if (at < 16 + EDGE) {
    fail("список у левого края уходит под колонку меню: левый край на " + at);
  }
  if (at + WIDTH > 390 - EDGE) {
    fail("задвинутый вправо список вылез за правый край окна: правый край на " + (at + WIDTH));
  }
}

// --- места вдоволь: список висит правым краем на кнопке, как и раньше ---
{
  place(900, 930, 800, 1400, 208);
  open();
  const at = shown();
  if (at === null) fail("горизонталь списка не посчитана на широком экране");
  if (Math.round(at + WIDTH) !== 930) {
    fail("список посреди экрана съехал с правого края кнопки: правый край на " + (at + WIDTH));
  }
}

// --- кнопка посреди узкого экрана: не влезает ни слева, ни справа ---
{
  place(330, 360, 290, 390, 16);
  open();
  const at = shown();
  if (at === null) fail("горизонталь списка не посчитана на узком экране");
  if (at < 16 + EDGE || at + WIDTH > 390 - EDGE) {
    fail("список у кнопки посреди узкого экрана вышел за границы: " + at + ".." + (at + WIDTH));
  }
}

// --- мерить нечем: список остаётся там, куда его поставил стиль ---
{
  more.getBoundingClientRect = () => ({ left: 0, right: 0, top: 0, bottom: 0,
    width: 0, height: 0 });
  pop.getBoundingClientRect = () => ({ width: 0, height: 0, left: 0, right: 0,
    top: 0, bottom: 0 });
  open();
  if (pop.style.left) fail("без размеров узла список всё же переставлен: " + pop.style.left);
}

console.log("poc_hpop: ok");

// Стенд экрана доски (ветка poc-chat): два таба и полоска работ.
//
// Накопитель черновиков переехал с отдельного раздела меню во второй таб
// доски, а лента живых сессий над списком строк сменилась полоской с числом
// работ и дорогой в раздел «Агенты» (решение пользователя). Предмет стенда в
// том, что оба таба стоят на обоих экранах и переключают друг друга, старый
// адрес накопителя открывает свой таб, а полоска считает работы и уводит в
// раздел, не открывая разговоров.
//
// Зовётся: node testdata/poc_btabs.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const board = { sections: [
  { key: "in-progress", rows: [{ id: "XR-9", title: "идущая работа", sect: "in-progress" }] },
  { key: "backlog", rows: [{ id: "XR-1", title: "первая строка", sect: "backlog" }] },
] };
const drafts = [{ id: "XR-D1", title: "запись накопителя", file: "docs/tasks/drafts/XR-D1.md" }];
const works = [
  { id: "XR-1", kind: "task", via: "tmux", title: "первая строка", session: "aaaa1111" },
  { kind: "session", via: "session", session: "bbbb2222", note: "задача не узнана" },
];

const app = appPathArg();
const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path.endsWith("/board")) return { board, works };
  if (path.endsWith("/drafts")) return { drafts };
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Узлы берутся у самого мока: карта byId отдаёт только то, что уже создано, а
// полоса работ рождается первой отрисовкой.
const groups = sandbox.document.getElementById("groups");
const live = sandbox.document.getElementById("live");
await settle();

const tabs = () => {
  const bar = byClass(groups, "ktabs");
  if (!bar) fail("табов доски на экране нет: " + dump(groups).slice(0, 200));
  return allByClass(bar, "ktab");
};
const openTab = () => (tabs().find((t) => String(t.className).includes("onktab")) || {}).textContent;

// --- доска: два таба, открыт первый ---
await go("#demo");
{
  const names = tabs().map((t) => t.textContent);
  if (names.join("|") !== "Задачи|Черновики") {
    fail("табы доски не те: " + JSON.stringify(names));
  }
  if (openTab() !== "Задачи") fail("на доске подсвечен не тот таб: " + openTab());
  if (!dump(groups).includes("XR-1")) fail("строки доски не собрались");
}

// --- переход во второй таб открывает накопитель по своему адресу ---
{
  tabs()[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "demo/drafts") {
    fail("таб черновиков увёл не на адрес накопителя: " + sandbox.location.hash);
  }
  await sandbox.refresh();
  await settle();
  if (openTab() !== "Черновики") fail("в накопителе подсвечен не тот таб: " + openTab());
  if (!dump(groups).includes("XR-D1")) fail("записи накопителя не собрались: " + dump(groups).slice(0, 300));
  // Хлебной крошки над накопителем больше нет: назад ведёт тот же таб.
  if (byClass(groups, "crumb")) fail("над накопителем осталась хлебная крошка");
}

// --- старый адрес накопителя открывает его же таб ---
{
  await go("#demo/drafts");
  if (openTab() !== "Черновики") fail("старый адрес открыл не таб накопителя: " + openTab());
  tabs()[0].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "demo") {
    fail("возврат в задачи увёл не на доску: " + sandbox.location.hash);
  }
}

// --- полоска работ: число и дорога в раздел, без карточек и разговоров ---
{
  await go("#demo");
  const said = dump(live);
  if (!said.includes("работает 2 агента")) fail("полоска не сосчитала работы: " + said);
  if (byClass(live, "lcard")) fail("лента карточек сессий вернулась на экран проекта");
  const link = byClass(live, "alink");
  if (!link) fail("с полоски нет дороги в раздел агентов: " + said);
  link.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "/agents") {
    fail("полоска увела не в раздел агентов: " + sandbox.location.hash);
  }
  // Работ нет вовсе: полоска уходит с экрана целиком, пустая строка над доской
  // читалась бы недорисованной.
  sandbox.renderLive("demo", []);
  if (live.children.length) fail("полоска осталась на экране без работ");
}

// --- телефон: третий таб «Сессии» вместо своего переключателя ---
{
  // Узкий экран мерится тем же запросом, что и в статике.
  sandbox.window.matchMedia = (q) => ({ matches: String(q).includes("max-width:900px"),
    addEventListener: () => {}, removeEventListener: () => {} });
  await go("#demo");
  const names = tabs().map((t) => t.textContent);
  if (names.join("|") !== "Задачи|Сессии|Черновики") {
    fail("на телефоне табы не те: " + JSON.stringify(names));
  }
  if (byClass(groups, "btabs")) fail("старый переключатель «Бэклог/Сессии» остался на экране");
  const sect = (key) => allByClass(groups, "bsec").find((n) => n.dataset.tab === key);
  const opened = (key) => String(sect(key).className).includes("onsec");

  // Половины доски переключаются по месту: адрес тот же, список не
  // пересобирается, и подсветка переезжает вместе с секциями.
  const was = sandbox.location.hash;
  tabs()[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash !== was) fail("таб сессий тронул адрес: " + sandbox.location.hash);
  if (openTab() !== "Сессии") fail("подсветка не переехала на сессии: " + openTab());
  if (!opened("sess") || opened("back")) fail("открыта не та половина доски");

  tabs()[0].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (openTab() !== "Задачи") fail("подсветка не переехала на задачи: " + openTab());
  if (!opened("back") || opened("sess")) fail("половина задач не открылась");

  // С накопителя «Сессии» возвращают на доску своим переходом.
  await go("#demo/drafts");
  if (tabs().length !== 3) fail("на телефоне накопитель показал не три таба");
  tabs()[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "demo") {
    fail("с накопителя сессии увели не на доску: " + sandbox.location.hash);
  }
  await sandbox.refresh();
  await settle();
  if (openTab() !== "Сессии") fail("после возврата открыт не таб сессий: " + openTab());
  sandbox.window.matchMedia = () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} });
}

console.log("poc_btabs: ok");

// Стенд таба сессий на доске (ветка poc-chat).
//
// Сессии переехали с отдельного раздела «Агенты» в третий таб доски: они
// работа проекта, и место им на его доске, а сквозной обзор машины живёт общим
// списком разговоров в панели (решение пользователя). Предмет стенда: три таба
// на любой ширине, единый список без вложенных табов, происхождение сессии
// чипом в строке, переезд старого адреса вместе с запросом и поиск по табу.
// Строка проверяется тут же: номер задачи это ссылка на её форму, разговор
// открывается и кнопкой, и нажатием на строку, а адрес его берётся от сессии,
// когда она известна.
//
// Зовётся: node testdata/poc_agents.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

const app = appPathArg();
// Список проектов для экрана по адресу: одна своя работа и одна чужая, чтобы
// оба таба были непустыми.
const headProjects = [{ name: "demo", prefix: "XR", works: [
  { id: "XR-1", kind: "task", via: "tmux", title: "конвейер задачи", session: "aaaa1111",
    own: true, model: "opus", sect: "in-progress" },
  { id: "XR-2", kind: "goal", via: "registry", title: "цикл цели", sect: "in-progress" },
] }];

// Ответы стенда: раздел собирается из одного списка проектов, а экран по
// адресу «#/agents» поднимается тем же refresh, что и доска.
const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: headProjects };
  if (path.endsWith("/board")) return { board: { sections: [] } };
  if (path.includes("/chats")) return { chats: [] };
  return {};
});

const now = Date.now();
const works = {
  // Конвейер задачи: сессию поднял дашборд, её и слушаем.
  tmux: { id: "DK-397", kind: "task", via: "tmux", sect: "in-progress", own: true,
    title: "дашборд ведёт разговор", session: "aaaa1111-1111", started: now / 1000 - 600 },
  // Работа поднята мимо дашборда: сессии он не видит, а задача известна.
  registry: { id: "DK-470", kind: "task", via: "registry", sect: "in-progress",
    title: "экран документа", started: now / 1000 - 3600 },
  // Сессия без задачи: остаётся один разговор.
  bare: { kind: "session", via: "session", session: "bbbb2222-2222",
    note: "задача не узнана", started: now / 1000 - 60 },
};

const rowOf = (w) => sandbox.agentRow("demo", w, now);
const btns = (row) => allByClass(row, "btn").map((b) => b.textContent);
// Кнопка чата тут значком, как в строке доски: подписи у неё нет, узнаётся она
// подсказкой.
const chatBtn = (row) => allByClass(row, "btn").find((b) => String(b.title) === "Чат агента");

// --- у работы с задачей обе дороги, чем бы она ни была поднята ---
for (const which of ["tmux", "registry"]) {
  const w = works[which];
  const row = rowOf(w);
  const link = byClass(row, "alink");
  if (!link || link.textContent !== w.id) {
    fail(which + ": номер задачи не ссылка: " + dump(row));
  }
  if (!chatBtn(row)) fail(which + ": входа в разговор нет: " + dump(row));
  if (!String(chatBtn(row).className).includes("btn-ico")) {
    fail(which + ": кнопка чата не значком: " + chatBtn(row).className);
  }
  // Ссылка ведёт на форму задачи и не утаскивает за собой чат строки.
  sandbox.location.hash = "#/agents";
  link.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "demo/" + w.id) {
    fail(which + ": ссылка увела не на задачу: " + sandbox.location.hash);
  }
}

// --- стоп стоит только у своей tmux-работы ---
{
  // Снятие сессии зовётся одним словом на весь дашборд: три подписи у одного
  // действия («Остановить», «Стоп», «Остановить агента») человек читал как три
  // разных (замечание пользователя).
  if (!btns(rowOf(works.tmux)).includes("Стоп")) {
    fail("у своей работы пропал стоп: " + JSON.stringify(btns(rowOf(works.tmux))));
  }
  const reg = rowOf(works.registry);
  if (btns(reg).includes("Стоп")) fail("реестровой работе достался стоп");
  if (btns(rowOf(works.tmux)).includes("Остановить")) {
    fail("у стопа осталась старая подпись: " + JSON.stringify(btns(rowOf(works.tmux))));
  }
  // Происхождение сессии видно чипом: список один, вложенных табов «Дашборд» и
  // «Прочие» больше нет, и различать строки надо в них самих.
  const marks = allByClass(reg, "chip").map((c) => c.textContent);
  if (!marks.includes("внешняя")) {
    fail("чужая сессия не помечена чипом: " + JSON.stringify(marks));
  }
  if (allByClass(rowOf(works.tmux), "chip").map((c) => c.textContent).includes("внешняя")) {
    fail("своя сессия помечена чужой");
  }
  if (!String(reg.title).includes("вне дашборда")) {
    fail("подсказка строки не говорит, почему стопа нет: " + reg.title);
  }
}

// --- разговор открывается кнопкой и нажатием на строку, адрес от сессии ---
{
  const row = rowOf(works.tmux);
  sandbox.location.hash = "#/agents";
  chatBtn(row).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.tmux.session)) {
    fail("кнопка открыла не разговор этой сессии: " + sandbox.location.hash);
  }
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.tmux.session)) {
    fail("нажатие на строку не открыло разговор: " + sandbox.location.hash);
  }
}

// --- у работы без сессии разговор адресуется задачей ---
{
  const row = rowOf(works.registry);
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("DK-470")) {
    fail("разговор реестровой работы открылся не по задаче: " + sandbox.location.hash);
  }
}

// --- сессия без задачи: только разговор, ссылки нет ---
{
  const row = rowOf(works.bare);
  if (byClass(row, "alink")) fail("у сессии без задачи взялась ссылка на задачу");
  if (!chatBtn(row)) fail("у сессии без задачи нет разговора");
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.bare.session)) {
    fail("разговор сессии без задачи открылся не по её ID: " + sandbox.location.hash);
  }
}

// --- три таба на широком экране, список один, поиск по нему ---
{
  const sessions = [
    { id: "XR-1", kind: "task", via: "tmux", title: "конвейер задачи", session: "aaaa1111",
      own: true, model: "opus", sect: "in-progress", live: "busy" },
    { id: "XR-2", kind: "goal", via: "registry", title: "цикл цели", sect: "in-progress",
      live: "dead" },
    { kind: "session", via: "session", session: "bbbb2222", note: "окно человека",
      live: "idle" },
  ];
  const groups = sandbox.document.getElementById("groups");
  const tabs = () => allByClass(groups, "ktab");
  const rows = () => allByClass(groups, "arow");
  const openTab = () => (tabs().find((t) => String(t.className).includes("onktab")) || {}).textContent;

  // Экран тут широкий: matchMedia мока отвечает «нет» на запрос телефона.
  sandbox.renderSessions("demo", sessions, "");
  const names = tabs().map((t) => t.textContent);
  if (names.join("|") !== "Задачи|Сессии|Черновики") {
    fail("на широком экране табов доски не три: " + JSON.stringify(names));
  }
  if (openTab() !== "Сессии") fail("подсвечен не таб сессий: " + openTab());
  if (rows().length !== 3) {
    fail("список сессий не единый: " + rows().length + " строк, " + dump(groups).slice(0, 300));
  }
  const said = dump(groups);
  if (said.includes("Дашборд") || said.includes("Прочие")) {
    fail("вложенные табы остались на экране: " + said.slice(0, 300));
  }
  // Своя и чужая стоят в одном списке, различает их чип.
  const alien = rows().filter((r) => allByClass(r, "chip").some((c) => c.textContent === "внешняя"));
  if (alien.length !== 2) {
    fail("чипом происхождения помечены не те строки: " + alien.map((r) => dump(r)).join(" | "));
  }

  // Поиск фильтрует сессии таба, а не задачи доски.
  sandbox.renderSessions("demo", sessions, "цикл");
  if (rows().length !== 1 || !dump(rows()[0]).includes("цикл цели")) {
    fail("поиск не нашёл сессию по заголовку: " + dump(groups).slice(0, 200));
  }
  for (const [q, what] of [["XR-1", "задаче"], ["opus", "модели"], ["конвейер", "заголовку"]]) {
    sandbox.renderSessions("demo", sessions, q);
    if (!rows().length) fail("поиск по " + what + " (" + q + ") ничего не нашёл");
  }
  sandbox.renderSessions("demo", sessions, "такого нет");
  if (rows().length) fail("поиск нашёл лишнее по чепухе");
  if (!dump(groups).includes("ничего не нашлось")) {
    fail("пустая выдача таба молчит: " + dump(groups).slice(0, 200));
  }
}

// --- счётчик сессий стоит на самом табе отдельным словом ---
// Прежде число жило пунктом боковой колонки, а в снимке пользователя таб
// читался словом «Дашборд5»: число шло хвостом подписи, без отступа и цвета.
{
  const many = [];
  for (let i = 1; i <= 123; i += 1) {
    many.push({ id: "XR-" + i, kind: "task", via: "tmux", title: "работа " + i,
      session: "s" + i, own: true, sect: "in-progress" });
  }
  const groups = sandbox.document.getElementById("groups");
  const tabs = () => allByClass(groups, "ktab");

  sandbox.renderSessions("demo", many, "");
  const sess = tabs()[1];
  if (sess.textContent !== "Сессии") {
    fail("число слиплось с подписью таба: " + JSON.stringify(sess.textContent));
  }
  const n = byClass(sess, "n");
  if (!n || n.textContent !== "123") {
    fail("трёхзначный счётчик таба не отдельным узлом: " + dump(sess));
  }
  // Соседние табы числа не пишут вовсе: считается тут только список сессий.
  if (byClass(tabs()[0], "n") || byClass(tabs()[2], "n")) {
    fail("счётчик вылез на соседние табы: " + tabs().map(dump).join(" | "));
  }
  sandbox.renderSessions("demo", [], "");
  if (byClass(tabs()[1], "n")) fail("пустой таб пишет счётчик: " + dump(tabs()[1]));

  // Вид баджа живёт в style.css: голая цифра вплотную к подписи читалась
  // признаком самого таба, а не числом строк за ним.
  const css = readFileSync(join(dirname(app), "style.css"), "utf8");
  const rule = (css.match(/\.ktab \.n\{([^}]*)\}/) || [])[1];
  if (!rule) fail("вида у баджа таба нет: правила .ktab .n в style.css не нашлось");
  if (!/margin-left:\s*[1-9]/.test(rule)) {
    fail("бадж стоит вплотную к подписи: " + rule);
  }
  for (const want of ["border-radius", "background", "padding"]) {
    if (!rule.includes(want)) fail("бадж собран не пилюлей (нет " + want + "): " + rule);
  }
}

// --- бадж стоит у всех трёх табов одного вида ---
{
  const groups = sandbox.document.getElementById("groups");
  const tabs = () => allByClass(groups, "ktab");
  sandbox.countsSet({ tasks: 12, sess: 3, drafts: 7 });
  sandbox.renderSessions("demo", [{ id: "XR-1", kind: "task", via: "tmux", own: true },
    { id: "XR-2", kind: "task", via: "tmux", own: true },
    { id: "XR-3", kind: "task", via: "tmux", own: true }], "");
  const got = tabs().map((t) => (byClass(t, "n") || {}).textContent || "");
  if (JSON.stringify(got) !== JSON.stringify(["12", "3", "7"])) {
    fail("баджи табов собрались не по числам: " + JSON.stringify(got));
  }
  for (const t of tabs()) {
    const n = byClass(t, "n");
    if (!n || String(n.className) !== "n") {
      fail("вид баджа разный у табов: " + (n ? n.className : "баджа нет"));
    }
  }
}

// --- старый адрес раздела ведёт на таб вместе с запросом ---
// Раздел «Агенты» упразднён, а ссылки на него и память вкладки ломаться не
// должны: адрес переезжает на таб сессий текущего проекта. Поле шапки тут
// фильтрует сессии, поэтому и слова в нём про сессии, а на доске про задачи.
{
  const psub = byId.get("psub");
  const hq = byId.get("hq");
  const go = async (hash) => {
    sandbox.location.hash = hash;
    await sandbox.refresh();
    await settle();
  };
  const hashNow = () => sandbox.location.hash.replace(/^#/, "");

  await go("#/agents");
  if (hashNow() !== "demo/sess") {
    fail("старый адрес раздела не переехал на таб: " + sandbox.location.hash);
  }
  if (String(psub.textContent || "").trim() !== "сессии проекта") {
    fail("шапка не называет открытый таб: " + JSON.stringify(psub.textContent));
  }
  if (hq.placeholder !== "Поиск сессий") {
    fail("поле таба обещает не то, что ищет: " + JSON.stringify(hq.placeholder));
  }
  if (!allByClass(sandbox.document.getElementById("groups"), "arow").length) {
    fail("после переезда список сессий пуст: " + dump(sandbox.document.getElementById("groups")).slice(0, 300));
  }

  await go("#/agents/" + encodeURIComponent("цикл"));
  // Адрес тут читается как есть: разбор хэша его расшифровывает, и сравнивать
  // с закодированным значило бы сравнивать с чужой записью того же адреса.
  if (decodeURIComponent(hashNow()) !== "demo/sess/цикл") {
    fail("запрос при переезде потерялся: " + sandbox.location.hash);
  }
  if (hq.placeholder !== "Поиск сессий") {
    fail("под поиском поле таба заговорило о задачах: " + JSON.stringify(hq.placeholder));
  }
  // Смена слов поля не трогает его значение: запрос по-прежнему приезжает из
  // адреса, а не остаётся от прежнего экрана.
  if (hq.value !== "цикл") {
    fail("поле шапки не отражает запрос таба: " + JSON.stringify(hq.value));
  }

  // Набранное в поле уходит в адрес таба, а не в выдачу по доске.
  sandbox.findGo("конвейер");
  await settle();
  if (decodeURIComponent(hashNow()) !== "demo/sess/конвейер") {
    fail("набранное в табе увело не туда: " + sandbox.location.hash);
  }

  await go("#demo");
  if (hq.placeholder !== "Поиск задач") {
    fail("на доске поле заговорило о сессиях: " + JSON.stringify(hq.placeholder));
  }
  if (hq.value !== "") {
    fail("уход с таба оставил запрос в поле: " + JSON.stringify(hq.value));
  }
  // На самой доске табов тоже три, и открыт первый.
  const tabs = allByClass(sandbox.document.getElementById("groups"), "ktab");
  if (tabs.map((t) => t.textContent).join("|") !== "Задачи|Сессии|Черновики") {
    fail("на доске табов не три: " + JSON.stringify(tabs.map((t) => t.textContent)));
  }
  const on = tabs.find((t) => String(t.className).includes("onktab"));
  if (!on || on.textContent !== "Задачи") fail("на доске подсвечен не таб задач: " + (on && on.textContent));
  // Таб сессий уводит на свой адрес, а не переключает половины доски.
  tabs[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (hashNow() !== "demo/sess") fail("таб сессий увёл не на свой адрес: " + sandbox.location.hash);
}

console.log("poc_agents: ok");

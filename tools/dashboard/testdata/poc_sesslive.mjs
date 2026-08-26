// Стенд живого таба сессий (ветка poc-chat).
//
// Список сессий отвечает на вопрос «чем машина занята сейчас», и отвечать он
// обязан сам по себе: сессия начала ход, поднялась наверх и позеленела,
// кончила, опустилась. Прежде порядок был порядком обхода, а список стоял до
// перерисовки экрана. Предмет стенда: сортировка по состоянию и свежести хода,
// живой опрос своей ручкой с перерисовкой по месту (экран и соседние строки
// узлами не меняются, иначе вернутся тормоза правки 4ca66f54) и гашение опроса
// при уходе с таба.
//
// Он же держит главное правило таба: показывает он живые работы. Разговор,
// чьей tmux-сессии не видно, уходит независимо от того, как она кончилась;
// прежде уходили только снятые из дашборда, а умершие сами висели строками
// «сессии нет 7 мин» с припиской о невозможности снятия (снимок пользователя).
//
// Зовётся: node testdata/poc_sesslive.mjs static/app.js

import { readFileSync } from "node:fs";
import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const sec = (ago) => Math.floor((Date.now() - ago * 1000) / 1000);

// Четыре сессии по одной на состояние плюс вторая работающая: по ней видно,
// что внутри группы порядок идёт по свежести хода.
// Имя tmux-сессии (tmux) едет у каждой работы, которую поднимал дашборд: на
// нём стоит и признак own, и закрытие, и врозь эти два поля не ездят.
const works = () => [
  { kind: "session", via: "session", session: "idle-1", note: "молчащее окно",
    own: true, tmux: "chat-idle-1", live: "idle", moved: sec(3 * 3600) },
  { kind: "session", via: "session", session: "dead-1", note: "снятая сессия",
    own: true, tmux: "chat-dead-1", live: "dead", moved: sec(9 * 3600) },
  { kind: "session", via: "session", session: "busy-2", note: "второй ход",
    own: true, tmux: "chat-busy-2", live: "busy", moved: sec(120) },
  { kind: "session", via: "session", session: "wait-1", note: "спросил и ждёт",
    own: true, tmux: "chat-wait-1", live: "waiting", moved: sec(300) },
  { kind: "session", via: "session", session: "busy-1", note: "первый ход",
    own: true, tmux: "chat-busy-1", live: "busy", moved: sec(5) },
];

// Ответ ручки работ стенд подменяет по ходу: так выглядит происходящее на
// машине, пока человек смотрит на таб.
const now = { works: works(), asked: 0 };

const stopped = [];
const { sandbox, byId, timers } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST" && path.includes("/stop")) {
    stopped.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { way: "drop", message: "сессия снята" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: now.works }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/works")) {
    now.asked += 1;
    return { project: "demo", works: now.works };
  }
  if (path.endsWith("/board")) return { board: { sections: [] }, works: now.works };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const rows = () => allByClass(groups, "arow");
const order = () => rows().map((r) => {
  const said = dump(r);
  for (const name of ["первый ход", "второй ход", "спросил и ждёт", "молчащее окно", "снятая сессия"]) {
    if (said.includes(name)) return name;
  }
  return "?";
});
const dotOf = (what) => {
  const row = rows().find((r) => dump(r).includes(what));
  return row ? String((byClass(row, "dot") || {}).className || "") : "";
};
// Опрос заведён одним таймером своей частоты: его же стенд и прокручивает.
const tick = async () => {
  const own = timers.filter((t) => t.ms === poll && t.fn);
  if (!own.length) fail("опрос таба не заведён: таймера с его частотой нет");
  const last = own[own.length - 1];
  last.fn();
  last.fn = null;
  await settle();
};

await go("#demo/sess");

// --- порядок: работающие, ждущие, простаивающие; внутри по свежести ---
// Мёртвой сессии в списке нет вовсе: таб показывает живые работы.
{
  const got = order();
  const want = ["первый ход", "второй ход", "спросил и ждёт", "молчащее окно"];
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("порядок сессий не по состоянию и свежести: " + JSON.stringify(got));
  }
  if (dump(groups).includes("снятая сессия")) {
    fail("разговор с мёртвой сессией остался в табе: " + dump(groups).slice(0, 300));
  }
}

// Частота опроса названа в статике одним местом, и стенд читает её оттуда же:
// своё число тут разъехалось бы с кодом на первой же правке.
const poll = Number((readFileSync(app, "utf8").match(/const SESS_POLL = (\d+)/) || [])[1]);
if (!poll) fail("частоты опроса таба нет в статике: константы SESS_POLL не нашлось");

// --- опрос идёт своей ручкой, а не общим списком проектов ---
{
  const was = now.asked;
  await tick();
  if (now.asked !== was + 1) {
    fail("опрос не сходил в ручку работ проекта: заходов " + now.asked);
  }
}

// --- строка меняет место и цвет по приезду нового состояния, экран не пересобран ---
{
  const card = byClass(groups, "tbl");
  const quiet = rows().find((r) => dump(r).includes("молчащее окно"));
  if (!card || !quiet) fail("экран таба собрался не так: " + dump(groups).slice(0, 200));
  if (!dotOf("спросил и ждёт").includes("dot-wait")) {
    fail("ждущая сессия помечена не своим кружком: " + dotOf("спросил и ждёт"));
  }

  // Ждущая сессия получила ответ и пошла в ход, а работавшая кончила.
  now.works = now.works.map((w) => {
    if (w.session === "wait-1") return Object.assign({}, w, { live: "busy", moved: sec(1) });
    if (w.session === "busy-1") return Object.assign({}, w, { live: "idle", moved: sec(1) });
    return w;
  });
  await tick();

  const got = order();
  if (got[0] !== "спросил и ждёт") {
    fail("пошедшая в ход сессия не поднялась наверх: " + JSON.stringify(got));
  }
  if (got.indexOf("первый ход") <= got.indexOf("второй ход")) {
    fail("кончившая сессия не опустилась под работающую: " + JSON.stringify(got));
  }
  if (!dotOf("спросил и ждёт").includes("pulse")) {
    fail("пошедшая в ход сессия не позеленела: " + dotOf("спросил и ждёт"));
  }
  if (!dotOf("первый ход").includes("dot-idle")) {
    fail("кончившая сессия осталась зелёной: " + dotOf("первый ход"));
  }
  // Экран не пересобран: карточка та же, и строка, у которой ничего не
  // изменилось, пережила обновление тем же узлом (в ней бывает и открытое
  // подтверждение закрытия, и его перерисовка снимала бы).
  if (byClass(groups, "tbl") !== card) fail("обновление пересобрало экран таба целиком");
  if (rows().find((r) => dump(r).includes("молчащее окно")) !== quiet) {
    fail("нетронутая строка пересобрана заново");
  }
}

// --- работа без состояния: «активной» она не зовётся, а снимается с оглядкой ---
//
// Сервер состояния не назвал (старая сборка, поломка разбора), и назвать за
// него нечем. Прежде зелёный кружок был умолчанием, и такая строка горела
// работающей (замечание пользователя: сессия давнего разговора показана
// работающей и с кнопкой). Закрытие у неё есть: имя tmux-сессии приехало, и
// снимать есть что. Держится оно вторым нажатием наравне с занятой, потому
// что снятое обратно не поднимается.
{
  now.works = [{ kind: "session", via: "session", session: "mute-1", own: true,
    tmux: "chat-mute-1", note: "сессия без состояния" }];
  await tick();
  const row = rows().find((r) => dump(r).includes("сессия без состояния"));
  if (!row) fail("строка без состояния пропала с экрана: " + dump(groups).slice(0, 300));
  const said = dump(row);
  // Слово ищется чипом, а не подстрокой: «интерактивная сессия» содержит его
  // внутри себя, и голый поиск по тексту ловил бы вид работы вместо состояния.
  const chips = allByClass(row, "chip").map((c) => c.textContent);
  if (chips.includes("активна")) {
    fail("работа без состояния названа активной: " + JSON.stringify(chips));
  }
  if (!said.includes("состояние неизвестно")) {
    fail("работа без состояния молчит вместо честных слов: " + said);
  }
  const dot = String((byClass(row, "dot") || {}).className || "");
  if (dot.includes("pulse")) fail("работа без состояния горит зелёным: " + dot);
  const shut = byClass(row, "sclose");
  if (!shut) fail("у работы без состояния пропало закрытие: " + said);
  stopped.length = 0;
  shut.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (stopped.length) {
    fail("работа, о которой неизвестно даже, идёт ли она, снялась без подтверждения: " +
      JSON.stringify(stopped));
  }
  if (!String(shut.className).split(" ").includes("armed")) {
    fail("кнопка не взвелась подтверждением: " + shut.className);
  }
  shut.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!stopped.some((x) => x.path.includes("mute-1/stop"))) {
    fail("подтверждённое снятие не ушло: " + JSON.stringify(stopped));
  }
}

// --- снятая сессия уходит из списка тем же ходом ---
{
  now.works = [
    { kind: "session", via: "session", session: "keep-1", own: true, tmux: "chat-keep-1",
      live: "idle", note: "останется в списке", moved: sec(600) },
    { kind: "session", via: "session", session: "gone-1", own: true, tmux: "chat-gone-1",
      live: "idle", note: "эту снимаем", moved: sec(900) },
  ];
  await tick();
  const row = rows().find((r) => dump(r).includes("эту снимаем"));
  if (!row) fail("строки для снятия нет: " + dump(groups).slice(0, 300));
  const close = deepBtn(row, "sclose");
  if (!close) fail("у простаивающей сессии нет кнопки снятия: " + dump(row));
  // Сервер снял сессию, но своим списком работ он ответит только следующим
  // заходом: строка обязана уйти сразу.
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (rows().some((r) => dump(r).includes("эту снимаем"))) {
    fail("снятая строка осталась в списке: " + dump(groups).slice(0, 300));
  }
  if (!rows().some((r) => dump(r).includes("останется в списке"))) {
    fail("снятие унесло с собой соседнюю строку");
  }
  // Разговор при этом не потерян: снималась сессия, а не он.
  if (!stopped.some((x) => x.path.includes("gone-1/stop") && x.body && x.body.drop)) {
    fail("снятие пошло не той ручкой: " + JSON.stringify(stopped));
  }
}

// --- живые работы остаются, а мёртвые уходят, как бы сессия ни кончилась ---
// Три мёртвых разговора из снимка пользователя: две снятые вручную сессии и
// старый чат чужой подписки. Рядом живое окно мимо дашборда и цикл цели из
// реестра: их прятать нельзя, работа в них идёт, просто снять её отсюда нечем.
{
  now.works = [
    { kind: "session", via: "session", session: "alive-1", own: false, live: "idle",
      note: "окно в редакторе", moved: sec(120) },
    { kind: "session", via: "session", session: "dead-a", own: true, tmux: "chat-dead-a",
      live: "dead", note: "моя тестовая сессия", moved: sec(420) },
    { kind: "session", via: "session", session: "dead-b", own: true, tmux: "chat-dead-b",
      live: "dead", note: "вторая тестовая", moved: sec(430) },
    { kind: "session", via: "session", session: "dead-c", own: false, live: "dead",
      note: "старый чат glm", moved: sec(900) },
    { id: "XR-9", kind: "goal", title: "Цель: панель разговора", via: "registry", live: "dead" },
  ];
  await tick();
  const said = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["моя тестовая сессия", "вторая тестовая", "старый чат glm"]) {
    if (said.includes(gone)) {
      fail("разговор с мёртвой сессией остался в табе: " + gone + " | " + said.slice(0, 300));
    }
  }
  if (!said.includes("окно в редакторе")) {
    fail("живое окно мимо дашборда пропало из таба: " + said.slice(0, 300));
  }
  // Сессия жива, а снять её нечем, и говорит это сама кнопка: она стоит в
  // строке погашенной. Приписок в хвосте («поднята вне дашборда», «снимать
  // нечем») тут больше нет, они повторяли чип «внешняя» и подсказку строки.
  const outer = rows().find((r) => dump(r).includes("окно в редакторе"));
  const shut = outer && byClass(outer, "sclose");
  if (!shut) fail("у живого внешнего окна пропала кнопка закрытия: " + said.slice(0, 300));
  if (!shut.disabled) fail("внешнему окну предложено закрытие, которого нет: " + dump(outer));
  if (!String(shut.title || "").includes("там же, где открыта")) {
    fail("погашенная кнопка не сказала, где это окно закрывают: " + shut.title);
  }
  for (const gone of ["поднята вне дашборда", "идёт вне дашборда", "снимать нечем"]) {
    if (said.includes(gone)) fail("снятая приписка вернулась в строку: " + said.slice(0, 300));
  }
  // Цикл цели из реестра дашборд не видит вовсе, а идёт он на машине.
  if (!said.includes("Цель: панель разговора")) {
    fail("цикл цели из реестра пропал из таба: " + said.slice(0, 300));
  }
  // Счётчик таба считает то же, что показано списком.
  const bar = byClass(groups, "ktabs");
  const tab = (bar.children || []).find((t) => dump(t).includes("Сессии"));
  const n = tab ? dump(tab).replace(/\D+/g, "") : "";
  if (n !== "2") fail("счётчик таба говорит " + n + ", а строк в списке две");
}

// --- только что поднятая работа из таба не пропадает ---
// Между нажатием и первым ходом клиента проходят секунды: сессия ещё не жива,
// но и не мертва, и пропавшая строка читалась бы как несработавшее нажатие.
{
  now.works = [
    { id: "XR-7", kind: "task", title: "только что поднятая", via: "session",
      session: "lift-1", own: true, tmux: "task-XR-7-1", live: "dead", moved: sec(5) },
  ];
  sandbox.markRunLive("demo", "XR-7", "task-XR-7-1");
  await tick();
  if (!dump(groups).includes("только что поднятая")) {
    fail("поднимающаяся работа пропала из таба: " + dump(groups).slice(0, 300));
  }
}

// --- уход с таба гасит опрос ---
{
  await tick();
  const pending = timers.filter((t) => t.ms === poll);
  const armed = pending[pending.length - 1];
  if (!armed || !armed.fn) {
    fail("опрос не завёл следующий заход: список замер бы после первого тика");
  }
  // Снятие таймера мок изображает подменой его хода на пустой, как это делает
  // clearTimeout браузера: заведённым остаётся сам слот, а хода в нём больше
  // нет.
  const was = armed.fn;
  await go("#demo");
  if (armed.fn === was) fail("опрос остался заведённым после ухода с таба");
  const asked = now.asked;
  armed.fn();
  await settle();
  if (now.asked !== asked) fail("погашенный опрос всё равно сходил на сервер");
}

console.log("poc_sesslive: ok");

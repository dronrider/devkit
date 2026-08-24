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
// Зовётся: node testdata/poc_sesslive.mjs static/app.js

import { readFileSync } from "node:fs";
import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const sec = (ago) => Math.floor((Date.now() - ago * 1000) / 1000);

// Четыре сессии по одной на состояние плюс вторая работающая: по ней видно,
// что внутри группы порядок идёт по свежести хода.
const works = () => [
  { kind: "session", via: "session", session: "idle-1", note: "молчащее окно",
    own: true, live: "idle", moved: sec(3 * 3600) },
  { kind: "session", via: "session", session: "dead-1", note: "снятая сессия",
    own: true, live: "dead", moved: sec(9 * 3600) },
  { kind: "session", via: "session", session: "busy-2", note: "второй ход",
    own: true, live: "busy", moved: sec(120) },
  { kind: "session", via: "session", session: "wait-1", note: "спросил и ждёт",
    own: true, live: "waiting", moved: sec(300) },
  { kind: "session", via: "session", session: "busy-1", note: "первый ход",
    own: true, live: "busy", moved: sec(5) },
];

// Ответ ручки работ стенд подменяет по ходу: так выглядит происходящее на
// машине, пока человек смотрит на таб.
const now = { works: works(), asked: 0 };

const { sandbox, byId, timers } = makeSandbox(app, (path) => {
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

// --- порядок: работающие, ждущие, простаивающие, мёртвые; внутри по свежести ---
{
  const got = order();
  const want = ["первый ход", "второй ход", "спросил и ждёт", "молчащее окно", "снятая сессия"];
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("порядок сессий не по состоянию и свежести: " + JSON.stringify(got));
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
  const card = byClass(groups, "card");
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
  if (byClass(groups, "card") !== card) fail("обновление пересобрало экран таба целиком");
  if (rows().find((r) => dump(r).includes("молчащее окно")) !== quiet) {
    fail("нетронутая строка пересобрана заново");
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

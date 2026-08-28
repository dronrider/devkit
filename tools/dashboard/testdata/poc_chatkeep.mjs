// Стенд смены проекта под открытым разговором, вторая половина (жалоба
// пользователя: «почему при переключении проекта чат иногда переключается на
// новый пустой диалог?»). Соседний стенд poc_chatproj.mjs сторожит адрес
// сессии, и с ним всё было в порядке: он значит один и тот же разговор с любой
// доски. «Иногда» приходилось на три остальных вида адреса, которые сами по
// себе ничего не значат и читаются «в том проекте, что сейчас на доске»: чат
// задачи, общий чат доски и новый чат. Смена проекта перечитывала такой адрес
// заново, разговор devkit исчезал, а на его месте вставал пустой чат соседа.
//
// Зовётся: node testdata/poc_chatkeep.mjs static/app.js

import { makeSandbox, settle, dump, allByClass, appPathArg } from "./poc_dom.mjs";

const SID = "aaaa1111-2222-4222-8222-222222222222";
const SAID = "реплика разговора devkit, которая обязана пережить смену проекта";
const GONE = "доски demo больше нет в конфиге";

const chats = [
  { id: SID, project: "demo", title: "Разговор devkit", tasks: ["DK-1"], state: "dead",
    model: "opus", mtime: "2026-08-23T10:00:00+03:00" },
];

function fail(msg) {
  console.error(msg);
  process.exit(1);
}

// Свежая вкладка со своим списком досок: пул готовых панелей живёт вкладкой, и
// проверять первый заход на чужой адрес надо там, где пула ещё нет.
function tab(names) {
  const projects = names.map((name) => ({ name, works: [], sections: {} }));
  const kit = makeSandbox(appPathArg(), (path) => {
    if (path === "/api/projects") return { projects };
    if (!names.some((n) => path.startsWith("/api/projects/" + n + "/"))) {
      return { raw: { status: 404, statusText: "Not Found", text: JSON.stringify({ error: GONE }) } };
    }
    // Префикс доски свой у каждого проекта: по нему панель отличает чужую
    // задачу от своей.
    if (path.endsWith("/board")) {
      return { prefix: path.includes("/api/projects/other/") ? "OT" : "DK", rows: {} };
    }
    if (path.includes("/chats/") && path.endsWith("/status")) return { live: false, busy: false };
    if (path.includes("/chats")) return { chats };
    if (path.includes("/api/projects/other/sessions/")) {
      return { raw: { status: 404, statusText: "Not Found", text: '{"error":"сессии нет в проекте"}' } };
    }
    if (path.includes("/sessions/" + SID)) {
      return { session: SID, head: { id: SID },
        items: [{ key: "m:1", role: "user", text: SAID, time: "2026-08-23T10:00:00+03:00" }],
        start: true };
    }
    return {};
  });
  // Видно человеку только показанный слот пула: остальные разговоры лежат
  // рядом спрятанными, и читать их вместе с открытым значит смотреть мимо
  // экрана.
  kit.shown = () => (kit.sandbox.document.getElementById("cpin").children || []).filter((kid) => {
    const cls = String(kid.className || "");
    return cls.includes("cslot") && !cls.split(" ").includes("off");
  }).map((kid) => dump(kid)).join(" | ");
  kit.open = async (hash) => {
    kit.sandbox.location.hash = hash;
    await kit.sandbox.refresh();
    await settle();
  };
  // Смена проекта рукой: человек жмёт строку соседней доски в левой колонке.
  kit.pick = async (name) => {
    const item = allByClass(kit.byId.get("projects"), "sitem").find((n) => dump(n).includes(name));
    if (!item) fail("в колонке проектов нет строки " + name);
    item.handlers.click({});
    // Нажатие меняет адрес, а перерисовку в браузере поднимает событие
    // hashchange: без него стенд смотрел бы на панель, которую никто не
    // трогал.
    kit.sandbox.window.fire("hashchange", {});
    await settle();
  };
  return kit;
}

// Три вида адреса, которые сбрасывались: чат задачи (открыт кнопкой «Чат по
// задаче» с экрана задачи), общий чат доски (кнопка разговора в шапке) и новый
// чат.
for (const [what, hash, mark] of [
  ["чат задачи", "#demo/DK-1/chat/DK-1", SAID],
  ["общий чат доски", "#demo/chat/board", SAID],
  ["новый чат", "#demo/chat/new", "Новый чат"],
]) {
  const kit = tab(["demo", "other"]);
  await settle();
  await kit.open(hash);
  if (!kit.shown().includes(mark)) fail("панель не открыла " + what + ": " + kit.shown().slice(0, 300));

  const wasAsked = kit.asked.length;
  const wasPost = kit.posted.length;
  await kit.pick("other");

  if (!kit.shown().includes(mark)) {
    fail("смена проекта сбросила " + what + " на пустой диалог: " + kit.shown().slice(0, 300));
  }
  // Разговор остаётся при своей доске: список и лента спрашиваются у неё, а не
  // у той, что человек только что выбрал в шапке.
  const alien = kit.asked.slice(wasAsked).filter((p) => p.startsWith("/api/projects/other/chats"));
  if (alien.length) {
    fail(what + " переехал на соседнюю доску: " + JSON.stringify(alien));
  }
  // Адрес панели несёт свой проект: перечитывать его по доске больше не по
  // чему.
  if (!kit.sandbox.location.hash.includes("demo" + "~")) {
    fail("адрес панели потерял свой проект (" + what + "): " + kit.sandbox.location.hash);
  }
  // Мусора после себя сброс не оставлял и раньше, и заводить пустые записи от
  // смены доски панель не начинает.
  const born = kit.posted.slice(wasPost).filter((p) => p.includes("/chats/blank"));
  if (born.length) {
    fail("смена проекта завела пустую запись разговора: " + JSON.stringify(born));
  }
}

// Показать разговор нельзя: доски, за которой он закреплён, в конфиге больше
// нет. Панель говорит об этом словами, а не встречает человека пустым чатом.
const dead = tab(["other"]);
await settle();
await dead.open("#other/chat/demo~DK-1");
if (!dead.shown().includes(GONE)) {
  fail("панель промолчала о недоступном разговоре: " + dead.shown().slice(0, 300));
}
if (dead.posted.some((p) => p.includes("/chats/blank"))) {
  fail("недоступный разговор завёл пустую запись: " + JSON.stringify(dead.posted));
}

console.log("ok: смена проекта не сбрасывает ни чат задачи, ни общий чат доски, " +
  "ни новый чат, пустых записей не заводит, а недоступный разговор называет причину");

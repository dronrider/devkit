// Стенд строки доски после конца работы (ветка poc-chat).
//
// Груминг черновика идёт живым чатом, и его tmux-сессия переживает конец
// разбора: клиент стоит на приглашении и ждёт человека. Строка с таким соседом
// показывала один «Стоп» и запустить себя не давала, хотя разбор кончился час
// назад (замечание пользователя). Про сам признак работы отвечает сервер
// (TestBoardRowFreeWhenTmuxIdle), а предмет этого стенда клиентская половина:
// доска обязана заметить конец работы сама, опросом, а не перезагрузкой
// страницы, и вернуть строке её обычные кнопки.
//
// Зовётся: node testdata/poc_boardfree.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Признак работы строки: пока грумингова сессия считается работой, это tmux.
let run = "tmux";
const board = () => ({
  prefix: "XR",
  sections: [{ key: "backlog", title: "Backlog", rows: [
    { id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-", run },
  ] }],
});

const { sandbox, byId, timers } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") {
    return { harnesses: [{ name: "claude-code", bin: "claude", default: true },
      { name: "glm-code", bin: "glm" }] };
  }
  if (path.endsWith("/board")) return { board: board(), works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

const groups = byId.get("groups");
const rowOf = () => {
  const hit = [];
  const walk = (n) => {
    if (String(n.className || "").includes("row") && dump(n).includes("XR-002")) hit.push(n);
    for (const kid of n.children || []) walk(kid);
  };
  walk(groups);
  return hit[hit.length - 1] || null;
};

// --- работа идёт: у строки «Стоп» ---
{
  const row = rowOf();
  if (!row) fail("строки XR-002 на доске нет вовсе: " + dump(groups).slice(0, 300));
  // Стоп стоит значком рядом с главной кнопкой строки, и узнают его классом:
  // подписи у кнопки нет вовсе, она уехала в подсказку.
  if (!byClass(row, "rstop")) fail("у идущей работы нет «Стопа»: " + dump(row));
  if (byClass(row, "rmain") || byClass(row, "split")) {
    fail("у идущей работы оказалась кнопка запуска: " + dump(row));
  }
}

// --- пока работа идёт, доска перечитывается сама ---
const poll = timers.filter((t) => t.ms === 3000);
if (!poll.length) {
  fail("опрос конца работы не заведён: строка под «Стопом» простоит так до перезагрузки страницы");
}

// --- ход кончился: очередной тик сам возвращает строке её кнопки ---
run = "";
poll[poll.length - 1].fn();
await settle();
{
  const row = rowOf();
  if (byClass(row, "rstop")) {
    fail("«Стоп» висит на строке после конца работы: " + dump(row));
  }
  if (!byClass(row, "rmain")) {
    fail("строка не вернулась к своим кнопкам: " + dump(row));
  }
  // Выбор подписки на месте, просто лежит он в меню под тремя точками.
  const menu = byClass(row, "rmenu");
  if (!menu || !dump(menu).includes("glm-code")) {
    fail("у свободной строки нет выбора подписки: " + dump(row));
  }
}

// --- работы нет, круг оборван ---
const left = timers.filter((t) => t.ms === 3000);
if (left.length !== poll.length) {
  fail("опрос идёт и после конца работы: таймеров было " + poll.length + ", стало " + left.length);
}

console.log("poc_boardfree: ok");

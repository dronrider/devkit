// Стенд молчания скрытой вкладки (DK-986).
//
// Живой случай: на переносном макбуке грелся корпус, а на машине была одна
// вкладка дашборда, поднятого на другой машине. Опросы дашборда на видимость
// не смотрели вовсе, и вкладка в фоне спрашивала сервер столько же раз, сколько
// открытая.
//
// Предмет стенда: скрытая вкладка следующего круга не заводит, а возврат к ней
// зовёт опросы сразу, не дожидаясь остатка срока.
//
// Зовётся: node testdata/poc_hiddentab.mjs static/app.js

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Доска с живой работой: круг обновления строки заводит именно она.
const row = {
  id: "XR-7", title: "агент ведёт задачу", sect: "in-progress", r: 40,
  r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "task",
  run: "tmux", run_busy: true,
};
const work = {
  id: "XR-7", kind: "task", via: "tmux", session: "s-7", own: true,
  tmux: "task-XR-7", live: "busy", title: "агент ведёт задачу",
  started: 3000, moved: 1786000000,
};
const board = { prefix: "XR", sections: [
  { key: "in-progress", title: "In progress", rows: [row] },
] };

const { sandbox, byId, timers, asked } = makeSandbox(app, (path) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: [work],
      sections: { "in-progress": 1 } }] };
  }
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path === "/api/notifications") return { items: [] };
  if (path === "/api/waiting") return { items: [], errors: [] };
  if (path.endsWith("/board")) return { board, works: [work] };
  if (path.endsWith("/works")) return { works: [work] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

// Круги обновления строки: заведённые и ещё не отработавшие.
const beats = () => timers.filter((t) => t.ms === 3000 && t.fn);
// Отработавший круг стенд гасит сам: браузер второй раз его не позовёт.
const beat = async () => {
  const one = beats().pop();
  if (!one) return false;
  const go = one.fn;
  one.fn = null;
  go();
  await settle();
  return true;
};

byId.get("groups");
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

// --- открытая вкладка ходит по кругу ---
{
  if (!beats().length) fail("круга обновления у живой строки нет: стенд смотрит не туда");
  if (!(await beat())) fail("круг не отработал");
  if (!beats().length) fail("открытая вкладка круг не возобновила");
}

// --- скрытая вкладка следующего круга не заводит ---
// Здесь и падает старый код: опрос заводил следующий круг из своего же
// обработчика и на видимость не смотрел.
{
  sandbox.document.visibilityState = "hidden";
  if (!(await beat())) fail("круг не отработал под скрытой вкладкой");
  if (beats().length) {
    fail("скрытая вкладка завела следующий круг опроса: " + beats().length);
  }
  // Ходок в фоне не остаётся вовсе: чего не заведено, то и не спросит.
  asked.length = 0;
  await settle();
  if (asked.length) fail("скрытая вкладка спрашивала сервер: " + JSON.stringify(asked));
}

// --- возврат к вкладке догоняет пропущенное ---
{
  sandbox.document.visibilityState = "visible";
  asked.length = 0;
  sandbox.document.handlers.visibilitychange();
  await settle();
  if (!asked.some((p) => p.includes("/api/waiting"))) {
    fail("возврат к вкладке не догнал полку ждущих: " + JSON.stringify(asked));
  }
  if (!asked.some((p) => p.includes("/board"))) {
    fail("возврат к вкладке не перечитал доску: " + JSON.stringify(asked));
  }
  if (!beats().length) fail("после возврата к вкладке круг обновления не завёлся");
}

console.log("poc_hiddentab: ok");

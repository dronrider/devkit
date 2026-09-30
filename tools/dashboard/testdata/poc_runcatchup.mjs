// Стенд догона записи после старта (DK-1254).
//
// Живой случай: человек открывает форму задачи и жмёт «Взять в работу».
// Ручка запуска отвечает, как только поднялась сессия, а статус и этап пишет
// уже сама сессия командой taskctl move несколько секунд спустя. Единственный
// обход клиента после ответа читает доску заведомо раньше этой записи, и
// форма остаётся без шапки «сейчас» и степпера этапов до случайного повода
// (фокус окна, возврат на вкладку).
//
// Предмет стенда: ограниченный догон сам добирает записавшийся статус
// несколькими обходами с паузой, гаснет сам, как только запись догнала, не
// превышает потолок попыток, если она не догнала, и снимается уходом с формы.
//
// Зовётся: node testdata/poc_runcatchup.mjs static/app.js

import { makeSandbox, settle, byClass, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const ID = "XR-9";

// Пауза догона (RUN_CATCHUP_MS в app.js). Таймеры мока стоят, пока их не
// завести рукой, и стенд ищет свой круг по этой же паузе, той же лесенкой,
// что и стенд пришивания разбора (poc_groomsew.mjs).
const CATCHUP_MS = 1500;

const fireEvery = (timers, ms) => {
  const fired = new Set();
  return async () => {
    for (let i = 0; i < timers.length; i += 1) {
      if (fired.has(i) || timers[i].ms !== ms) continue;
      fired.add(i);
      timers[i].fn();
      await settle();
      return true;
    }
    return false;
  };
};

const baseRow = () => ({
  id: ID, title: "догон записи после старта", sect: "backlog", type: "task",
  cost: "S", r: 10, r_parts: [4, 2, 2, 1, 1], moved: "2026-09-29",
});

const caughtUpRow = () => ({
  ...baseRow(), sect: "in-progress", run: "tmux", run_busy: true,
  stage: "разработка", stage_since: Math.floor(Date.now() / 1000),
  stage_round: 1, stage_state: "открыт", stage_session: "сессия жива",
});

function stand(caughtUpAt) {
  let getCount = 0;
  let caughtUp = false;
  const { sandbox, byId, timers } = makeSandbox(app, (path, init) => {
    if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
    if (path.includes("/tasks/") && (!init || !init.method)) {
      getCount += 1;
      if (caughtUpAt && getCount >= caughtUpAt) caughtUp = true;
      const row = caughtUp ? caughtUpRow() : baseRow();
      return { project: "demo", id: ID, row, after: [], blocks: [],
        file: "docs/tasks/" + ID + ".md", text: "# " + ID + "\n\nПостановка.\n" };
    }
    if (path.includes("/runs") && init && init.method === "POST") {
      return { message: "конвейер " + ID + " поднят в tmux-сессии task-" + ID, session: "task-" + ID };
    }
    if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
    if (path.endsWith("/works")) return { works: [] };
    if (path.endsWith("/drafts")) return { drafts: [] };
    if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
    if (path.includes("/chats")) return { chats: [], models: [] };
    if (path === "/api/quota") return { harnesses: [], buckets: [] };
    if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
    return {};
  });
  return { sandbox, byId, timers, gets: () => getCount };
}

// --- запись догнала на третьем обходе: догон гасит себя сам ---
{
  const { sandbox, byId, timers, gets } = stand(4);
  await settle();
  const groups = byId.get("groups");
  const tick = fireEvery(timers, CATCHUP_MS);

  await sandbox.startRun("demo", ID, "", "demo/" + ID);
  await settle();
  if (gets() !== 1) fail("старт сделал не один обход доски: " + gets());
  if (byClass(groups, "now2")) fail("шапка «сейчас» пришла раньше записи");

  if (!await tick()) fail("после старта нет ни одного обхода догона");
  if (gets() !== 2) fail("первый обход догона не сходил на сервер: " + gets());
  if (byClass(groups, "now2")) fail("шапка появилась раньше своей записи");

  if (!await tick()) fail("второго обхода догона нет");
  if (gets() !== 3) fail("второй обход догона не сходил на сервер: " + gets());
  if (byClass(groups, "now2")) fail("шапка появилась раньше своей записи (второй обход)");

  // Третий обход застаёт запись, которая уже догнала (caughtUpAt=4 сверху не
  // подходит: запись, догнавшая на четвёртом ответе ручки, включая старт,
  // приходит третьим обходом самого догона).
  if (!await tick()) fail("третьего обхода догона нет");
  if (gets() !== 4) fail("третий обход догона не сходил на сервер: " + gets());
  if (!byClass(groups, "now2")) {
    fail("шапка «сейчас» не пришла после догнавшей записи: " + dump(groups).slice(0, 300));
  }
  if (!byClass(groups, "step2")) fail("степпер этапов не пришёл после догнавшей записи");

  // Догон гаснет сам: следующего круга по паузе догона нет вовсе. Снятый
  // таймер оставляет в моке пустую заглушку на своём месте, и проба находит
  // её молча, не сходив на сервер. Обход сервер больше не беспокоит, счёт
  // это и проверяет.
  const after = gets();
  await tick();
  await tick();
  if (gets() !== after) fail("обход ушёл на сервер уже после удачи догона: " + gets());
}

// --- запись не догнала: догон стоит потолком попыток, а не кругом ---
{
  const { sandbox, byId, timers, gets } = stand(0);
  await settle();
  const tick = fireEvery(timers, CATCHUP_MS);

  await sandbox.startRun("demo", ID, "", "demo/" + ID);
  await settle();
  if (gets() !== 1) fail("старт сделал не один обход доски: " + gets());

  let tries = 0;
  while (await tick()) {
    tries += 1;
    if (tries > 20) fail("догон без записи не останавливается: похоже на постоянный опрос");
  }
  if (tries < 2) fail("догон вышел потолком с первого же обхода: пауз с расчётом на запись нет");
  const total = gets();
  if (total !== 1 + tries) fail("число обходов догона разошлось со счётом сервера: " + total + " vs " + (1 + tries));

  // Потолок кончился: круга дальше нет, и пробы сервер больше не трогают.
  await tick();
  await tick();
  if (gets() !== total) fail("обход ушёл на сервер уже после потолка попыток: " + gets());
}

// --- уход с формы снимает догон ---
{
  const { sandbox, byId, timers, gets } = stand(0);
  await settle();
  const tick = fireEvery(timers, CATCHUP_MS);

  await sandbox.startRun("demo", ID, "", "demo/" + ID);
  await settle();
  if (!await tick()) fail("обхода догона нет: снимать нечего");
  const onScreen = gets();

  // Человек уходит с формы задачи на доску, не дожидаясь записи.
  sandbox.location.hash = "#demo";
  await sandbox.refresh();
  await settle();

  // Снятый уходом таймер в моке остаётся пустой заглушкой на своём месте
  // (её и находит следующая проба), а сервер она не трогает: это и есть
  // снятый догон.
  await tick();
  await tick();
  if (gets() !== onScreen) fail("догон сходил на сервер уже после ухода с формы: " + gets());
}

console.log("poc_runcatchup: ok");

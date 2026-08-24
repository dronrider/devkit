// Стенд состояния сессий и их закрытия (ветка poc-chat).
//
// Раздел красил зелёным всякую живую сессию: на снимке пользователя три
// молчащих часами окна выглядели работающими, а всего на машине их накопилось
// три десятка, и закрыть их было нечем вовсе («я ничего не могу сделать с
// этими сессиями»). Предмет стенда две половины: строка называет состояние
// словом и красит кружок по нему, а закрытие снимает сессию той же ручкой,
// какой её снимает смена модели, с подтверждением у занятой и пачкой для
// молчащих.
//
// Зовётся: node testdata/poc_agentlive.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

const works = [
  { id: "XR-1", kind: "task", via: "tmux", title: "идущая работа", own: true,
    live: "busy", moved: Math.floor((Date.now() - 30 * 1000) / 1000) },
  { id: "XR-2", kind: "task", via: "session", session: "aaaa1111-1111", title: "спросил и ждёт",
    own: true, live: "waiting", moved: Math.floor((Date.now() - 5 * 60 * 1000) / 1000) },
  { kind: "session", via: "session", session: "bbbb2222-2222", note: "молчащее окно",
    own: true, live: "idle", moved: Math.floor((Date.now() - 3 * hour) / 1000) },
  { id: "XR-9", kind: "goal", via: "registry", title: "цикл цели мимо дашборда", live: "dead" },
];

// Разговоры машины: два молчат дольше порога пачки, один свежий.
const chats = [
  { id: "bbbb2222-2222", state: "live", tmux: "chat-XR-2-1", project: "demo", mtime: stamp(3 * hour) },
  { id: "cccc3333-3333", state: "live", tmux: "chat-XR-3-1", project: "demo", mtime: stamp(9 * hour) },
  { id: "dddd4444-4444", state: "live", tmux: "chat-XR-4-1", project: "demo", mtime: stamp(2 * 60 * 1000) },
  { id: "eeee5555-5555", state: "dead", tmux: "", project: "demo", mtime: stamp(30 * hour) },
];

const stopped = [];
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST" && path.includes("/stop")) {
    stopped.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { way: "drop", message: "сессия снята" };
  }
  if (init && init.method === "DELETE" && path.includes("/runs/")) {
    stopped.push({ path, body: null });
    return { id: "XR-1", message: "работа снята" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works };
  if (path.includes("/chats")) return { chats, models: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

await go("#/agents");

const rows = () => allByClass(groups, "arow");
const rowOf = (what) => rows().find((r) => dump(r).includes(what));
// Таб раздела помнится между отрисовками, и стенд переключает его сам, как это
// делает человек.
const tabTo = async (name) => {
  const tab = allByClass(groups, "ktab").find((t) => dump(t).includes(name));
  if (!tab) fail("таба «" + name + "» на экране нет: " + dump(groups).slice(0, 200));
  tab.handlers.click({ stopPropagation: () => {} });
  await settle();
};

// --- кружок и слово состояния у каждой строки ---
{
  const cases = [
    ["идущая работа", "работает", "pulse"],
    ["спросил и ждёт", "ждёт ответа", "dot-wait"],
    ["молчащее окно", "простаивает", "dot-idle"],
  ];
  for (const [what, word, cls] of cases) {
    const row = rowOf(what);
    if (!row) fail("строки «" + what + "» на экране нет: " + dump(groups).slice(0, 300));
    if (!dump(row).includes(word)) {
      fail("строка «" + what + "» не назвала состояние «" + word + "»: " + dump(row));
    }
    const dot = byClass(row, "dot");
    if (!dot || !String(dot.className).split(" ").includes(cls)) {
      fail("кружок строки «" + what + "» не по состоянию: " + (dot ? dot.className : "кружка нет"));
    }
  }
  // Зелёным горит только идущий ход: в этом и была вся жалоба.
  const green = rows().filter((r) => String((byClass(r, "dot") || {}).className || "").includes("pulse"));
  if (green.length !== 1 || !dump(green[0]).includes("идущая работа")) {
    fail("зелёным помечено не только работающее: " + green.map((r) => dump(r)).join(" | "));
  }
  // Давность последнего хода видна словами у молчащей строки.
  if (!/простаивает\s+3 ч/.test(dump(rowOf("молчащее окно")))) {
    fail("простой без давности хода: " + dump(rowOf("молчащее окно")));
  }
  // Работа, поднятая мимо дашборда, живёт соседним табом, и зелёной она тоже
  // не бывает: сессии дашборд не видит вовсе.
  await tabTo("Прочие");
  const alien = rowOf("цикл цели мимо дашборда");
  if (!alien) fail("строки чужой работы нет: " + dump(groups).slice(0, 300));
  if (!dump(alien).includes("сессии нет")) fail("чужая работа не назвала состояние: " + dump(alien));
  const dot = byClass(alien, "dot");
  if (!String(dot.className).split(" ").includes("dot-other")) {
    fail("кружок чужой работы не по состоянию: " + dot.className);
  }
}

// --- закрытие молчащей сессии: одно нажатие, ручка та же, что у смены модели ---
{
  await tabTo("Дашборд");
  const row = rowOf("молчащее окно");
  const close = deepBtn(row, "Закрыть");
  if (!close) fail("у молчащей сессии нет кнопки закрытия: " + dump(row));
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = stopped[stopped.length - 1];
  if (!last || !last.path.includes("/chats/bbbb2222-2222/stop") || !last.body.drop) {
    fail("закрытие пошло не той ручкой: " + JSON.stringify(stopped));
  }
}

// --- занятая сессия снимается только со второго нажатия ---
{
  stopped.length = 0;
  await go("#/agents");
  const row = rowOf("спросил и ждёт");
  const close = deepBtn(row, "Закрыть");
  if (!close) fail("у ждущей сессии нет кнопки закрытия: " + dump(row));
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (stopped.length) fail("занятая сессия снялась без подтверждения: " + JSON.stringify(stopped));
  if (!dump(close).includes("Точно закрыть?")) {
    fail("кнопка не спросила подтверждения: " + dump(close));
  }
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!stopped.length || !stopped[0].path.includes("/chats/aaaa1111-1111/stop")) {
    fail("подтверждённое закрытие не ушло: " + JSON.stringify(stopped));
  }
}

// --- пачка: список того, что уйдёт, стоит до нажатия ---
{
  stopped.length = 0;
  await go("#/agents");
  const sweep = deepBtn(groups, "Закрыть простаивающие");
  if (!sweep) fail("кнопки пачки на экране нет: " + dump(groups).slice(0, 300));
  sweep.handlers.click({ stopPropagation: () => {} });
  await settle();
  const box = byClass(groups, "swbox");
  const said = dump(box);
  if (!said.includes("chat-XR-2-1") || !said.includes("chat-XR-3-1")) {
    fail("пачка не назвала, что снимет: " + said);
  }
  if (said.includes("chat-XR-4-1")) fail("в пачку попала свежая сессия: " + said);
  if (stopped.length) fail("пачка сняла сессии до подтверждения: " + JSON.stringify(stopped));
  const go2 = deepBtn(box, "Снять 2");
  if (!go2) fail("в пачке нет кнопки снятия: " + said);
  go2.handlers.click({ stopPropagation: () => {} });
  await settle();
  const paths = stopped.map((x) => x.path).join(" ");
  if (!paths.includes("bbbb2222-2222/stop") || !paths.includes("cccc3333-3333/stop")) {
    fail("пачка сняла не те сессии: " + JSON.stringify(stopped));
  }
  if (paths.includes("dddd4444-4444")) fail("пачка сняла свежую сессию: " + JSON.stringify(stopped));
}

console.log("poc_agentlive: ok");

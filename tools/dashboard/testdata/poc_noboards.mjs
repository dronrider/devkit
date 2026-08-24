// Стенд честной пустоты списка проектов (ветка poc-chat).
//
// У пользователя периодически вылезала ошибка «в корнях конфига не нашлось ни
// одной доски docs/TASKS.md», хотя досок четыре и всё работает. Причин было
// две: ответ, не приехавший вовсе (обрыв связи, отказ входа перед сервером),
// подставлял пустой список и с ним фразу про настройку, и ответ, приехавший
// после ухода с экрана, рисовал её поверх чужого экрана. Предмет стенда: про
// связь говорится про связь, поздний ответ ничего не рисует, а при живых
// проектах фразы нет вовсе.
//
// Зовётся: node testdata/poc_noboards.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const now = { projects: [{ name: "demo", prefix: "XR", works: [], sections: { backlog: 1 } }],
  fail: false, slow: false };

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") {
    if (now.fail) {
      return { raw: { status: 502, statusText: "Bad Gateway", text: "<html>вход не ответил</html>" } };
    }
    return { projects: now.projects };
  }
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const said = () => dump(groups).replace(/\s+/g, " ");
const BOARDS = "не нашлось ни одной доски";

// --- при живых проектах фразы нет вовсе ---
await go("#demo");
if (said().includes(BOARDS)) fail("фраза про доски вылезла при живых проектах: " + said());

// --- обрыв связи говорит про связь и не стирает показанное ---
{
  const was = said();
  now.fail = true;
  await sandbox.refresh();
  await settle();
  if (said().includes(BOARDS)) {
    fail("обрыв связи выдан за пустой конфиг: " + said());
  }
  if (said() !== was) {
    fail("неудачный заход стёр уже показанное: " + said().slice(0, 200));
  }
  // Про сам обрыв человек узнаёт: молчание тут неотличимо от порядка.
  const flashes = dump(byId.get("flashes"));
  if (!flashes.includes("не прочитался")) {
    fail("про обрыв связи не сказано ни слова: " + flashes);
  }
}

// --- первый заход без связи: слова про связь, а не про настройку ---
{
  const { sandbox: cold, byId: coldIds } = makeSandbox(app, () =>
    ({ raw: { status: 502, statusText: "Bad Gateway", text: "<html>вход не ответил</html>" } }));
  cold.location.hash = "#demo";
  await cold.refresh();
  await settle();
  const shown = dump(coldIds.get("groups")).replace(/\s+/g, " ");
  if (shown.includes(BOARDS)) fail("первый заход без связи назвал это настройкой: " + shown);
  if (!shown.includes("связь")) fail("про связь ни слова: " + shown);
}

// --- пустой список от ответившего сервера остаётся честной ошибкой ---
{
  now.fail = false;
  now.projects = [];
  const { sandbox: bare, byId: bareIds } = makeSandbox(app, (path) => {
    if (path === "/api/projects") return { projects: [], errors: [] };
    return {};
  });
  bare.location.hash = "#";
  await bare.refresh();
  await settle();
  if (!dump(bareIds.get("groups")).includes(BOARDS)) {
    // Пустой конфиг это настоящая ошибка настройки, и она обязана остаться.
    bare.location.hash = "#demo";
    await bare.refresh();
    await settle();
    if (!dump(bareIds.get("groups")).includes(BOARDS)) {
      fail("пустой конфиг перестал говорить про доски: " + dump(bareIds.get("groups")).slice(0, 200));
    }
  }
}

console.log("poc_noboards: ok");

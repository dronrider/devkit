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
// Он же держит меру громкости отказа связи. Карточка про потерю связи
// разрослась в красную простыню на пол-экрана с перечислением всех причин, и
// вылезала она от одного неудачного захода, то есть от штатной жизни ноутбука:
// заснул, сеть моргнула, дашборд перезапустился выкатом («зачем о штатной
// ситуации делать уведомление», замечание пользователя). Теперь одиночный
// отказ проходит молча с тихим повтором, а слова приходят одной спокойной
// строкой, только если связь не вернулась за несколько заходов подряд.
//
// Зовётся: node testdata/poc_noboards.mjs static/app.js

import { readFileSync } from "node:fs";
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

// --- одиночный обрыв связи проходит молча и не стирает показанное ---
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
  // Ни карточки, ни уведомления: экран просто сходит ещё раз.
  const flashes = dump(byId.get("flashes")).replace(/\s+/g, " ");
  if (flashes.trim()) {
    fail("одиночный отказ связи родил уведомление: " + flashes.slice(0, 200));
  }
  if (said().includes("связь с дашбордом прервалась")) {
    fail("одиночный отказ связи родил карточку: " + said().slice(0, 200));
  }
}

// --- устойчивый отказ говорит одной спокойной строкой, а возврат её гасит ---
{
  // Сколько заходов экран терпит молча, стенд читает из статики: своё число
  // тут разъехалось бы с кодом на первой правке.
  const tries = Number((readFileSync(app, "utf8").match(/const LOST_TRIES = (\d+)/) || [])[1]);
  if (!tries) fail("порога молчания нет в статике: константы LOST_TRIES не нашлось");
  for (let i = 1; i < tries; i += 1) {
    await sandbox.refresh();
    await settle();
  }
  const flashes = dump(byId.get("flashes")).replace(/\s+/g, " ");
  const shown = said();
  const lost = "связь с дашбордом прервалась";
  if (!flashes.includes(lost) && !shown.includes(lost)) {
    fail("устойчивый отказ связи промолчал: " + flashes.slice(0, 200) + " | " + shown.slice(0, 200));
  }
  // Строка одна и короткая: перечисление причин ушло в подсказку.
  const loud = ["ноутбук мог уснуть", "сеть моргнуть", "перезапускаться"];
  for (const word of loud) {
    if (shown.includes(word) || flashes.includes(word)) {
      fail("причины остались на экране простынёй: " + word);
    }
  }
  // Связь вернулась: сказанное уходит само, как только данные приехали.
  now.fail = false;
  await sandbox.refresh();
  await settle();
  if (said().includes(lost)) {
    fail("карточка потери связи не ушла по приезду данных: " + said().slice(0, 200));
  }
}

// --- заход без связи: молчит, пока не станет ясно, что связь не вернулась ---
{
  const { sandbox: cold, byId: coldIds } = makeSandbox(app, () =>
    ({ raw: { status: 502, statusText: "Bad Gateway", text: "<html>вход не ответил</html>" } }));
  cold.location.hash = "#demo";
  await cold.refresh();
  await settle();
  const first = dump(coldIds.get("groups")).replace(/\s+/g, " ");
  if (first.includes(BOARDS)) fail("заход без связи назвал это настройкой: " + first);
  if (first.includes("связь с дашбордом")) {
    fail("первый же отказ нарисовал карточку: " + first);
  }
  const tries = Number((readFileSync(app, "utf8").match(/const LOST_TRIES = (\d+)/) || [])[1]);
  for (let i = 1; i < tries; i += 1) {
    await cold.refresh();
    await settle();
  }
  const shown = dump(coldIds.get("groups")).replace(/\s+/g, " ");
  if (shown.includes(BOARDS)) fail("устойчивый отказ назвал это настройкой: " + shown);
  if (!shown.includes("связь с дашбордом прервалась")) {
    fail("про потерю связи ни слова: " + shown);
  }
  // Строка спокойная, а не красная: это не поломка, а временная тишина.
  const line = (coldIds.get("groups").children || [])[0];
  const inner = (line && line.children && line.children[0]) || {};
  if (String(inner.className || "").includes("error")) {
    fail("потеря связи нарисована сбоем: " + String(inner.className));
  }
  // Причины остались, но подсказкой, а не простынёй на экране.
  if (!String(inner.title || "").trim()) {
    fail("причины потери связи не сохранены в подсказке карточки");
  }
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

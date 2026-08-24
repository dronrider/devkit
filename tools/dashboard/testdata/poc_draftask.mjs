// Стенд вопросов груминга (ветка poc-chat).
//
// Прежде разбор кончался вопросом: заход выходил, экран записи показывал
// карточку «Вопрос груминга» с текстом, полем «Что ответить грумингу» и
// кнопкой «Повторить груминг», и ответ уезжал новым заходом, где агент
// перечитывал черновик заново. Груминг идёт живым разговором (решение 1 LLD
// DK-354), и все вопросы задаются в нём: механика повторного захода с формы
// снята (решение пользователя). Предмет стенда это две половины замены: на
// форме записи вопросов нет вовсе, а ожидание ответа видно чипом у самой
// записи, и на её экране, и в строке накопителя. На кнопке чата признака нет:
// кружок там дублировал чип и висел криво (замечание пользователя).
//
// Зовётся: node testdata/poc_draftask.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const waiting = { state: "ждёт ответа", source: "ask", note: "спросил агент",
  questions: ["резать строку или поднять цену"], until: Math.floor(Date.now() / 1000) + 400 };

// Ожиданием управляет стенд: сперва разбор спросил и ждёт, потом человек
// ответил, и признак снялся.
const now = { waiting: true };
const draft = { id: "XR-D2", title: "запись накопителя", age_words: "вчера",
  file: "docs/tasks/drafts/XR-D2.md", order: "Проведи груминг XR-D2" };

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/drafts")) {
    return { drafts: [now.waiting ? Object.assign({ waiting }, draft) : draft] };
  }
  if (path.endsWith("/outcome")) {
    // Груминг кончился, не оставив следа на диске: прежде ровно тут экран и
    // показывал карточку вопроса с полем ответа.
    return { state: "open", file: draft.file, question: "резать строку или поднять цену?",
      session: "feedbeef-0003", note: "груминг вышел, не оставив следа на диске" };
  }
  if (path.includes("/chats")) {
    return { chats: [{ id: "feedbeef-0003", state: "live", tmux: "task-XR-D2",
      project: "demo", tasks: ["XR-D2"], title: "разбор XR-D2" }], models: [] };
  }
  if (path.includes("/drafts/XR-D2")) {
    const body = { id: "XR-D2", file: draft.file, text: "текст записи", order: draft.order };
    if (now.waiting) body.waiting = waiting;
    return body;
  }
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
// Признак ожидания это чип «ждёт ответа» у записи, а не отметка на кнопке.
const waits = () => allByClass(groups, "c-wait").length;
// Кнопка чата отвечает за одно, вход в разговор: ни кружка, ни слов о статусе.
const chatBtns = () => allByClass(groups, "btn-ico");

// --- экран записи: вопросов на форме нет, ожидание видно кружком ---
{
  await go("#demo/draft/XR-D2");
  const said = dump(groups).replace(/\s+/g, " ");
  if (said.includes("Вопрос груминга")) fail("карточка вопроса осталась на форме: " + said.slice(0, 400));
  if (said.includes("Что ответить грумингу")) fail("поле ответа грумингу осталось на форме");
  if (said.includes("Повторить груминг")) fail("кнопка повторного захода осталась на форме");
  if (said.includes("Ответ уходит новым заходом")) {
    fail("подсказка про повторный заход осталась на экране: " + said.slice(0, 400));
  }
  // Разбор без следа на диске экран по-прежнему называет, но зовёт в разговор.
  if (!said.includes("Груминг кончился без исхода")) {
    fail("исход разбора без следа не назван: " + said.slice(0, 400));
  }
  if (!waits()) fail("ожидание ответа на экране записи ничем не помечено: " + said.slice(0, 400));
  if (!said.includes("ждёт ответа")) fail("чип ожидания молчит: " + said.slice(0, 400));
  // На кнопке чата признака нет вовсе, и подсказка её про вход в разговор.
  for (const btn of chatBtns()) {
    if (allByClass(btn, "wdot").length || /ждёт ответа/.test(String(btn.title || ""))) {
      fail("кнопка чата снова помечена ожиданием: " + JSON.stringify(btn.title));
    }
  }
}

// --- человек ответил: признак снялся, кружок погас ---
{
  now.waiting = false;
  await go("#demo");
  await go("#demo/draft/XR-D2");
  if (waits()) fail("чип ожидания стоит после ответа: " + dump(groups).slice(0, 300));
}

// --- строка накопителя: тот же кружок на кнопке чата ---
{
  now.waiting = true;
  await go("#demo/drafts");
  if (!waits()) fail("в строке накопителя ожидание не помечено: " + dump(groups).slice(0, 400));
  const row = byClass(groups, "srow");
  if (!row) fail("строки накопителя нет вовсе");
  if (!allByClass(row, "c-wait").length) {
    fail("чип ожидания встал не в строке записи: " + dump(groups).slice(0, 300));
  }
  for (const btn of allByClass(row, "btn-ico")) {
    if (allByClass(btn, "wdot").length) {
      fail("на кнопке чата строки вернулся кружок: " + dump(row));
    }
  }
  now.waiting = false;
  await go("#demo");
  await go("#demo/drafts");
  if (waits()) fail("чип в строке накопителя стоит после ответа");
}

console.log("poc_draftask: ok");

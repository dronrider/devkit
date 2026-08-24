// Стенд вопроса клиента в панели (ветка poc-chat).
//
// Клиент, поднятый в незнакомом каталоге, встаёт на вопросе о доверии («Yes, I
// trust this folder»), а следом на вопросе про внешние импорты правил, и до
// ответа не делает ни хода. Человек этих вопросов не видел вовсе: лента
// пустая, реплика висит недоставленной, а ответить можно было только руками в
// tmux (замечание пользователя: «не хочу каждый раз чинить что-то через
// тебя»). Предмет стенда: вопрос приходит в панель блоком с кнопками, нажатие
// шлёт ответ клиенту и ожидания не роняет, а спрашивать нечего, значит блока
// нет вовсе.
//
// Зовётся: node testdata/poc_clientask.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const ask = {
  text: "Accessing workspace: /Users/rider/projects/xr-proxy Quick safety check: " +
    "Is this a project you created or one you trust?",
  options: ["Yes, I trust this folder", "No, exit"],
  at: 1,
};

// Что отдаёт ручка вопроса и что у неё спросили: стенд правит первое и смотрит
// второе.
const now = { ask, answered: [] };

const { sandbox } = makeSandbox(app, (path, init) => {
  if (path.includes("/ask") && init && init.method === "POST") {
    now.answered.push(JSON.parse(init.body));
    now.ask = null;
    return { message: "ответ отправлен клиенту: Yes, I trust this folder" };
  }
  if (path.includes("/ask")) return now.ask ? { session: SID, tmux: "chat-2", ask: now.ask }
    : { session: SID, tmux: "chat-2", note: "клиент chat-2 ни о чём не спрашивает" };
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const st = { addr: SID, sid: SID, project: "demo", chats: [], entry: { id: SID, state: "live",
  tmux: "chat-2", idle: true }, models: [] };

// --- вопрос приходит блоком с кнопками ---
const panel = sandbox.chatPanel("demo", st);
await settle();
{
  const box = byClass(panel, "cask");
  if (!box || box.hidden) fail("блока вопроса в панели нет: " + dump(panel).slice(0, 300));
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Клиент ждёт ответа")) fail("блок не назвал себя: " + said);
  if (!said.includes("xr-proxy")) fail("в блоке нет самого вопроса: " + said);
  const btns = allByClass(byClass(box, "caskr"), "btn").map((b) => b.textContent);
  if (JSON.stringify(btns) !== JSON.stringify(ask.options)) {
    fail("кнопки собрались не по вариантам клиента: " + JSON.stringify(btns));
  }
}

// --- нажатие шлёт ответ клиенту и ожидание не роняет ---
{
  const box = byClass(panel, "cask");
  const yes = deepBtn(box, "Yes, I trust this folder");
  if (!yes) fail("кнопки согласия нет: " + dump(box));
  yes.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.answered) !== JSON.stringify([{ option: 1 }])) {
    fail("ответ ушёл не тем пунктом: " + JSON.stringify(now.answered));
  }
  // Панель после ответа продолжает ждать клиента, а не гаснет молча.
  if (!dump(box).includes("ждём клиента")) {
    fail("после ответа панель молчит: " + dump(box));
  }
}

// --- спрашивать нечего: блока нет вовсе ---
{
  now.ask = null;
  const quiet = sandbox.chatPanel("demo", st);
  await settle();
  const box = byClass(quiet, "cask");
  if (box && !box.hidden) fail("блок вопроса стоит у молчащего клиента: " + dump(box));
}

// --- у чужого окна вопрос не спрашивается вовсе ---
{
  const asked = [];
  const { sandbox: other } = makeSandbox(app, (path) => {
    asked.push(path);
    if (path.includes("/sessions/")) return { items: [], start: true };
    if (path.includes("/chats")) return { chats: [], models: [] };
    return {};
  });
  const alien = Object.assign({}, st, { entry: { id: SID, state: "vscode", tmux: "" } });
  other.chatPanel("demo", alien);
  await settle();
  if (asked.some((p) => p.includes("/ask"))) {
    fail("панель спросила вопрос у окна без нашей tmux: " + JSON.stringify(asked));
  }
}

console.log("poc_clientask: ok");

// Стенд дороги от упоминания черновика до экрана записи (ветка poc-chat).
//
// Черновик живёт в накопителе файлом и строки на доске не имеет, а в разговоре
// его называют и путём файла, и голым ID. Путь вёл на запись, а ID приводил на
// форму задачи, которой нет: экран отвечал «на доске нет строки», и с виду
// ссылка не открывалась вовсе (замечание пользователя). Предмет стенда это оба
// вида упоминания и разбор ID: за строкой доски экран остаётся формой задачи,
// за файлом накопителя уходит на запись, а ID, за которым нет ни того ни
// другого, честно упирается в отказ.
//
// Зовётся: node testdata/poc_draftlink.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const projects = [{ name: "demo", prefix: "XR", works: [] }];
const board = { prefix: "XR", sections: [
  { key: "backlog", rows: [{ id: "XR-1", title: "первая строка", sect: "backlog" }] },
] };
const draftText = "ссылка на черновик из чата не открывается";

// Отказ сервера с телом json: у ID черновика в нём стоит имя записи, у ID без
// строки и без файла его нет. Слова те же, что пишет tasks.go.
const gone = (text, draft) => ({ raw: { status: 404, statusText: "Not Found",
  text: JSON.stringify(draft ? { error: text, draft } : { error: text }) } });

const { sandbox, moves } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/tasks/XR-005")) {
    return gone("XR-005 это запись накопителя (docs/tasks/drafts/XR-005.md), " +
      "строки на доске demo у неё пока нет", "XR-005");
  }
  if (path.includes("/tasks/XR-777")) return gone("на доске demo нет строки XR-777", "");
  if (path.includes("/drafts/XR-005")) {
    return { id: "XR-005", file: "docs/tasks/drafts/XR-005.md", text: draftText, order: "разбери запись" };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

sandbox.rememberPrefixes(projects);

const groups = sandbox.document.getElementById("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const hashNow = () => sandbox.location.hash.replace(/^#/, "");

const bubble = (project, text) => {
  sandbox.chatFeedIn(project);
  return sandbox.chatBubble("агент", text, "");
};
const linkIn = (node) => allByClass(node, "mdgo")[0];
const clickLink = async (node) => {
  const a = linkIn(node);
  if (!a) fail("упоминание не стало ссылкой: " + dump(node));
  a.handlers.click({ preventDefault: () => {}, stopPropagation: () => {} });
  await sandbox.refresh();
  await settle();
};

// --- голый ID черновика ведёт на экран записи, а не на форму задачи ---
{
  const box = bubble("demo", "Про это уже есть XR-005, посмотри.");
  await clickLink(box);
  if (hashNow() !== "demo/draft/XR-005") {
    fail("ID черновика увёл не на запись накопителя: " + sandbox.location.hash);
  }
  const said = dump(groups);
  if (!said.includes(draftText)) fail("запись накопителя не собралась: " + said.slice(0, 300));
  if (said.includes("нет строки")) fail("на экране записи остался отказ доски: " + said.slice(0, 300));
}

// --- путь файла черновика ведёт туда же и без похода на доску ---
{
  await go("#demo");
  const box = bubble("demo", "Мысль лежит в docs/tasks/drafts/XR-005.md целиком.");
  const a = linkIn(box);
  if (!a || a.href !== "#demo/draft/XR-005") {
    fail("путь файла черновика повёл не на запись: " + (a ? a.href : dump(box)));
  }
  await clickLink(box);
  if (hashNow() !== "demo/draft/XR-005") {
    fail("путь файла черновика увёл не на запись: " + sandbox.location.hash);
  }
  if (!dump(groups).includes(draftText)) fail("запись по пути файла не собралась");
}

// --- подмена экрана не толкает лишнюю запись в историю ---
{
  await go("#demo");
  sandbox.location.hash = "#demo/XR-005";
  await sandbox.refresh();
  await settle();
  const went = moves.filter((m) => String(m[1]).includes("draft/XR-005"));
  if (!went.length) fail("подмена экрана мимо истории: " + JSON.stringify(moves));
  if (went.some((m) => m[0] === "push")) {
    fail("экран записи встал новой записью истории, и «назад» вернуло бы на тот же ID: " +
      JSON.stringify(went));
  }
}

// --- разговор переход переживает: хвост панели едет с новым адресом ---
{
  sandbox.location.hash = "#demo/XR-005/chat/aaaa1111-1111";
  // Адрес меняется до всякой сети, потому и читается сразу: ждать тут нечего.
  sandbox.goDraftInstead("demo", "XR-005").catch(() => {});
  if (hashNow() !== "demo/draft/XR-005/chat/aaaa1111-1111") {
    fail("подмена экрана оборвала разговор: " + sandbox.location.hash);
  }
  await settle();
}

// --- ID без строки и без записи остаётся отказом ---
{
  await go("#demo/XR-777");
  if (hashNow() !== "demo/XR-777") {
    fail("ID без записи увёл с экрана задачи: " + sandbox.location.hash);
  }
  const said = dump(groups);
  if (!byClass(groups, "error") || !said.includes("нет строки XR-777")) {
    fail("отказ по пропавшей задаче не показан: " + said.slice(0, 300));
  }
}

console.log("poc_draftlink: ok");

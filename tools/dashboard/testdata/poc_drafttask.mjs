// Стенд дороги от открытой формы черновика до формы заведённой задачи.
//
// Груминг идёт при открытой форме записи: он ставит строку на доску и уносит
// файл из накопителя. Форма оставалась формой черновика, поля пустели, а внизу
// стоял отказ «файла не видно» (замечание пользователя, DK-719). Предмет
// стенда это разбор отказа: за ID, ставшим строкой доски, экран уходит на
// форму задачи, а ID без записи и без строки честно упирается в отказ.
//
// Зовётся: node testdata/poc_drafttask.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const projects = [{ name: "demo", prefix: "XR", works: [] }];
const row = { id: "XR-005", title: "заведённая грумингом", type: "task", cost: "S", r: 5, sect: "backlog" };
const board = { prefix: "XR", sections: [{ key: "backlog", rows: [row] }] };
const taskText = "# XR-005: заведённая грумингом\n\nтело постановки\n";

// Отказ сервера с телом json: у ID, ставшего строкой доски, в нём стоит имя
// задачи, у ID без строки и без файла его нет. Слова те же, что пишет
// drafts.go.
const gone = (text, task) => ({ raw: { status: 404, statusText: "Not Found",
  text: JSON.stringify(task ? { error: text, task } : { error: text }) } });

const { sandbox, moves } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { exists: true, items: [] };
  if (path === "/api/quota") return { buckets: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/drafts/XR-005")) {
    return gone("черновика XR-005 в demo нет: грумминг завёл по нему задачу", "XR-005");
  }
  if (path.includes("/drafts/XR-777")) {
    return gone("черновика XR-777 в demo нет: файла docs/tasks/drafts/XR-777.md не видно, " +
      "грумминг мог уже завести по нему задачу", "");
  }
  if (path.includes("/tasks/XR-005") && (!init || !init.method)) {
    return { project: "demo", id: "XR-005", row, after: [], blocks: [],
      file: "docs/tasks/XR-005.md", text: taskText };
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

// --- форма черновика, чей ID стал строкой доски, уходит на форму задачи ---
{
  await go("#demo/draft/XR-005");
  if (hashNow() !== "demo/XR-005") {
    fail("форма черновика осталась на прежнем адресе: " + sandbox.location.hash);
  }
  const said = dump(groups);
  if (!said.includes("заведённая грумингом")) {
    fail("форма задачи не собралась: " + said.slice(0, 300));
  }
  if (said.includes("файла") || said.includes("черновика XR-005")) {
    fail("на форме задачи остался отказ черновика: " + said.slice(0, 300));
  }
}

// --- подмена экрана не толкает лишнюю запись в историю ---
{
  await go("#demo");
  sandbox.location.hash = "#demo/draft/XR-005";
  await sandbox.refresh();
  await settle();
  const went = moves.filter((m) => String(m[1]).endsWith("demo/XR-005"));
  if (!went.length) fail("подмена экрана мимо истории: " + JSON.stringify(moves));
  if (went.some((m) => m[0] === "push")) {
    fail("форма задачи встала новой записью истории, и «назад» вернуло бы на тот же ID: " +
      JSON.stringify(went));
  }
}

// --- разговор переход переживает: хвост панели едет с новым адресом ---
{
  sandbox.location.hash = "#demo/draft/XR-005/chat/aaaa1111-1111";
  // Адрес меняется до всякой сети, потому и читается сразу: ждать тут нечего.
  sandbox.goTaskInstead("demo", "XR-005").catch(() => {});
  if (hashNow() !== "demo/XR-005/chat/aaaa1111-1111") {
    fail("подмена экрана оборвала разговор: " + sandbox.location.hash);
  }
  await settle();
}

// --- ID без записи и без строки остаётся отказом ---
{
  await go("#demo/draft/XR-777");
  if (hashNow() !== "demo/draft/XR-777") {
    fail("ID без строки увёл с формы черновика: " + sandbox.location.hash);
  }
  const said = dump(groups);
  if (!said.includes("черновика XR-777")) {
    fail("отказ по пропавшей записи не показан: " + said.slice(0, 300));
  }
}

console.log("poc_drafttask: ok");

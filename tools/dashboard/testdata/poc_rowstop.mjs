// Стенд кнопок строки, за которой работает окно разговора (DK-716).
//
// Признак работы строки перестал зависеть от имени tmux-сессии: строку ведёт и
// конвейер task-<ID>, и доводящий чат chat-<ID>-<n>, и окно vscode. Прежде
// работой считалось только имя конвейера, и у строки, которую целый вечер вёл
// чат, стояла жёлтая кнопка «Продолжить» рядом с доступной «Выполнить»: первая
// уводила вводную в живой ход посреди работы, вторая поднимала второго
// исполнителя поверх идущей.
//
// Предмет стенда: у работы в окне разговора стоит красный «Стоп», подсказка
// называет его исход (ход прервётся, разговор останется жить), иконка чата
// ведёт в разговор с идущим ходом, а при нескольких рабочих сессиях стоп
// спрашивает, в какой прервать ход, и уносит выбранную в ручку.
//
// Зовётся: node testdata/poc_rowstop.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const calls = [];
// Ответ стопа подменяется стендом: при нескольких рабочих сессиях сервер
// отвечает списком с кодом 409, и кнопка обязана спросить человека.
let stopAnswer = null;
const { sandbox } = makeSandbox(app, (path, init) => {
  const way = init && init.method ? init.method : "GET";
  if (way !== "GET") calls.push({ way, path, body: init.body ? JSON.parse(init.body) : null });
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  if (way === "DELETE" && stopAnswer) return stopAnswer;
  return { id: "XR-9", message: "готово" };
});
await settle();

// Строка, которую ведёт разговор: признак работы chat, ход идёт, а разговор с
// этим ходом назван сервером.
const chatty = { id: "XR-9", title: "ведёт разговор", sect: "in-progress",
  run: "chat", run_busy: true, run_state: "busy", run_chat: "sid-живой" };
// Та же строка под конвейером: исход стопа у неё другой.
const piped = { id: "XR-4", title: "идёт конвейером", sect: "in-progress",
  run: "tmux", run_busy: true, run_state: "busy" };
// Работы за строкой нет вовсе.
const fresh = { id: "XR-1", title: "нетронутая очередь", sect: "backlog" };

const main = (box) => byClass(box, "rmain");
const stop = (box) => byClass(box, "rstop");
const chat = (box) => allByClass(box, "btn")
  .find((b) => String(b.attrs["aria-label"] || "").startsWith("Чат по задаче")) || null;
const press = (node) => node.handlers.click({ stopPropagation: () => {} });
const last = () => calls[calls.length - 1];

// --- у работы в окне разговора стоит «Стоп», и он обещает свой исход ---
{
  const box = sandbox.rowAction("demo", chatty, "in-progress");
  const off = stop(box);
  if (!off) fail("у работы в окне разговора нет стопа: " + dump(box));
  if (main(box)) fail("рядом со стопом осталась кнопка запуска: " + dump(box));
  // Два разных исхода под одним значком человек различает только подсказкой:
  // конвейер снимают целиком, ход разговора прерывают.
  if (!String(off.title || "").includes("разговор останется жить")) {
    fail("стоп разговора обещает исход конвейера: " + off.title);
  }
  const piper = stop(sandbox.rowAction("demo", piped, "in-progress"));
  if (!piper) fail("у конвейера пропал стоп");
  if (String(piper.title || "").includes("разговор останется жить")) {
    fail("стоп конвейера обещает исход разговора: " + piper.title);
  }
  calls.length = 0;
  stopAnswer = null;
  press(off);
  await settle();
  if (!last() || last().way !== "DELETE" || !last().path.includes("/runs/XR-9")) {
    fail("стоп разговора пошёл не той ручкой: " + JSON.stringify(calls));
  }
  // Строке без идущей работы стоп не предлагается: снимать там нечего.
  if (stop(sandbox.rowAction("demo", fresh, "backlog"))) {
    fail("строке без работы предложен стоп");
  }
}

// --- иконка чата ведёт в разговор с идущим ходом ---
//
// Прежде она открывала адрес задачи, панель показывала список её чатов, и до
// живого разговора человек делал ещё один клик, выбирая его глазами по времени.
{
  press(chat(sandbox.rowAction("demo", chatty, "in-progress")));
  await settle();
  if (!sandbox.location.hash.includes("sid-живой")) {
    fail("иконка чата открыла не живой разговор: " + sandbox.location.hash);
  }
  // Работы за строкой нет: адрес задачи и есть правильный вход, он откроет
  // список её чатов или заведёт новый.
  press(chat(sandbox.rowAction("demo", fresh, "backlog")));
  await settle();
  if (!sandbox.location.hash.includes("XR-1") || sandbox.location.hash.includes("sid-")) {
    fail("иконка чата без живой работы открыла не адрес задачи: " + sandbox.location.hash);
  }
}

// --- рабочих сессий несколько: стоп спрашивает, в какой прервать ход ---
//
// Остановить чужую работу вслепую дороже, чем спросить: какая из сессий «та
// самая», знает только человек.
{
  const box = sandbox.rowAction("demo", chatty, "in-progress");
  const off = stop(box);
  stopAnswer = { raw: { status: 409, text: JSON.stringify({
    id: "XR-9",
    error: "по задаче XR-9 работают 2 сессии: выбери, в какой прервать ход",
    sessions: [
      { session: "sid-свежий", tmux: "chat-XR-9-2", title: "ведёт разговор", live: "busy", moved: 200 },
      { session: "sid-старый", tmux: "chat-XR-9-1", title: "ведёт разговор", live: "waiting", moved: 100 },
    ],
  }) } };
  calls.length = 0;
  press(off);
  await settle();
  const pick = byClass(box, "spick");
  if (!pick) fail("стоп при нескольких сессиях не спросил, какую прервать: " + dump(box));
  const rows = allByClass(pick, "pmrow");
  if (rows.length !== 2) fail("в выборе не обе рабочие сессии: " + dump(pick));
  if (dump(rows[0]).trim() !== "chat-XR-9-2") {
    fail("свежайшая по реплике сессия стоит в выборе не первой: " + dump(pick));
  }
  // Вслепую при этом не прервали ничего: до ответа человека ушёл ровно один
  // запрос, тот, что вернул список.
  if (calls.filter((c) => c.way === "DELETE").length !== 1) {
    fail("стоп ушёл вслепую, не дождавшись выбора: " + JSON.stringify(calls));
  }
  stopAnswer = null;
  calls.length = 0;
  press(rows[1]);
  await settle();
  if (!last() || !last().path.includes("session=")) {
    fail("выбранная сессия не доехала до ручки стопа: " + JSON.stringify(calls));
  }
  if (!decodeURIComponent(last().path).includes("sid-старый")) {
    fail("стоп ушёл не в выбранную сессию: " + last().path);
  }
  if (byClass(box, "spick")) fail("выбор не закрылся после нажатия");
}

console.log("poc_rowstop: ok");

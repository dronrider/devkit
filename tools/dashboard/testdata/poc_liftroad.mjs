// Стенд кнопки «Поднять» (DK-673).
//
// Кнопка своей ручки подъёма не имеет и шлёт обычную реплику, а ручка выбирает
// дорогу сама. Пока имя окна числилось за мёртвым разговором, дорогой выходили
// клавиши в чужой терминал, и панель на любой ответ говорила «разговор поднят
// резюмом»: человек читал успех, текст уезжал постороннему, лента не менялась.
// Отсюда предмет стенда: панель повторяет ту дорогу, какую назвала ручка.
//
// Зовётся: node testdata/poc_liftroad.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "DK", sections: [{ key: "check", rows: [
  { id: "DK-673", title: "Реплика в чужую сессию", sect: "check" }] }] };
const models = [{ model: "fable", tier: "max", harness: "claude-code" }];

// Разговор без своего процесса: карточка показывает строку подъёма и кнопку.
const dead = { id: "b7cc7ae5", project: "devkit", title: "Разбор XR-207",
  mtime: "2026-08-31T21:09:00Z", tasks: ["DK-673"], model: "fable", state: "dead",
  idle: true, own: false, bound: "lead" };

// Ответ ручки на нажатие кнопки. Стенд подменяет его целиком: дорогу выбирает
// сервер, и панель обязана повторить любую.
let way = { way: "send-keys", tmux: "chat-DK-161-1",
  message: "реплика подана прямо в процесс агента: ответ придёт в ленту" };
const { sandbox } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST" && p.endsWith("/say")) return way;
  if (p.includes("/sessions/")) return { session: dead.id, head: { id: dead.id }, items: [], total: 0 };
  if (p.includes("/chats")) return { chats: [dead], models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

// Слова исхода сами по себе: каждой дороге своя строка, и про резюм говорит
// только резюм.
const lifted = sandbox.chatLiftWord({ way: "resume", tmux: "chat-DK-673-1" });
if (!lifted.includes("резюмом")) fail("резюм не назван резюмом: " + lifted);
for (const road of ["send-keys", "socket", "answer", "ask"]) {
  const said = sandbox.chatLiftWord({ way: road });
  if (said.includes("резюм")) {
    fail("дорога " + road + " названа резюмом: " + said);
  }
  if (!said.includes("не пришлось")) {
    fail("дорога " + road + " не сказала, что подъёма не было: " + said);
  }
}
const mute = sandbox.chatLiftWord({});
if (mute.includes("резюм")) fail("молчание ручки прочитано как резюм: " + mute);

// Та же проверка кнопкой: нажатие ушло репликой, ручка ответила клавишами, и
// панель говорит про клавиши, а не про подъём.
const st = await sandbox.chatState("devkit", dead.id, board);
const panel = sandbox.chatPanel("devkit", st);
await settle();
const up = deepBtn(byClass(panel, "cnosess"), "Поднять");
if (!up) fail("кнопки подъёма нет: " + dump(panel).slice(0, 300));
(up.handlers.click || up.onclick)({});
await settle();
const flashes = dump(sandbox.document.getElementById("flashes"));
if (flashes.includes("поднят резюмом")) {
  fail("панель сказала про резюм, которого не было: " + flashes);
}
if (!flashes.includes("не пришлось")) {
  fail("панель не назвала дорогу подачи: " + flashes);
}

// Настоящий резюм панель называет резюмом, как и прежде.
way = { way: "resume", tmux: "chat-DK-673-2", model: "fable" };
(up.handlers.click || up.onclick)({});
await settle();
if (!dump(sandbox.document.getElementById("flashes")).includes("резюмом")) {
  fail("подъём резюмом остался без слов: " + dump(sandbox.document.getElementById("flashes")));
}

console.log("кнопка подъёма называет ту дорогу, какой заказ уехал");

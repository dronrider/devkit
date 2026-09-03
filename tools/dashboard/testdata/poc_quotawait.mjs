// Стенд ретрая кончившейся подписки (DK-647).
//
// Разбор 480 сессий машины за 10 дней нашёл 11 полностью немых смертей.
// Харнес упирается в лимит подписки, берёт реплику клавишами так же охотно,
// как живой, и молчит часами, ничего не пишет ни в транскрипт, ни в ошибку.
// Сервер уже узнаёт ретрай по пейну и называет причину со сроком полем
// entry.quota (quotawait_test.go). Предмет этого стенда это панель. Причина и
// срок обязаны стоять в живом статусе открытого разговора над лентой (та же
// строка, что и у Login, Stuck, Gone) и чипом в выпадающем списке разговоров,
// а не только в машинном поле ответа.
//
// Зовётся: node testdata/poc_quotawait.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const QUOTA_NOTE = "лимит подписки исчерпан, сброс 15:00";

// --- чистая логика: причина и срок не гасят поле ввода ---
{
  const { sandbox } = makeSandbox(app, () => ({}));
  const way = sandbox.chatWay({ sid: "s1", entry: { state: "live", quota: QUOTA_NOTE } });
  if (way.off) fail("ретрай квоты запер поле ввода: реплика всё равно уедет и встанет в очередь");
  if (way.why !== QUOTA_NOTE) fail("причина и срок не дошли до статуса разговора: " + JSON.stringify(way));
  // Разговор без ретрая идёт прежней дорогой. Живая сессия молчит статусом.
  const plain = sandbox.chatWay({ sid: "s1", entry: { state: "live" } });
  if (plain.why) fail("обычный живой разговор обзавёлся статусом ретрая: " + JSON.stringify(plain));
}

// --- живой статус: та же строка стоит над лентой открытого разговора ---
{
  const sid = "aaaa9999-1111-4111-8111-111111111111";
  const board = { prefix: "DK", sections: [{ key: "check", rows: [] }] };
  const chats = [{ id: sid, title: "продолжай", tasks: [], model: "sonnet",
    tmux: "chat-9", state: "live", idle: false, quota: QUOTA_NOTE }];
  const { sandbox } = makeSandbox(app, (path) => {
    if (path.includes("/chats")) return { chats, models: [] };
    if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
    if (path.endsWith("/board")) return { board, works: [] };
    return {};
  });
  const st = await sandbox.chatState("devkit", sid, board, []);
  if (!st.entry || st.entry.quota !== QUOTA_NOTE) fail("состояние панели не донесло причину: " + JSON.stringify(st.entry));
  const panel = sandbox.chatPanel("devkit", st);
  await settle();
  const note = byClass(panel, "cnote");
  if (!note) fail("живой статус ретрая не встал над лентой: " + dump(panel).slice(0, 400));
  if (!dump(note).includes(QUOTA_NOTE)) fail("статус не назвал причину и срок: " + dump(note));
  // Поле ввода не заперто. Класс idle это признак гашения (way.off), у ретрая
  // квоты его быть не должно.
  if (note.className.includes("idle")) fail("статус ретрая ошибочно погасил поле ввода");
}

// --- выпадающий список: та же причина видна до открытия разговора ---
{
  const { sandbox } = makeSandbox(app, () => ({}));
  const row = sandbox.chatOption("devkit", { id: "s1", state: "live", idle: false, quota: QUOTA_NOTE }, "");
  const chip = byClass(row, "c-quota");
  if (!chip) fail("в строке списка нет чипа ретрая квоты: " + dump(row));
  if (!dump(chip).includes(QUOTA_NOTE)) fail("чип не называет причину и срок: " + dump(chip));
  // Разговор без ретрая чипа не получает.
  const plainRow = sandbox.chatOption("devkit", { id: "s2", state: "live", idle: false }, "");
  if (byClass(plainRow, "c-quota")) fail("обычная строка обзавелась чипом ретрая квоты");
}

console.log("ретрай кончившейся подписки виден живым статусом и чипом списка, поле ввода не гасится");

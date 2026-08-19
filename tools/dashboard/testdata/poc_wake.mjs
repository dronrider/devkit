// Стенд пробуждения ленты (ветка poc-chat). Предмет проверки это догон
// пропущенного и переподключение потока: EventSource шлёт только новое, а
// вкладка, задушенная браузером на время сна ноутбука, возвращается с мёртвым
// объектом, у которого readyState навсегда CLOSED. Ровно это человек и видел:
// после двадцати минут сна лента показывала разговор таким, каким он был до
// сна, и лечилось только перезагрузкой страницы.
//
// Зовётся: node testdata/poc_wake.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const session = { id: "aaaa1111-1111", mtime: "2026-08-13T10:02:00+03:00", first: "Выполни XR-1" };

// Лента разговора: стенд дописывает сюда реплики, будто их принёс чужой ход.
const talk = [
  { seq: 0, role: "user", text: "быстрый вопрос", time: "2026-08-13T09:00:00+03:00" },
  { seq: 1, role: "assistant", text: "быстрый ответ", time: "2026-08-13T09:00:30+03:00" },
];

const app = appPathArg();
const { sandbox, streams, asked } = makeSandbox(app, (path) => {
  if (path.includes("/sessions/") && !path.includes("stream=1")) {
    return { session: session.id, head: session, items: talk.slice(), total: talk.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works: [] };
  return {};
});

// Потоков в дашборде два вида: лента разговора и лента уведомлений, которую
// app.js поднимает сам при загрузке. Стенду интересен только первый.
const sess = () => streams.filter((s) => String(s.url).includes("/sessions/"));

const box = makeNode("div");
sandbox.wireChatFeed("demo", box, session.id);
await settle();

if (sess().length !== 1) fail("поток не поднялся: " + sess().length);
const first = sess()[0];
if (!dump(box).includes("быстрый ответ")) fail("хвост не встал в ленту: " + dump(box));

// Ноутбук уснул: вкладку задушили, поток умер, а в разговор доехали новые
// реплики. Возврат к вкладке обязан догнать пропуск запросом.
talk.push({ seq: 2, role: "assistant", text: "пока ты спал, я доделал",
  time: "2026-08-13T10:20:00+03:00" });
first.readyState = 2;
asked.length = 0;
streams.length = 0;
sandbox.document.handlers.visibilitychange();
await settle();

if (!asked.some((p) => p.includes("/sessions/" + session.id + "?n="))) {
  fail("возврат к вкладке не дочитал хвост запросом: " + JSON.stringify(asked));
}
if (sess().length !== 1) fail("мёртвый поток не пересоздан: " + sess().length);
if (!dump(box).includes("пока ты спал")) fail("пропущенная реплика не встала в ленту");

// Догон не двоит: те же реплики приходят и потоком, и запросом, а в ленте
// каждая одна.
const twice = (dump(box).match(/пока ты спал/g) || []).length;
if (twice !== 1) fail("догон удвоил реплику: " + twice);

// Живой поток пробуждение не рвёт: браузер ретраит сам, и второй поток рядом с
// его попыткой удваивал бы события.
const kept = sess()[0];
kept.readyState = 1;
streams.length = 0;
sandbox.document.handlers.visibilitychange();
await settle();
if (sess().length !== 0) fail("живой поток пересоздан без нужды: " + sess().length);

// Обрыв: пока браузер ретраит сам (readyState не CLOSED), лента не вмешивается.
streams.length = 0;
kept.readyState = 0;
if (kept.onerror) kept.onerror();
await settle();
if (sess().length !== 0) fail("лента полезла переподключаться поверх браузерного ретрая");

console.log("пробуждение ленты: возврат к вкладке догоняет пропуск и поднимает мёртвый поток, " +
  "догон не двоит реплик, живой поток не рвётся, браузерному ретраю лента не мешает");

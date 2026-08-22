// Стенд пробуждения ленты (ветка poc-chat). Предмет проверки это догон
// пропущенного и переподключение потока: EventSource шлёт только новое, а
// вкладка, задушенная браузером на время сна ноутбука, возвращается с мёртвым
// объектом, у которого readyState навсегда CLOSED. Ровно это человек и видел:
// после двадцати минут сна лента показывала разговор таким, каким он был до
// сна, и лечилось только перезагрузкой страницы.
//
// Зовётся: node testdata/poc_wake.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const session = { id: "aaaa1111-1111", mtime: "2026-08-13T10:02:00+03:00", first: "Выполни XR-1" };

// Лента разговора: стенд дописывает сюда реплики, будто их принёс чужой ход.
const talk = [
  { seq: 0, role: "user", text: "быстрый вопрос", time: "2026-08-13T09:00:00+03:00" },
  { seq: 1, role: "assistant", text: "быстрый ответ", time: "2026-08-13T09:00:30+03:00" },
];

const app = appPathArg();
const { sandbox, streams, asked, timers } = makeSandbox(app, (path) => {
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

// Возврат к вкладке при открытом чате: догон просит хвост длиннее открытого
// окна (200 против 40), и приезжает он целым куском ленты, а не одними
// пропущенными записями. Человек видел ровно это: ушёл на соседнюю вкладку,
// вернулся, и под последней репликой лежала сотня старых записей, которых там
// быть не должно. Стенд держит три свойства разом: чужого в ленте не
// прибавилось, ключи не задвоились, поток остался один.
{
  const old = [];
  for (let i = 0; i < 60; i++) {
    old.push({ seq: i, key: "m:" + i, role: i % 2 ? "assistant" : "user",
      text: (i < 20 ? "старое " : "своё ") + i, time: "2026-08-13T09:00:00+03:00" });
  }
  const tailOf = (path) => {
    const m = /[?&]n=(\d+)/.exec(path);
    const n = m ? Number(m[1]) : old.length;
    const from = Math.max(0, old.length - n);
    return { session: "deep", head: { id: "deep" }, items: old.slice(from),
      total: old.length, start: from === 0 };
  };
  const prev = sandbox.fetch;
  sandbox.fetch = (path, init) => {
    if (path.includes("/sessions/deep") && !path.includes("stream=1")) {
      return Promise.resolve({ ok: true, status: 200,
        text: () => Promise.resolve(JSON.stringify(tailOf(path))),
        json: () => Promise.resolve(tailOf(path)) });
    }
    return prev(path, init);
  };

  const deep = makeNode("div");
  streams.length = 0;
  sandbox.wireChatFeed("demo", deep, "deep");
  await settle();
  const rows = (box) => allByClass(box, "frow").length;
  const before = rows(deep);
  if (before !== 40) fail("хвост встал не по CHAT_TAIL: " + before);
  if (dump(deep).includes("старое 0")) fail("свежий заход притащил историю глубже хвоста");

  // Пока вкладка спала, разговор дописали одной репликой.
  old.push({ seq: 60, key: "m:60", role: "assistant", text: "новое слово",
    time: "2026-08-13T10:20:00+03:00" });
  const alive = streams.filter((x) => String(x.url).includes("/sessions/deep"));
  streams.length = 0;
  sandbox.document.handlers.visibilitychange();
  await settle();

  const text = dump(deep);
  if (text.includes("старое 0") || text.includes("старое 19")) {
    fail("пробуждение вкладки влило в чат записи, которых там не было");
  }
  if (!text.includes("новое слово")) fail("пробуждение не догнало дописанную реплику");
  if ((text.match(/новое слово/g) || []).length !== 1) fail("догон удвоил дописанную реплику");
  const after = rows(deep);
  if (after !== before + 1) fail("после пробуждения строк стало " + after + " вместо " + (before + 1));
  const keys = allByClass(deep, "frow").map((n) => n.dataset && n.dataset.pkey).filter(Boolean);
  if (new Set(keys).size !== keys.length) fail("ключи записей задвоились: " + keys.length);
  const live = streams.filter((x) => String(x.url).includes("/sessions/deep"));
  if (alive.length !== 1 || live.length !== 0) {
    fail("живой поток пересоздан пробуждением: было " + alive.length + ", прибавилось " + live.length);
  }
  sandbox.fetch = prev;
}

// Холодное открытие: лента встаёт вниз и остаётся там, когда начальный экран
// дорисовывается асинхронно. Миниатюры вложений догружаются картинками, большой
// блок работы субагента меряется позже, и всякая дорисовка сдвигает содержимое
// из-под уже выставленной прокрутки: человек видел прыжок вверх ровно на
// выросшую высоту (замечание про перезагрузку страницы).
{
  const many = [];
  for (let i = 0; i < 30; i++) {
    many.push({ seq: i, key: "a:" + i, role: "assistant", text: "шаг " + i, sub: "работа",
      time: "2026-08-13T09:00:00+03:00" });
  }
  const prev = sandbox.fetch;
  sandbox.fetch = (path, init) => {
    if (path.includes("/sessions/") && !path.includes("stream=1")) {
      const said = { session: "cold", head: { id: "cold" }, items: many, total: many.length };
      return Promise.resolve({ ok: true, status: 200,
        text: () => Promise.resolve(JSON.stringify(said)),
        json: () => Promise.resolve(said) });
    }
    return prev(path, init);
  };

  // Открытие в хвост: после дорисовки лента обязана остаться внизу.
  const box = makeNode("div");
  box.clientHeight = 300;
  sandbox.wireChatFeed("demo", box, "cold");
  await settle();
  // Дорисовка меряется на строке ленты: блока работы субагента больше нет, а
  // высота её всё так же встаёт позже открытия (разметка, миниатюры).
  const grown = byClass(box, "frow");
  if (!grown) fail("строка работы субагента не собралась");
  grown.own = 900;
  for (const t of timers.splice(0)) t.fn();
  await settle();
  if (box.scrollTop < box.scrollHeight - box.clientHeight - 5) {
    fail("после дорисовки лента уехала вверх: top=" + box.scrollTop + " из " + box.scrollHeight);
  }

  // Жест человека снимает удержание: дальше прокрутка его, а не ленты.
  const box2 = makeNode("div");
  box2.clientHeight = 300;
  sandbox.wireChatFeed("demo", box2, "cold");
  await settle();
  if (box2.handlers.wheel) box2.handlers.wheel({});
  box2.scrollTop = 100;
  const grown2 = byClass(box2, "frow");
  if (grown2) grown2.own = 900;
  for (const t of timers.splice(0)) t.fn();
  await settle();
  if (box2.scrollTop !== 100) {
    fail("лента отобрала прокрутку после жеста человека: " + box2.scrollTop);
  }
  sandbox.fetch = prev;
}

console.log("пробуждение ленты: возврат к вкладке догоняет пропуск и поднимает мёртвый поток, " +
  "чужих записей глубже хвоста в чат не приносит и ключей не двоит, " +
  "догон не двоит реплик, живой поток не рвётся, браузерному ретраю лента не мешает, " +
  "холодное открытие держит место через асинхронные дорисовки и отдаёт прокрутку по жесту");

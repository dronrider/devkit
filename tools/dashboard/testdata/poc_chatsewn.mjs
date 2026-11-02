// Стенд переезда панели к родившемуся разговору (первая реплика в чат с
// умершей сессией).
//
// Живой случай: «при отправке в чат первого сообщения лента на время пустеет
// полностью, будто сообщение пропало» (замечание пользователя). Реплика в
// мёртвый разговор уходит ручкой /say, сервер отвечает way=start и поднимает
// продолжение, панель переезжает на новый sid. Пузырь отправки при этом уже
// был снят с персиста удачной отправкой, переезд нёс пустой ключ, и до
// приезда реплики в транскрипт человек смотрел на пустую ленту.
//
// Предмет стенда: после удачной отправки пузырь стоит в персисте; переезд
// перевозит его к родившемуся разговору; пересобранная панель показывает
// пузырь поверх пустого транскрипта; приехавшая лента снимает пузырь ровно
// один раз, и дубля реплики нигде не встаёт.
//
// Панель поднимается самой загрузкой приложения на адресе мёртвого
// разговора, как у человека по ссылке: сборка панели в отрыве от экрана
// мимо показанного проекта проверяемое поведение не задаёт.
//
// Зовётся: node testdata/poc_chatsewn.mjs static/app.js

import { makeSandbox, settle, dump, tag, allByClass, deepBtn, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const OLD = "aaaa1111-0001";
const NEW = "aaaa1111-0002";
const TMUX = "chat-sewn";
const TEXT = "реплика в чат с умершей сессией";

// Мир: старый разговор с умершей сессией; продолжение рождается по имени
// tmux, реплика ложится в транскрипт, когда стенд это разрешит.
const models = [{ model: "opus", tier: "pro", harness: "claude-code" }];
const oldChat = { id: OLD, title: "прошлый разговор", tasks: [], state: "dead",
  idle: true, model: "opus", mtime: "2026-11-02T20:00:00+03:00" };
const newChat = { id: NEW, title: TEXT.slice(0, 24), tasks: [], state: "live",
  idle: true, model: "opus", mtime: "2026-11-02T21:00:00+03:00" };
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
let born = false;
let said = false;

const { sandbox, store, timers, streams } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST" && p.includes("/chats/" + OLD + "/say")) {
    born = true;
    return { way: "start", tmux: TMUX };
  }
  if (p.includes("/chats") && p.includes("tmux=" + TMUX)) {
    return { chats: born ? [newChat] : [], dead: null };
  }
  if (p.includes("/chats")) {
    return { chats: born ? [oldChat, newChat] : [oldChat], models, days: 3, older: false };
  }
  if (p.includes("/sessions/")) {
    const sid = p.slice(p.indexOf("/sessions/") + 10).split("?")[0];
    if (sid === OLD) {
      return { session: OLD, head: { id: OLD, first: "прошлый разговор", live: false,
        mtime: oldChat.mtime, model: "opus", tasks: [] }, items: [], total: 0 };
    }
    if (sid === NEW) {
      const items = said ? [{ key: "tr-1", role: "user",
        time: "2026-11-02T21:00:01+03:00", text: TEXT, wire: TEXT }] : [];
      return { session: NEW, head: { id: NEW, first: TEXT.slice(0, 24), live: true,
        mtime: newChat.mtime, model: "opus", tasks: [] }, items, total: items.length };
    }
  }
  if (p.endsWith("/projects")) {
    return { projects: [{ name: "demo", title: "Демо", sections: {}, works: [] }] };
  }
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
}, { hash: "#demo/chat/" + OLD });

// Панельные ошибки в песочнице гасятся catch'ем на console.error: без сбора
// стенд молчал бы о причине. Провал стенда печатает всё собранное.
const errs = [];
sandbox.console.error = (...a) => { errs.push(a.map((x) => String(x && x.stack || x)).join(" ").slice(0, 400)); };
const fail = (msg) => {
  console.error(msg);
  for (const e of errs) console.error("панель: " + e);
  process.exit(1);
};

await settle();

const visibleSlot = () => allByClass(sandbox.document.getElementById("cpin"), "cslot")
  .find((s) => !s.classList.contains("off")) || null;

// dump печатает и скрытое: панель носит скрытые запасные блоки (вход, пул
// списков), и без обхода родителей по hidden они попадали бы в счёт строк.
const seen = (n) => {
  for (let p = n; p; p = p.parentNode) if (p.hidden) return false;
  return true;
};

// Шаги опроса реестра идут таймерами на полторы секунды, и прокачивать надо
// только их: прочие круги панели (пульс, вопрос, сторож пузыря) остаются в
// очереди, иначе сторож пузыря состарил бы его раньше эха.
const pumpAsk = async () => {
  for (let guard = 0; guard < 80; guard += 1) {
    if (sandbox.location.hash === "#demo/chat/" + NEW) return true;
    const at = timers.findIndex((t) => t.ms === 1500);
    if (at < 0) {
      await new Promise((ok) => setTimeout(ok, 5));
      continue;
    }
    const [t] = timers.splice(at, 1);
    t.fn();
    await settle();
  }
  return sandbox.location.hash === "#demo/chat/" + NEW;
};

const panel = visibleSlot();
if (!panel) {
  fail("панель мёртвого разговора не собралась на загрузке: " +
    dump(sandbox.document.getElementById("cpin")).slice(0, 300));
}

// --- удачная отправка: пузырь уезжает в персист, а не в никуда ---
{
  const ta = tag(panel, "TEXTAREA");
  if (!ta) fail("в панели нет поля ввода");
  ta.value = TEXT;
  ta.handlers.input();
  await settle();
  const send = deepBtn(panel, "Отправить");
  if (!send) fail("в панели нет кнопки отправки: " + dump(panel).slice(0, 200));
  send.handlers.click({ stopPropagation: () => {} });
  await settle();

  const kept = JSON.parse(store.get("devkit.chat.pend.demo/" + OLD) || "[]");
  if (kept.length !== 1 || kept[0].state !== "sent") {
    fail("доставленная реплика не легла в персист: " + JSON.stringify(kept));
  }
  if (!dump(panel).includes(TEXT)) fail("пузырь отправки не встал в ленту");
}

// --- переезд: опрос реестра находит продолжение, панель переезжает сама ---
if (!(await pumpAsk())) {
  fail("панель не переехала к родившемуся разговору: " + sandbox.location.hash);
}
await settle();
if (store.has("devkit.chat.pend.demo/" + OLD)) {
  fail("старый ключ персиста пережил переезд: " + store.get("devkit.chat.pend.demo/" + OLD));
}
const carried = JSON.parse(store.get("devkit.chat.pend.demo/" + NEW) || "[]");
if (carried.length !== 1 || carried[0].state !== "sent") {
  fail("переезд потерял доставленную реплику: " + JSON.stringify(carried));
}

// Пустой транскрипт продолжения: человек всё ещё видит свою реплику, а не
// пустую ленту. Ровно тут раньше стояла пустота (замечание пользователя).
const slot = visibleSlot();
if (!slot) fail("после переезда нет видимого слота панели");
const mine = allByClass(slot, "m-local");
if (mine.length !== 1) fail("после переезда своя реплика не стоит в ленте: " + mine.length);
if (!dump(mine[0]).includes(TEXT)) {
  fail("переехавший пузырь потерял текст: " + dump(mine[0]).slice(0, 200));
}
if (!dump(mine[0]).includes("доставлено")) {
  fail("переехавший пузырь выглядит недоставленным: " + dump(mine[0]).slice(0, 200));
}
// Своё имя несёт и обёртка пузыря (frow r-user gtop f-bub), и пузырь остаётся
// единственной видимой строкой: чужая запись входа живёт скрытой и счёту не
// подлежит.
const rows = allByClass(slot, "frow")
  .filter((r) => !allByClass(r, "m-local").length && seen(r));
if (rows.length) {
  fail("пустой транскрипт продолжения рисует строки: " +
    dump(rows[0]).slice(0, 200));
}

// --- эхо из ленты: пузырь снимается один раз, дубля нет ---
said = true;
const es = streams.find((s) => String(s.url).includes("/sessions/" + NEW) &&
  String(s.url).includes("stream=1"));
if (!es) fail("живой поток ленты продолжения не открыт");
es.onmessage({ data: JSON.stringify({ key: "tr-1", role: "user",
  time: "2026-11-02T21:00:01+03:00", text: TEXT, wire: TEXT }) });
await settle();

const left = allByClass(visibleSlot(), "m-local");
if (left.length) {
  fail("приехавшая из ленты копия не сняла пузырь: " + dump(visibleSlot()).slice(0, 200));
}
if (store.has("devkit.chat.pend.demo/" + NEW)) {
  fail("сверенный пузырь остался в персисте: " + store.get("devkit.chat.pend.demo/" + NEW));
}
const hit = allByClass(visibleSlot(), "frow").filter((r) => dump(r).includes(TEXT));
if (hit.length !== 1) {
  fail("реплика в ленте не одной строкой: " + hit.length + " " +
    dump(visibleSlot()).slice(0, 300));
}

console.log("пузырь пережил удачную отправку, переезд и пустой транскрипт; эхо сняло его один раз");

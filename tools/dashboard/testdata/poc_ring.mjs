// Стенд кольца агентов в шапке разговора (ветка poc-chat, макет пользователя).
// Предмет проверки это собранное кольцо и строка состояния под названием
// разговора, а не текст исходника: четыре состояния кольца различаются классом
// обёртки, закраской сегментов и наличием числа, и ошибка тут выглядит на
// экране работающим агентом там, где на самом деле тишина.
//
// Зовётся: node testdata/poc_ring.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const chats = [
  { id: "aaaa1111-1111", title: "Выполни XR-1", mtime: "2026-08-13T10:02:00+03:00",
    tasks: ["XR-1"], model: "opus", tmux: "task-XR-1", state: "live", tree: "xr-1" },
];
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "дашборд без дёрганья", sect: "in-progress" },
] }] };

const now = Math.floor(Date.now() / 1000);

// Четыре состояния кольца, как их отдаёт ручка-агрегат /pulse.
const pulses = {
  working: {
    task: "XR-1", state: "working", flow: true, count: 2, quiet: 60,
    phase: "тесты", about: "Bash go test ./tools/...", since: now - 12,
    phases: [
      { name: "код", done: true }, { name: "тесты", done: false, now: true },
      { name: "ревью", done: false }, { name: "слияние", done: false },
      { name: "выкат", done: false },
    ],
    agents: [
      { session: "aaaa1111-1111", name: "task-XR-1", state: "working",
        about: "Bash go test ./tools/...", since: now - 12 },
      { session: "bbbb2222-2222", name: "chat-XR-1-2", state: "silent",
        about: "Read app.js", since: now - 840 },
    ],
  },
  waiting: {
    task: "XR-1", state: "waiting", flow: false, count: 1, waiting: 1, quiet: 60,
    phase: "код", since: now - 240,
    wait: { state: "ждёт ответа", source: "ask", note: "спросил агент", since: now - 240 },
    phases: [
      { name: "код", done: false, now: true }, { name: "тесты", done: false },
      { name: "ревью", done: false }, { name: "слияние", done: false },
      { name: "выкат", done: false },
    ],
    agents: [{ session: "aaaa1111-1111", name: "task-XR-1", state: "waiting", since: now - 240 }],
  },
  silent: {
    task: "XR-1", state: "silent", flow: false, count: 1, quiet: 60,
    phase: "ревью", about: "Bash go build", since: now - 840,
    phases: [
      { name: "код", done: true }, { name: "тесты", done: true },
      { name: "ревью", done: false, now: true }, { name: "слияние", done: false },
      { name: "выкат", done: false },
    ],
    agents: [{ session: "aaaa1111-1111", name: "task-XR-1", state: "silent",
      about: "Bash go build", since: now - 840 }],
  },
  empty: {
    task: "XR-1", state: "empty", flow: false, count: 0, quiet: 60,
    phases: [
      { name: "код", done: false }, { name: "тесты", done: false },
      { name: "ревью", done: false }, { name: "слияние", done: false },
      { name: "выкат", done: false },
    ],
    agents: [],
  },
};

let mood = "working";
const app = appPathArg();
const { sandbox, moves } = makeSandbox(app, (path) => {
  if (path.includes("/pulse")) return pulses[mood];
  if (path.includes("/chats")) return { chats, models: [] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

// Шапка собирается один раз на состояние: кольцо ходит за пульсом само, и
// стенд ждёт его ответа.
async function headOf(which) {
  mood = which;
  const st = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const head = sandbox.chatHead("demo", st);
  await settle();
  return head;
}

function ringOf(head) {
  const wrap = byClass(head, "ringwrap");
  if (!wrap) fail("кольца в шапке нет: " + dump(head).slice(0, 200));
  return wrap;
}

// --- кольцо стоит слева от названия разговора ---
{
  const head = await headOf("working");
  const kids = head.children.map((k) => String(k.className));
  if (kids[0] !== "rslot") fail("кольцо стоит не первым в шапке: " + JSON.stringify(kids));
  if (!kids.includes("ct")) fail("названия разговора нет колонкой справа: " + JSON.stringify(kids));
  const ct = head.children.find((k) => k.className === "ct");
  if (!byClass(ct, "chline")) fail("строка с названием уехала из колонки");
}

// --- работа: сегменты по фазам, дуга бежит, число в середине ---
{
  const head = await headOf("working");
  const wrap = ringOf(head);
  if (!String(wrap.className).includes("r-working")) {
    fail("состояние работы не назвалось классом: " + wrap.className);
  }
  const segs = allByClass(wrap, "seg");
  if (segs.length !== 5) fail("сегментов не пять: " + segs.length);
  const done = segs.filter((s) => String(s.className).includes("on")).length;
  if (done !== 1) fail("закрашено не то число фаз: " + done);
  const here = segs.filter((s) => String(s.className).includes("here"));
  if (here.length !== 1) fail("идущая фаза не помечена: " + here.length);
  // Сегменты идут по окружности без наложения: смещение каждого следующего на
  // свою пятую часть, зазор между ними взят из длины дуги.
  const offs = segs.map((s) => Number(s.attrs["stroke-dashoffset"]));
  for (let i = 1; i < offs.length; i += 1) {
    if (!(offs[i] < offs[i - 1])) fail("сегменты стоят друг на друге: " + JSON.stringify(offs));
  }
  if (!byClass(wrap, "comet")) fail("бегущей дуги нет");
  const num = byClass(wrap, "rnum");
  if (!num || num.textContent !== "2") fail("в середине не число агентов: " + (num && num.textContent));
}

// --- ожидание: красное кольцо, ореол и число ждущих ---
{
  const head = await headOf("waiting");
  const wrap = ringOf(head);
  if (!String(wrap.className).includes("r-waiting")) {
    fail("ожидание не назвалось классом: " + wrap.className);
  }
  if (!byClass(wrap, "halo")) fail("ореола ожидания нет");
  const num = byClass(wrap, "rnum");
  if (!num || num.textContent !== "1") fail("число ждущих не то: " + (num && num.textContent));
  const cts = byClass(head, "cts");
  if (!dump(cts).includes("вопрос человеку") || !dump(cts).includes("4 мин без ответа")) {
    fail("строка состояния молчит про вопрос: " + dump(cts));
  }
}

// --- молчание: сегменты серые, дуга не бежит ---
{
  const head = await headOf("silent");
  const wrap = ringOf(head);
  if (!String(wrap.className).includes("r-silent")) fail("молчание не назвалось классом: " + wrap.className);
  const cts = byClass(head, "cts");
  if (!dump(cts).includes("тишина") || !dump(cts).includes("последний ход")) {
    fail("строка состояния не сказала про тишину: " + dump(cts));
  }
  if (!dump(cts).includes("14 мин")) fail("давность тишины не в минутах: " + dump(cts));
}

// --- пусто: кольцо без числа ---
{
  const head = await headOf("empty");
  const wrap = ringOf(head);
  if (!String(wrap.className).includes("r-empty")) fail("пустота не назвалась классом: " + wrap.className);
  if (byClass(wrap, "rnum")) fail("у пустого кольца стоит число");
  const cts = byClass(head, "cts");
  if (!dump(cts).includes("живых сессий нет")) fail("строка состояния не сказала про пустоту: " + dump(cts));
}

// --- список агентов: точка состояния, что делает, давность, дорога в чат ---
{
  const head = await headOf("working");
  const wrap = ringOf(head);
  const pop = byClass(wrap, "pop");
  if (!pop) fail("списка агентов нет");
  const rows = allByClass(pop, "prow");
  if (rows.length !== 2) fail("в списке не те строки: " + rows.length);
  if (!byClass(rows[0], "p-working")) fail("у работающего агента точка не того состояния");
  if (!byClass(rows[1], "p-silent")) fail("у молчащего агента точка не того состояния");
  if (!dump(rows[0]).includes("go test") || !dump(rows[0]).includes("12 с")) {
    fail("строка агента не сказала, что он делает: " + dump(rows[0]));
  }
  if (!dump(rows[1]).includes("тишина 14 мин")) fail("молчание агента без срока: " + dump(rows[1]));
  if (!dump(pop).includes("клик по строке открывает разговор")) fail("подвала списка нет");
  const was = moves.length;
  rows[1].handlers.click({ stopPropagation: () => {} });
  if (moves.length === was || !String(moves[moves.length - 1][1]).includes("bbbb2222-2222")) {
    fail("клик по строке не открыл разговор: " + JSON.stringify(moves.slice(was)));
  }
  // На таче наведения нет, и список открывается нажатием на само кольцо.
  wrap.handlers.click({ stopPropagation: () => {} });
  if (!String(wrap.className).includes("open")) fail("нажатие не открыло список на таче");
}

// --- давность словами: секунды ниже минуты, дальше минуты и часы ---
for (const [ago, want] of [[12, "12 с"], [240, "4 мин"], [4000, "1 ч 6 мин"]]) {
  const said = sandbox.pulseAge(now - ago, now * 1000);
  if (said !== want) fail("давность " + ago + " с сказана как " + said + ", ждал " + want);
}

console.log("кольцо агентов: место в шапке, четыре состояния, фазы сегментами, " +
  "список агентов с дорогой в разговор, давность словами");

// Стенд плашки работы как состояния чата (DK-893).
//
// Живой случай: разбор чата 9 сентября 2026. Человек написал реплику в 23:15,
// модель думала шесть минут, первая запись легла в транскрипт в 23:21, и всё
// это время панель показывала пустую ленту. Данные были верными: реестр
// клиента нёс busy с 23:15, процесс был жив, ручка /status отвечала live и
// busy. Плашку поднимало только нажатие «Отправить» в этой же панели, и ход,
// начатый до открытия чата, панели не доставался вовсе. Второй дырой был
// потолок в десять минут: плашка гасла сама, жив процесс или нет.
//
// Предмет стенда: плашка встаёт по ответу ручки состояния с открытия панели, в
// ней идёт секундный счётчик от начала хода, ход длиннее десяти минут её не
// гасит, пропавший посреди хода процесс красит её своими словами, а строка
// списка чатов несёт минуты хода рядом с признаком занятости.
//
// Зовётся: node testdata/poc_chatbusylive.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const SID = "eeee7777-0893";

// Ход в шесть минут двенадцать секунд: ровно тот, на котором панель молчала.
let status = { live: true, busy: true, sec: 372, since: Date.now() - 372000 };

const { sandbox, timers } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats") && path.endsWith("/status")) return status;
  if (path.includes("/chats") && path.endsWith("/say")) return { ok: true };
  if (path.includes("/chats")) {
    return { chats: [{ id: SID, state: "live", tmux: "chat-XR-1:@1.%1", idle: false,
      sec: 372, title: "живой разговор" }], models: [] };
  }
  if (path.includes("/sessions/" + SID)) return { items: [], start: true };
  if (path === "/api/harnesses") return { harnesses: [] };
  return {};
});

// Часы стенда двигаются руками: ход длиннее десяти минут иначе не проиграть, а
// предмет проверки это как раз снятый потолок. Песочница отдаёт свой глобальный
// объект, и подменённый Date виден коду панели с первого же вызова.
let skew = 0;
const RealDate = sandbox.Date;
class FakeDate extends RealDate {
  constructor(...args) {
    if (!args.length) super(RealDate.now() + skew);
    else super(...args);
  }
  static now() { return RealDate.now() + skew; }
}
sandbox.Date = FakeDate;

const board = { prefix: "XR", sections: [] };

// Панель заново на каждый случай: плашка это память самой сборки makeBusy.
const freshPanel = async () => {
  const st = await sandbox.chatState("demo", SID, board);
  if (st.sid !== SID) fail("состояние не встало на живую сессию: " + JSON.stringify(st));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  return panel;
};

// Ближайший опрос состояния: шаг у него полторы секунды, и берётся свежайший,
// потому что в общем списке таймеров стенда лежат отработавшие записи прежних
// случаев.
const nextPoll = () => {
  const poll = timers.filter((t) => t.ms === 1500 && t.fn).pop();
  if (!poll) fail("слежение за состоянием чата не осталось в деле");
  return poll;
};

const runPoll = async () => {
  const poll = nextPoll();
  poll.fn();
  poll.fn = null;
  await settle();
};

// Секундный тик счётчика: он идёт своим таймером, а не опросом, иначе время в
// плашке шло бы рывками через секунду на третью.
const runBeat = async () => {
  const beat = timers.filter((t) => t.ms === 1000 && t.fn).pop();
  if (!beat) fail("секундный счётчик хода не заведён");
  beat.fn();
  beat.fn = null;
  await settle();
};

// --- плашка встаёт с открытия чата, без единого нажатия ---
{
  const panel = await freshPanel();
  const plate = byClass(panel, "busyrow");
  if (!plate || plate.hidden) {
    fail("чат открыт посреди чужого хода, а плашки работы нет: " + dump(panel).slice(0, 400));
  }
  // Счётчик идёт от начала хода, а не от открытия панели: человек застал ход
  // на шестой минуте и должен это видеть.
  if (!dump(plate).includes("6 мин 12 с")) {
    fail("в плашке нет возраста хода: " + dump(plate));
  }

  // --- ход длиннее десяти минут плашку не гасит ---
  skew += 12 * 60 * 1000;
  status = { live: true, busy: true, sec: 372 + 12 * 60, since: status.since };
  await runPoll();
  await runBeat();
  if (plate.hidden) {
    fail("плашка погасла на восемнадцатой минуте хода, хотя реестр говорит busy: " + dump(plate));
  }
  if (!dump(plate).includes("18 мин 12 с")) {
    fail("счётчик не досчитал до восемнадцатой минуты: " + dump(plate));
  }

  // --- сессия освободилась: плашка гаснет сама ---
  status = { live: true, busy: false };
  await runPoll();
  if (!plate.hidden) fail("сессия сказалась свободной, а плашка горит: " + dump(plate));
}

// --- процесс пропал посреди хода: плашка красится и называет пропажу ---
{
  skew = 0;
  status = { live: true, busy: true, sec: 372, since: RealDate.now() - 372000 };
  const panel = await freshPanel();
  const plate = byClass(panel, "busyrow");
  if (!plate || plate.hidden) fail("плашка не встала перед пропажей процесса");
  const at = new RealDate(2026, 8, 9, 23, 19, 0).getTime();
  status = { live: false, busy: false, gone: true, at };
  await runPoll();
  if (plate.hidden) {
    fail("процесс пропал посреди хода, а панель молча погасила плашку: " + dump(plate));
  }
  if (!plate.className.includes("stop")) {
    fail("плашка пропажи не покрашена остановкой: " + dump(plate));
  }
  const said = dump(plate);
  if (!said.includes("агент пропал в 23:19 посреди хода") ||
      !said.includes("реплика поднимет разговор снова")) {
    fail("пропажа не названа своими словами: " + said);
  }
  // Размышление от пропажи отличимо: счётчика тут нет вовсе, ждать больше
  // нечего.
  if (said.includes(" с") && said.includes("мин ")) {
    fail("в плашке пропажи остался счётчик хода: " + said);
  }
}

// --- строка списка чатов несёт минуты хода рядом с признаком занятости ---
{
  const row = sandbox.chatOption("demo", { id: SID, state: "live", idle: false, sec: 372,
    title: "живой разговор" }, "", null);
  if (!dump(row).includes("активна, 6 мин")) {
    fail("в строке списка нет минут хода рядом с признаком занятости: " + dump(row));
  }
  const idle = sandbox.chatOption("demo", { id: SID, state: "live", idle: true,
    title: "молчащий разговор" }, "", null);
  if (dump(idle).includes("мин")) {
    fail("строка молчащего разговора подписана минутами хода: " + dump(idle));
  }
}

// --- разговор ожил новым ходом, начатым не отсюда: панель гасила плашку по
// мёртвому процессу, и поднять её обязан тот же опрос ---
{
  skew = 0;
  status = { live: false, busy: false };
  const panel = await freshPanel();
  const plate = byClass(panel, "busyrow");
  if (!plate || !plate.hidden) fail("процесса за разговором нет, а плашка горит: " + dump(plate));
  status = { live: true, busy: true, sec: 5, since: RealDate.now() - 5000 };
  await runPoll();
  if (plate.hidden) {
    fail("ход начат из терминала, а панель осталась пустой: " + dump(plate));
  }
  if (!dump(plate).includes("5 с")) fail("счётчик нового хода не пошёл: " + dump(plate));
}

console.log("poc_chatbusylive: ok");

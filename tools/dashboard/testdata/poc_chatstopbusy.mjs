// Стенд плашки хода в панели чата после явного стопа (второй хвост второй
// приёмки DK-716, 2026-09-05, и замечание ревью 9 той же доработки).
//
// Живой случай первого хвоста: человек послал реплику, панель показала
// «агент работает...», нажал «Стоп», ход прервался и привязка снялась, а
// плашка осталась гореть. Опрос /status считает занятость и по хвосту
// транскрипта, где незакрытый вызов инструмента висит до получаса (busyNow,
// sessions.go), и после явного Escape плашка зависала бы на весь этот срок,
// хотя ответ самого стопа уже сказал, что ход кончен.
//
// Замечание ревью 9: чинить нужно не безусловным гашением. Стоп при живой
// фоновой работе отвечает 200 со state «останавливается», строка доски при
// этом стоит под «Стопом» (run_stopping), а старая правка гасила плашку на
// всякий ok, и панель молчала о том, что доска ещё говорит вслух.
//
// Предмет стенда: стоп без фоновой работы гасит плашку сразу, ответом самой
// ручки /stop, не дожидаясь опроса; стоп при живой фоновой работе плашку не
// гасит, а зажигает её тем же словом, что несёт строка доски, и опрос,
// оставшийся в деле, гасит её сам, когда сессия становится настоящей idle.
//
// Зовётся: node testdata/poc_chatstopbusy.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const SID = "eeee7777-0001";
const DOZHIM_WORDS = "ход прерван, но фоновые субагенты ещё работают: их ходы будут прерваны тем же стопом, " +
  "пока ты не напишешь в разговор сам";

let subBusy = false;
let statusBusy = true;
const posted = [];
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const method = init && init.method;
  if (method === "POST") posted.push(path);
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats") && path.endsWith("/stop")) {
    return subBusy
      ? { way: "escape", tmux: "chat-XR-1-1", state: "останавливается", message: DOZHIM_WORDS }
      : { way: "escape", tmux: "chat-XR-1-1", state: "стоп",
        message: "ход прерван: сессия жива и ждёт следующей реплики" };
  }
  if (path.includes("/chats") && path.endsWith("/say")) return { ok: true };
  if (path.includes("/chats") && path.endsWith("/status")) return { live: true, busy: statusBusy };
  if (path.includes("/chats")) {
    return { chats: [{ id: SID, state: "live", tmux: "chat-XR-1-1", idle: false,
      title: "живой разговор" }], models: [] };
  }
  if (path.includes("/sessions/" + SID)) return { items: [], start: true };
  if (path === "/api/harnesses") return { harnesses: [] };
  return {};
});

const board = { prefix: "XR", sections: [] };

const findTag = (node, tagName) => {
  if (!node) return null;
  if (node.tagName === tagName) return node;
  for (const kid of node.children || []) {
    const hit = findTag(kid, tagName);
    if (hit) return hit;
  }
  return null;
};

// Панель заново на каждый сценарий: busy это память самой сборки makeBusy, и
// вторая проверка на той же панели унесла бы состояние первой.
const freshPanel = async () => {
  const st = await sandbox.chatState("demo", SID, board);
  if (st.sid !== SID) fail("состояние не встало на живую сессию: " + JSON.stringify(st));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  return panel;
};

const sendTurn = async (panel) => {
  const ta = findTag(panel, "TEXTAREA");
  if (!ta) fail("поля ввода в панели нет: " + dump(panel).slice(0, 300));
  ta.value = "первая реплика хода";
  ta.handlers.keydown({ key: "Enter", preventDefault: () => {} });
  await settle();
};

// --- стоп без фоновой работы гасит плашку сразу, не дожидаясь опроса ---
{
  subBusy = false;
  const panel = await freshPanel();
  await sendTurn(panel);
  const plate = byClass(panel, "busyrow");
  if (!plate || plate.hidden) {
    fail("после отправки реплики плашка работы не встала: " + dump(panel).slice(0, 300));
  }
  const stopBtn = byClass(panel, "cstop");
  if (!stopBtn) fail("в панели нет кнопки стопа хода: " + dump(panel).slice(0, 300));
  posted.length = 0;
  stopBtn.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((p) => p.includes("/stop"))) {
    fail("нажатие стопа не позвало ручку /stop: " + JSON.stringify(posted));
  }
  if (!plate.hidden) {
    fail("плашка работы осталась гореть после стопа без фоновой работы: " + dump(plate));
  }
}

// --- стоп при живой фоновой работе не гасит уже горящую плашку, а называет
// остановку (замечание ревью 9) ---
{
  subBusy = true;
  statusBusy = true;
  const panel = await freshPanel();
  await sendTurn(panel);
  const plate = byClass(panel, "busyrow");
  if (!plate || plate.hidden) fail("плашка работы не встала перед стопом дожима");
  const stopBtn = byClass(panel, "cstop");
  posted.length = 0;
  stopBtn.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (plate.hidden) {
    fail("плашка погасла при живой фоновой работе: доска ещё стоит под «Стопом», а панель молчит");
  }
  if (!dump(plate).includes("фоновые субагенты")) {
    fail("плашка не назвала дожим словами строки доски: " + dump(plate));
  }
  // Три точки не должны спорить: слова плашки это те же слова, что понёс
  // ответ ручки (и тем же путём ушли всплывашкой sayResult).
  if (!dump(plate).includes(DOZHIM_WORDS)) {
    fail("плашка дожима не дословна с ответом ручки /stop: " + dump(plate));
  }
  // Опрос не снят стопом: молодая метка после только что отправленной
  // реплики держит его от ложного гашения (замечание 18), а сам опрос
  // остаётся в деле, а не отменён явным off().
  if (!timers.filter((t) => t.ms === 1500 && t.fn).pop()) {
    fail("опрос состояния после стопа дожима не остался в деле");
  }
}

// --- стоп при живой фоновой работе зажигает плашку, даже если её не было:
// главный ход уже кончился молча, а фоновые субагенты работают ещё, и опрос
// сам гасит плашку, когда сессия становится настоящей idle ---
{
  subBusy = true;
  statusBusy = true;
  const panel = await freshPanel();
  const plate = byClass(panel, "busyrow");
  if (!plate || !plate.hidden) fail("плашка горит до всякого хода: нечему было её поднять");
  const stopBtn = byClass(panel, "cstop");
  posted.length = 0;
  stopBtn.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (plate.hidden) {
    fail("стоп по молчавшей панели не зажёг плашку дожима: фоновая работа жива, а панель пуста");
  }
  if (!dump(plate).includes(DOZHIM_WORDS)) {
    fail("зажжённая плашка дожима не назвала слова остановки: " + dump(plate));
  }
  // Реплики не было, метка молодости пуста, и опрос волен гасить плашку по
  // первому же честному ответу «не занят». Опрос ищется свежайшим по шагу
  // 1500 мс: у прежних сценариев в общем списке таймеров стенда остались
  // свои такие же записи, отработавшие своё.
  const poll = timers.filter((t) => t.ms === 1500 && t.fn).pop();
  if (!poll) fail("опрос состояния после стопа дожима не остался в деле");
  statusBusy = false;
  poll.fn();
  poll.fn = null;
  await settle();
  if (!plate.hidden) {
    fail("опрос застал настоящую idle, а плашка дожима не погасла сама: " + dump(plate));
  }
}

console.log("poc_chatstopbusy: ok");

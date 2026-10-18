// Стенд итога ask-дороги в панели (DK-1399, замечание ревью).
//
// Живой случай: человек ответил в панели на вопрос черновика, чьё окно умерло.
// Сервер реплику не принял и назвал причину, а say-дорога читала только note
// и stuck, молчала и ставила пузырю «доставлено»: непринятый ответ выглядел
// ушедшим, и человек ждал реакции, которой не будет. Предмет стенда: отказ
// назван словами и тоном ошибки, пузырь держит причину, откладывание в файл
// задачи стоит очередью, принятый ответ доставлен как раньше.
//
// Зовётся: node testdata/poc_askrefused.mjs static/app.js

import { makeSandbox, settle, dump, tag, deepBtn, allByClass, tipOf, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const chats = [
  { id: "aaaa1111-1111", title: "Выполни XR-5", mtime: "2026-10-17T10:02:00+03:00",
    tasks: ["XR-5"], model: "opus", liveModel: "opus", own: true,
    tmux: "chat-XR-5-1", state: "live", tree: "xr-5", idle: true },
];
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-5", title: "мертвое окно вопроса", sect: "in-progress" },
] }] };

// Судьба следующего ответа ask-дороги: отказ мёртвого окна, откладывание в
// файл задачи и принятый ответ. Поля повторяют ответы сервера из chat.go,
// включая слово причины, по которому панель узнаёт свою строку.
let fate = "refused";
const answer = (body) => Promise.resolve({ ok: true, status: 200,
  text: () => Promise.resolve(JSON.stringify(body)),
  json: () => Promise.resolve(body) });

const { sandbox } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});
const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init && init.method === "POST" && path.includes("/say")) {
    if (fate === "refused") {
      return answer({ way: "ask", chat: "task-XR-5", task: "XR-5",
        refused: "у черновика нет строки доски",
        message: "ответ не принят: у черновика нет строки доски. Вопрос снят, реплика в доставку не легла" });
    }
    if (fate === "held") {
      return answer({ way: "ask", chat: "task-XR-5", task: "XR-5",
        held: true, why: "строка в Backlog без ведущей сессии",
        message: "ответ на вопрос задачи XR-5 отложен во «Входящие» файла задачи" });
    }
    return answer({ way: "ask", chat: "task-XR-5", task: "XR-5",
      until: 1790000000, message: "ответ лёг во вход разговора task-XR-5" });
  }
  return plain(path, init);
};

const st = await sandbox.chatState("demo", "aaaa1111-1111", board);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
const send = () => deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
const said = () => dump(sandbox.document.getElementById("flashes")).replace(/\s+/g, " ");
const tone = () => {
  const cards = sandbox.document.getElementById("flashes");
  return cards && cards.children.length ? String(cards.children[0].className) : "";
};
// Судить надо свежий пузырь, а не всю панель: прежние пузыри стоят в ленте со
// своими исходами и живут там до эха из транскрипта.
const lastBubble = () => {
  const own = allByClass(panel, "m-local");
  return own.length ? dump(own[own.length - 1]) : "";
};

// --- отказ мёртвого окна: причина словами и ошибкой, пузырь не врёт ---
ta.value = "механика сигнала";
send();
await settle();
{
  const words = said();
  if (!words.includes("ответ не принят")) fail("отказ не назван словами: " + words);
  if (!words.includes("Вопрос снят")) fail("исход вопроса не назван: " + words);
  if (!/\berr\b/.test(tone())) fail("отказ не покрашен ошибкой: " + tone());
  const bubble = lastBubble();
  if (!bubble.includes("не доставлено")) {
    fail("пузырь непринятого ответа выдаёт себя доставленным: " + bubble.slice(0, 400));
  }
  if (!bubble.includes("механика сигнала")) fail("текст ответа пропал из пузыря: " + bubble.slice(0, 400));
  if (!tipOf(panel).includes("у черновика нет строки доски")) {
    fail("причина отказа не стоит в подсказке пузыря: " + tipOf(panel));
  }
}

// --- откладывание в файл задачи: очередь с причиной ---
fate = "held";
ta.value = "бери схему из LLD";
send();
await settle();
{
  const words = said();
  if (!words.includes("отложен")) fail("откладывание не названо словами: " + words);
  const bubble = lastBubble();
  if (!bubble.includes("в очереди")) fail("отложенный ответ не стоит очередью: " + bubble.slice(0, 400));
  if (bubble.includes("не доставлено")) fail("отложенный ответ назван недоставленным: " + bubble.slice(0, 400));
  if (!tipOf(panel).includes("строка в Backlog без ведущей сессии")) {
    fail("причина откладывания не стоит в подсказке пузыря: " + tipOf(panel));
  }
}

// --- принятый ответ: доставлен как раньше ---
fate = "took";
ta.value = "продолжай";
send();
await settle();
{
  const words = said();
  if (!words.includes("ответ лёг во вход разговора")) fail("принятый ответ не назван словами: " + words);
  if (/\berr\b/.test(tone())) fail("принятый ответ покрашен ошибкой: " + tone());
  const bubble = lastBubble();
  if (bubble.includes("не доставлено") || !bubble.includes("доставлено")) {
    fail("принятый ответ не доставлен: " + bubble.slice(0, 400));
  }
}

console.log("poc_askrefused: ok");

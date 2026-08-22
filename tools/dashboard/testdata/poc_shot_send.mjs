// Стенд вложения в реплику (ветка poc-chat): вставленная картинка уезжает и
// тогда, когда сессии у чата ещё нет.
//
// Путь вложения был завязан на ID живой сессии: у нового разговора и у чата
// чужой задачи его нет, и картинка пропадала молча, а реплика уходила без неё
// (жалоба пользователя на чат DK-460). Предмет стенда: файл кладётся под
// свежим ключом, путь приклеивается к той самой реплике, которая поднимает
// сессию, а неудача загрузки видна словами и пузырём, а не молчанием.
//
// Зовётся: node testdata/poc_shot_send.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Что отвечает ручка вложения: путь либо пустота (сервер не назвал файл).
let shotPath = "/Users/rider/.devkit/uploads/new-1/20260822T120000.png";
const asked = [];
const bodies = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
  }
  if (path.includes("/shot")) return shotPath ? { path: shotPath, name: "снимок.png" } : {};
  if (path.endsWith("/chats")) return { tmux: "chat-DK-460-1" };
  return {};
});

// Разговор, у которого сессии ещё нет: ровно так открывается чат задачи,
// которую ведут на другой машине.
const fresh = () => ({
  addr: "DK-460", sid: "", task: "DK-460", chats: [], entry: null,
  models: [], fresh: false, error: "", note: "",
});

const paste = (ta) => {
  ta.handlers.paste({
    preventDefault: () => {},
    clipboardData: { items: [{ type: "image/png", getAsFile: () => ({ name: "снимок.png" }) }] },
  });
};

// --- вставка без сессии: файл лёг, путь уехал первой репликой ---
{
  const st = fresh();
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  if (!ta) fail("поля ввода в панели нет");
  paste(ta);
  await settle();
  ta.value = "посмотри, что на снимке";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const up = asked.find((p) => p.includes("/shot"));
  if (!up) fail("вложение не ушло на сервер вовсе: " + JSON.stringify(asked));
  if (!up.includes("/chats/new-")) {
    fail("вложение без сессии легло не под свежим ключом: " + up);
  }
  const raise = asked.findIndex((p) => p.endsWith("/chats"));
  if (raise < 0) fail("сессия не поднялась: " + JSON.stringify(asked));
  if (asked.indexOf(up) > raise) fail("картинка ушла позже реплики, которая её называет");
  const wire = bodies[raise].text || "";
  if (!wire.includes('<screenshot file="' + shotPath + '">')) {
    fail("путь картинки не приклеился к первой реплике: " + wire);
  }
  if (!wire.includes("посмотри, что на снимке")) fail("слова человека потерялись: " + wire);
}

// --- живой разговор: вложение по-прежнему ложится под свою сессию ---
{
  asked.length = 0;
  bodies.length = 0;
  const st = fresh();
  st.sid = "aaaa1111-1111";
  st.entry = { id: st.sid, state: "live", tasks: ["DK-460"], model: "opus" };
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  paste(ta);
  await settle();
  ta.value = "и вот сюда посмотри";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!asked.some((p) => p.includes("/chats/" + st.sid + "/shot"))) {
    fail("вложение живого разговора легло не под его сессию: " + JSON.stringify(asked));
  }
  const say = asked.findIndex((p) => p.endsWith("/say"));
  if (say < 0) fail("реплика живого разговора не ушла: " + JSON.stringify(asked));
  if (!String(bodies[say].text).includes("<screenshot file=")) {
    fail("путь картинки не приклеился к реплике: " + bodies[say].text);
  }
}

// --- неудача загрузки не молчит: реплика не уходит, причина сказана ---
{
  shotPath = "";
  asked.length = 0;
  bodies.length = 0;
  const st = fresh();
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  paste(ta);
  await settle();
  ta.value = "вот снимок";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (asked.some((p) => p.endsWith("/chats"))) {
    fail("реплика ушла без картинки, о которой человек говорит: " + JSON.stringify(asked));
  }
  const said = dump(sandbox.document.getElementById("flashes"));
  if (!said.includes("картинка не ушла")) {
    fail("неудача вложения смолчала: " + said);
  }
  const pend = byClass(panel, "mlocal");
  if (!dump(pend).includes("вот снимок")) {
    fail("пузырь неушедшей реплики пропал вместе с набранным: " + dump(pend));
  }
}

console.log("poc_shot_send: ok");

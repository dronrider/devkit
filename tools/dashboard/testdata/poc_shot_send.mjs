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
// Внешний вход рубит длинное тело своим 413 и пишет об этом страницей html:
// разбор такой страницы падал SyntaxError, и человек читал жалобу движка js
// вместо причины (жалоба пользователя).
let outer413 = false;
const asked = [];
const bodies = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
  }
  if (path.includes("/shot")) {
    if (outer413) {
      return { raw: { status: 413, statusText: "Request Entity Too Large",
        text: "<html><head><title>413 Request Entity Too Large</title></head>" +
          "<body><center><h1>413 Request Entity Too Large</h1></center>" +
          "<hr><center>nginx</center></body></html>" } };
    }
    return shotPath ? { path: shotPath, name: "снимок.png" } : {};
  }
  if (path.endsWith("/chats")) return { tmux: "chat-DK-460-1" };
  return {};
});

// Разговор, у которого сессии ещё нет: ровно так открывается чат задачи,
// которую ведут на другой машине.
const fresh = () => ({
  addr: "DK-460", sid: "", task: "DK-460", chats: [], entry: null,
  models: [], fresh: false, error: "", note: "",
});

// Вставка картинки из буфера. Размеры и длина приезжают в самом dataURL:
// мок FileReader отдаёт то, что положил стенд, а мок Image читает из него
// «#<ширина>x<высота>».
const paste = (ta, dataURL) => {
  ta.handlers.paste({
    preventDefault: () => {},
    clipboardData: { items: [{ type: "image/png",
      getAsFile: () => ({ name: "снимок.png", dataURL: dataURL || "" }) }] },
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

// --- большой снимок ужимается до предела входа ---
// Ретина-снимок уезжал во внешний вход как есть и возвращался оттуда 413:
// длинное тело фронт рубит раньше дашборда (жалоба пользователя).
{
  shotPath = "/Users/rider/.devkit/uploads/new-2/20260822T150000.jpg";
  asked.length = 0;
  bodies.length = 0;
  const big = "data:image/png;base64,#3000x2000" + "A".repeat(6 * 1024 * 1024);
  const st = fresh();
  st.sid = "aaaa1111-1111";
  st.entry = { id: st.sid, state: "live", tasks: ["DK-460"], model: "opus" };
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  paste(ta, big);
  await settle();
  ta.value = "вот весь экран";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const up = bodies[asked.findIndex((p) => p.includes("/shot"))];
  if (!up) fail("вложение не ушло вовсе: " + JSON.stringify(asked));
  if (up.kind !== "image/jpeg") fail("снимок уехал не перекодированным: " + up.kind);
  // Предел тот же, что и в самой статике (SHOT_BYTES): держать его тут
  // числом честнее, чем читать из песочницы, где верхний const не виден.
  const LIMIT = 900 * 1024;
  const bytes = Math.floor(up.data.length * 3 / 4);
  if (bytes > LIMIT) fail("снимок не ужался до предела входа: " + bytes + " байт");
  // Сжатие идёт шагами, а не одним качеством: первый шаг длинного снимка в
  // предел не влезает, и стенд держит, что дальше него ходка не встала.
  if (bytes <= 0) fail("после сжатия не осталось данных");
}

// --- мелкая картинка едет как есть, png не портится ---
{
  asked.length = 0;
  bodies.length = 0;
  const small = "data:image/png;base64,#64x64" + "A".repeat(4 * 1024);
  const st = fresh();
  st.sid = "aaaa1111-1111";
  st.entry = { id: st.sid, state: "live", tasks: ["DK-460"], model: "opus" };
  const panel = sandbox.chatPanel("demo", st);
  paste(tag(panel, "TEXTAREA"), small);
  await settle();
  tag(panel, "TEXTAREA").value = "значок";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const up = bodies[asked.findIndex((p) => p.includes("/shot"))];
  if (!up || up.kind !== "image/png") fail("мелкая картинка перекодирована зря: " + (up && up.kind));
}

// --- 413 от внешнего входа доходит до человека словами ---
{
  outer413 = true;
  asked.length = 0;
  const st = fresh();
  st.sid = "aaaa1111-1111";
  st.entry = { id: st.sid, state: "live", tasks: ["DK-460"], model: "opus" };
  const panel = sandbox.chatPanel("demo", st);
  paste(tag(panel, "TEXTAREA"), "data:image/png;base64,#3000x2000" + "A".repeat(6 * 1024 * 1024));
  await settle();
  tag(panel, "TEXTAREA").value = "вот экран";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = dump(sandbox.document.getElementById("flashes"));
  if (!said.includes("слишком большой")) fail("413 не объяснён человеку: " + said);
  if (said.includes("SyntaxError") || said.includes("JSON")) {
    fail("вместо причины показана жалоба разбора: " + said);
  }
  if (asked.some((p) => p.endsWith("/say"))) {
    fail("реплика ушла без картинки, которую 413 не пропустил: " + JSON.stringify(asked));
  }
  outer413 = false;
}

// --- отказ внешнего входа читается словами, а не разбором html ---
{
  const said = sandbox.outerFail(413, "Request Entity Too Large",
    "<html><head><title>413 Request Entity Too Large</title></head><body>nginx</body></html>");
  if (!said.includes("слишком большой") || !said.includes("413")) {
    fail("413 не объяснён человеку: " + said);
  }
  if (said.includes("<")) fail("в слова про отказ уехала разметка: " + said);
  const other = sandbox.outerFail(502, "Bad Gateway", "<html>502</html>");
  if (!other.includes("502") || other.includes("<")) fail("502 не объяснён: " + other);
}

console.log("poc_shot_send: ok");

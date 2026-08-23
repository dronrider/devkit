// Стенд клина в панели разговора (ветка poc-chat).
//
// Клин это разговор, у которого все признаки живого: сокет берёт реплики и
// отвечает удачей, состояние live, а хода нет и не будет, потому что клиент
// стоит на записи в исчезнувший терминал. Человек видел вечное «работает»
// (инцидент с чатом DK-460). Предмет стенда: плашка называет клин словами,
// кнопка выхода делает два шага в правильном порядке (сначала снятие процесса,
// потом резюм) и не идёт дальше, если процесс снять не вышло.
//
// Зовётся: node testdata/poc_wedge.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
let killOK = true;
let sayDeaf = false;
const asked = [];
const bodies = [];
const got = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
    if (path.endsWith("/stop") && !killOK) {
      return { raw: { status: 409, statusText: "Conflict",
        text: JSON.stringify({ error: "процесса чата в реестре клиента нет: снимать нечего" }) } };
    }
    if (path.endsWith("/stop")) return { way: "kill", pid: 19289, message: "зависший процесс снят" };
    if (path.endsWith("/say") && sayDeaf) {
      return { raw: { status: 502, statusText: "Bad Gateway",
        text: JSON.stringify({ error: "клиент принял байты, но не подтвердил доставку: " +
          "событийный цикл стоит, реплика не доставлена", stuck: "канал молчит" }) } };
    }
    if (path.endsWith("/say")) return { way: "resume", tmux: "chat-DK-460-3" };
  } else {
    got.push(path);
    // Перечитывание панели идёт полной перерисовкой экрана, и дорога к ней
    // лежит через список проектов: без него маршрут не находит demo.
    if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  }
  return {};
});

// Разговор в клине: сессия жива, сокет есть, а сервер назвал клин своим полем.
const wedged = () => ({
  addr: "DK-460", sid: "8e9c1cf9-1111", task: "DK-460", chats: [], models: [],
  fresh: false, error: "", note: "",
  entry: { id: "8e9c1cf9-1111", state: "live", tasks: ["DK-460"], model: "opus",
    sock: "/tmp/cc-socks/19289.sock", pid: 19289, stuck: "терминал пропал" },
});

// --- вопрос в терминале: плашка зовёт attach и не предлагает снятие ---
// Третий род стоящего чата: клиент спросил разрешение или доверие каталогу в
// своём окне (первый запуск чужого профиля, живой случай chat-13). Это не
// клин, процесс снимать нельзя, и плашка называет tmux-сессию дословно.
{
  const asks = () => ({
    addr: "DK-460", sid: "8e9c1cf9-1111", task: "DK-460", chats: [], models: [],
    fresh: false, error: "", note: "",
    entry: { id: "8e9c1cf9-1111", state: "live", tasks: ["DK-460"], model: "opus",
      sock: "/tmp/cc-socks/19289.sock", pid: 19289, tmux: "chat-13",
      stuck: "ждёт ответа в терминале" },
  });
  const panel = sandbox.chatPanel("demo", asks());
  const note = byClass(panel, "stuckn");
  if (!note) fail("плашки вопроса в терминале нет: " + dump(panel).slice(0, 200));
  const said = dump(note);
  if (!said.includes("tmux attach -t chat-13")) {
    fail("плашка не назвала tmux-сессию для attach: " + said);
  }
  if (said.includes("завис")) fail("вопрос в терминале назван клином: " + said);
  if (deepBtn(note, "Продолжить")) {
    fail("плашка вопроса предлагает снять процесс, а вопрос стоит человеку");
  }
}

// --- плашка называет клин и несёт кнопку выхода ---
{
  const panel = sandbox.chatPanel("demo", wedged());
  const note = byClass(panel, "stuckn");
  if (!note) fail("плашки клина в панели нет: " + dump(panel).slice(0, 200));
  const said = dump(note);
  if (!said.includes("завис") || !said.includes("терминал пропал")) {
    fail("плашка не назвала клин словами: " + said);
  }
  if (!deepBtn(note, "Продолжить")) fail("на плашке нет кнопки выхода: " + said);
}

// --- здоровый разговор плашки не носит ---
{
  const ok = wedged();
  ok.entry.stuck = "";
  if (byClass(sandbox.chatPanel("demo", ok), "stuckn")) {
    fail("плашка клина вылезла у здорового разговора");
  }
}

// --- кнопка снимает процесс, потом поднимает резюм ---
{
  asked.length = 0;
  bodies.length = 0;
  const panel = sandbox.chatPanel("demo", wedged());
  deepBtn(byClass(panel, "stuckn"), "Продолжить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const stop = asked.findIndex((p) => p.endsWith("/stop"));
  const say = asked.findIndex((p) => p.endsWith("/say"));
  if (stop < 0) fail("зависший процесс не снимали: " + JSON.stringify(asked));
  if (say < 0) fail("после снятия не подняли разговор: " + JSON.stringify(asked));
  if (stop > say) fail("резюм пошёл раньше снятия: поверх клина встал бы второй агент");
  if (!bodies[stop] || bodies[stop].kill !== true) {
    fail("стоп пошёл обычным, а мёртвому терминалу Escape подать некуда: " + JSON.stringify(bodies[stop]));
  }
  if (!String(bodies[say].text).includes("Продолжай")) {
    fail("вводная резюма не просит продолжить: " + bodies[say].text);
  }
}

// --- процесс не снялся: резюм не идёт, причина сказана словами ---
{
  killOK = false;
  asked.length = 0;
  const panel = sandbox.chatPanel("demo", wedged());
  deepBtn(byClass(panel, "stuckn"), "Продолжить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (asked.some((p) => p.endsWith("/say"))) {
    fail("резюм пошёл поверх неснятого процесса: " + JSON.stringify(asked));
  }
  const said = dump(sandbox.document.getElementById("flashes"));
  if (!said.includes("снимать нечего")) fail("отказ снятия смолчал: " + said);
  killOK = true;
}

// --- отказ доставки с именем клина: пузырь недоставлен, панель перечитана ---
// Реплика без подтверждения не помечается доставленной: ручка ответила отказом,
// пузырь остаётся «не ушло», а панель перечитывает список сразу, потому что
// сервер уже запомнил молчащий канал и плашка клина обязана встать здесь.
{
  sandbox.location.hash = "#demo/chat/8e9c1cf9-2222";
  sayDeaf = true;
  asked.length = 0;
  got.length = 0;
  const st = wedged();
  st.addr = "8e9c1cf9-2222";
  st.sid = "8e9c1cf9-2222";
  st.task = "";
  st.entry = { id: "8e9c1cf9-2222", state: "live", tasks: [], model: "opus", stuck: "" };
  const panel = sandbox.chatPanel("demo", st);
  const ta = (function find(node) {
    if (node.tagName === "TEXTAREA") return node;
    for (const kid of node.children || []) {
      const hit = typeof kid === "object" && find(kid);
      if (hit) return hit;
    }
    return null;
  })(panel);
  if (!ta) fail("поля ввода нет");
  ta.value = "ау";
  ta.handlers.keydown({ key: "Enter", preventDefault: () => {} });
  await settle(400);
  if (!got.some((p) => p.includes("/chats"))) {
    fail("отказ с именем клина не перечитал панель: " + JSON.stringify(got));
  }
  // Перечитанная панель собирается в cpin, а недоставленная реплика переживает
  // пересборку персистом эха: пузырь стоит с пометкой, доставленной её никто
  // не назвал.
  const said = dump(sandbox.document.getElementById("cpin")).replace(/\s+/g, " ");
  if (said.includes("доставлено")) fail("реплика без подтверждения помечена доставленной: " + said);
  if (!said.includes("не ушло") || !said.includes("ау")) {
    fail("пузырь недоставленной реплики пропал с перечитанной панели: " + said.slice(0, 300));
  }
  sayDeaf = false;
}

// --- второй род клина: живой pty, канал молчит, та же плашка ---
// Живой случай клиента 69975: терминал на месте, приглашение рисуется, а
// событийный цикл мёртв. Сервер зовёт его своим словом, плашка и кнопка одни
// на оба рода.
{
  const st = wedged();
  st.entry.stuck = "канал молчит";
  const panel = sandbox.chatPanel("demo", st);
  const note = byClass(panel, "stuckn");
  if (!note || !dump(note).includes("канал молчит")) {
    fail("плашка второго рода клина не встала: " + dump(panel).slice(0, 200));
  }
  if (!deepBtn(note, "Продолжить")) fail("у второго рода клина нет кнопки выхода");
}

console.log("poc_wedge: ok");

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
const asked = [];
const bodies = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
    if (path.endsWith("/stop") && !killOK) {
      return { raw: { status: 409, statusText: "Conflict",
        text: JSON.stringify({ error: "процесса чата в реестре клиента нет: снимать нечего" }) } };
    }
    if (path.endsWith("/stop")) return { way: "kill", pid: 19289, message: "зависший процесс снят" };
    if (path.endsWith("/say")) return { way: "resume", tmux: "chat-DK-460-3" };
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

console.log("poc_wedge: ok");

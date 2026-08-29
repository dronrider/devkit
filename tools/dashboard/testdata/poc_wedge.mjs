// Стенд клина в панели разговора (ветка poc-chat).
//
// Клин это разговор, у которого все признаки живого: сокет берёт реплики и
// отвечает удачей, состояние live, а хода нет и не будет. Клиент либо пишет в
// исчезнувший терминал (инцидент с чатом DK-460), либо стоит с мёртвым
// событийным циклом при живом pty (клиент 69975).
//
// Прежде тут висела плашка «Чат завис» с кнопкой «Продолжить». Плашкам в чате
// не место, чат должен просто работать (решение пользователя): при твёрдом
// признаке клина решение одно и очевидно, и спрашивать человека незачем.
//
// Предмет стенда: твёрдый клин лечится сам двумя шагами в правильном порядке,
// кнопки и плашки нет вовсе, лечение отчитывается серверу (тот кладёт строку в
// ленту), повторный клин подряд второй раз не лечится, а при сомнении не
// делается ничего.
//
// Зовётся: node testdata/poc_wedge.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
let killOK = true;
let sayDeaf = false;
let claimOK = true;
const asked = [];
const bodies = [];
const got = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
    if (path.endsWith("/heal")) {
      const body = init.body ? JSON.parse(init.body) : {};
      if (body && body.done !== undefined) return { session: "s", message: "отчёт принят" };
      return claimOK ? { session: "s", claim: true }
        : { session: "s", claim: false, why: "разговор завис снова сразу после перезапуска" };
    }
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

// Разговор в твёрдом клине: сессия жива, сокет есть, а сервер назвал клин своим
// полем и признал признак твёрдым (heal).
const wedged = (sid) => ({
  addr: sid, sid, task: "DK-460", chats: [], models: [],
  fresh: false, error: "", note: "",
  entry: { id: sid, state: "live", tasks: ["DK-460"], model: "opus",
    sock: "/tmp/cc-socks/19289.sock", pid: 19289, stuck: "терминал пропал", heal: true },
});

const clear = () => { asked.length = 0; bodies.length = 0; got.length = 0; };
const stepOf = (what) => asked.findIndex((p) => p.endsWith(what));
const healBodies = () => bodies.filter((b, i) => asked[i].endsWith("/heal"));

// --- ни плашки, ни кнопки: чат просто работает ---
{
  clear();
  const panel = sandbox.chatPanel("demo", wedged("8e9c1cf9-1111"));
  await settle();
  if (byClass(panel, "stuckn")) fail("плашка клина вернулась: " + dump(byClass(panel, "stuckn")));
  if (deepBtn(panel, "Продолжить")) fail("кнопка выхода из клина вернулась в панель");
  const said = dump(panel);
  for (const word of ["Чат завис", "Нажмите продолжить"]) {
    if (said.includes(word)) fail("слова плашки остались в панели: " + word);
  }
}

// --- твёрдый клин лечится сам: заявка, снятие, резюм, отчёт ---
{
  clear();
  sandbox.chatPanel("demo", wedged("8e9c1cf9-2222"));
  await settle();
  const claim = stepOf("/heal");
  const stop = stepOf("/stop");
  const say = stepOf("/say");
  if (claim < 0) fail("лечение пошло без заявки серверу: " + JSON.stringify(asked));
  if (stop < 0) fail("зависший процесс не снимали: " + JSON.stringify(asked));
  if (say < 0) fail("после снятия не подняли разговор: " + JSON.stringify(asked));
  if (claim > stop) fail("процесс сняли раньше заявки: снятие необратимо");
  if (stop > say) fail("резюм пошёл раньше снятия: поверх клина встал бы второй агент");
  if (!bodies[stop] || bodies[stop].kill !== true) {
    fail("стоп пошёл обычным, а мёртвому терминалу Escape подать некуда: " +
      JSON.stringify(bodies[stop]));
  }
  if (!String(bodies[say].text).includes("Продолжай")) {
    fail("вводная резюма не просит продолжить: " + bodies[say].text);
  }
  // Отчёт об удаче: строку в ленту кладёт сервер, панель её не сочиняет.
  const done = healBodies().filter((b) => b && b.done !== undefined);
  if (done.length !== 1 || done[0].done !== true) {
    fail("лечение не отчиталось удачей: " + JSON.stringify(healBodies()));
  }
}

// --- пока идёт лечение, состояние честное и одно ---
{
  clear();
  const panel = sandbox.chatPanel("demo", wedged("8e9c1cf9-3333"));
  // Один оборот очереди: заявка ушла, ответ разобран, дальше идёт снятие.
  await settle(0);
  const row = byClass(panel, "busyrow");
  if (row && !row.hidden && dump(row).includes("агент работает")) {
    fail("рядом с лечением мигает «агент работает», хотя ход стоит: " + dump(row));
  }
  // Слова тут про дело человека, а не про наше устройство: «разговор
  // перезапускается» это снятие процесса и подъём резюмом, и человеку в этом
  // нет ничего («совершенно непонятно, зачем это видеть пользователю и что это
  // значит»). Строка обязана сказать, кто пропал, надолго ли и цело ли
  // сказанное.
  const said = row ? dump(row) : "";
  if (said.includes("перезапуска") || said.includes("сесси")) {
    fail("на экран вылезла наша механика вместо дела человека: " + said);
  }
  for (const word of ["агент", "секунд", "не потеряно"]) {
    if (!said.includes(word)) {
      fail("строка лечения не говорит «" + word + "»: " + said);
    }
  }
}

// --- сервер отказал: панель молчит и ничего не трогает ---
{
  claimOK = false;
  clear();
  sandbox.chatPanel("demo", wedged("8e9c1cf9-4444"));
  await settle();
  if (stepOf("/stop") >= 0) {
    fail("процесс сняли вопреки отказу сервера: " + JSON.stringify(asked));
  }
  if (stepOf("/say") >= 0) fail("резюм пошёл вопреки отказу сервера");
  const flash = dump(sandbox.document.getElementById("flashes"));
  if (flash.includes("завис")) fail("отказ вылез тревогой поверх ленты: " + flash);
  claimOK = true;
}

// --- сомнения нет признака: не делается ничего ---
{
  clear();
  const doubt = wedged("8e9c1cf9-5555");
  doubt.entry.stuck = "ждёт ответа в терминале";
  doubt.entry.heal = false;
  sandbox.chatPanel("demo", doubt);
  await settle();
  if (asked.length) fail("при сомнении панель полезла лечить: " + JSON.stringify(asked));
}

// --- здоровый разговор не трогается вовсе ---
{
  clear();
  const ok = wedged("8e9c1cf9-6666");
  ok.entry.stuck = "";
  ok.entry.heal = false;
  sandbox.chatPanel("demo", ok);
  await settle();
  if (asked.length) fail("здоровый разговор полезли лечить: " + JSON.stringify(asked));
}

// --- процесс не снялся: резюм не идёт, отчёт уходит провалом ---
{
  killOK = false;
  clear();
  sandbox.chatPanel("demo", wedged("8e9c1cf9-7777"));
  await settle();
  if (stepOf("/say") >= 0) {
    fail("резюм пошёл поверх неснятого процесса: " + JSON.stringify(asked));
  }
  const done = healBodies().filter((b) => b && b.done !== undefined);
  if (done.length !== 1 || done[0].done !== false) {
    fail("провал лечения не отчитался серверу: " + JSON.stringify(healBodies()));
  }
  killOK = true;
}

// --- отказ доставки с именем клина: пузырь недоставлен, панель перечитана ---
// Реплика без подтверждения не помечается доставленной: ручка ответила отказом,
// пузырь остаётся «не ушло», а панель перечитывает список сразу, потому что
// сервер уже запомнил молчащий канал.
{
  sandbox.location.hash = "#demo/chat/8e9c1cf9-8888";
  sayDeaf = true;
  clear();
  const st = wedged("8e9c1cf9-8888");
  st.task = "";
  st.entry = { id: "8e9c1cf9-8888", state: "live", tasks: [], model: "opus", stuck: "" };
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
  const said = dump(sandbox.document.getElementById("cpin")).replace(/\s+/g, " ");
  if (said.includes("доставлено")) fail("реплика без подтверждения помечена доставленной: " + said);
  if (!said.includes("не ушло") || !said.includes("ау")) {
    fail("пузырь недоставленной реплики пропал с перечитанной панели: " + said.slice(0, 300));
  }
  sayDeaf = false;
}

console.log("poc_wedge: твёрдый клин лечится сам без плашки и кнопки, отказ и сомнение " +
  "не трогают ничего");

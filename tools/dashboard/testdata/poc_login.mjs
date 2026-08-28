// Стенд разлогиненного разговора (ветка poc-chat, DK-466).
//
// Живой случай: у клиента истёк OAuth-токен, и на каждую реплику сессия
// отвечала «Login expired. Please run /login». Дашборд знал про такой ответ
// только одно: не пускал его в заголовок разговора. На экране разговор
// выглядел живым, отказ стоял обычным пузырём ленты, а пути к починке не было
// вовсе, и с телефона его нет тем более.
//
// Предмет стенда: состояние стоит плашкой отдельно от ленты, слова говорят
// порядок починки (сперва /login на машине, потом перезапуск), кнопка
// перезапускает разговор двумя шагами в правильном порядке, живой ответ агента
// гасит плашку сам, а разговор из чужого окна кнопки не получает вовсе.
//
// Зовётся: node testdata/poc_login.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const BYE = "Login expired. Please run /login";

let items = [];
const asked = [];
const bodies = [];
let dropOK = true;

const { sandbox, streams } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
    if (path.endsWith("/stop")) {
      if (!dropOK) {
        return { raw: { status: 409, statusText: "Conflict",
          text: JSON.stringify({ error: "чат не в нашей tmux: снимать отсюда нечего" }) } };
      }
      return { way: "drop", tmux: "chat-DK-397-1", message: "сессия снята" };
    }
    if (path.endsWith("/say")) return { way: "resume", tmux: "chat-DK-397-3" };
    return {};
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

// Разговор, чей последний ответ это служебная строка про истёкший вход. Признак
// считает сервер (поле login), панель английских слов не разбирает.
const out = (sid, extra) => ({
  addr: sid, sid, task: "DK-397", chats: [], models: [], project: "demo",
  fresh: false, error: "", note: "",
  entry: Object.assign({ id: sid, state: "live", tasks: ["DK-397"], model: "opus",
    tmux: "chat-DK-397-1", own: true, login: "сессия разлогинена" }, extra || {}),
});

const clear = () => { asked.length = 0; bodies.length = 0; };
const stepOf = (what) => asked.findIndex((p) => p.endsWith(what));

// --- состояние видно плашкой, и слова говорят, что делать ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-1111"));
  await settle();
  const plate = byClass(panel, "cbye");
  if (!plate || plate.hidden) fail("плашки разлогина нет: " + dump(panel));
  const said = dump(plate);
  for (const word of ["разлогинена", "/login", "терминал", "Перезапустить"]) {
    if (!said.includes(word)) fail("на плашке нет слова «" + word + "»: " + said);
  }
  // Состояние стоит отдельно от ленты: плашка это не запись разговора.
  const feed = byClass(panel, "chatfeed");
  if (feed && dump(feed).includes("разлогинена")) {
    fail("состояние уехало в ленту, а ему место рядом с полем ввода: " + dump(feed));
  }
}

// --- кнопка чинит: снятие сессии, потом резюм, и в таком порядке ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-2222"));
  await settle();
  const go = deepBtn(panel, "Перезапустить");
  if (!go) fail("кнопки перезапуска нет: " + dump(panel));
  go.handlers.click({ stopPropagation: () => {} });
  await settle();
  const drop = stepOf("/stop");
  const say = stepOf("/say");
  if (drop < 0) fail("сессию не снимали: " + JSON.stringify(asked));
  if (say < 0) fail("после снятия разговор не подняли: " + JSON.stringify(asked));
  if (drop > say) fail("резюм пошёл раньше снятия: поверх живого клиента встал бы второй агент");
  if (!bodies[drop] || bodies[drop].drop !== true) {
    fail("снятие пошло не под перезапуск: " + JSON.stringify(bodies[drop]));
  }
  if (!String(bodies[say].text).includes("Продолжай")) {
    fail("вводная резюма не просит продолжить: " + bodies[say].text);
  }
}

// --- снять не вышло: резюм не идёт, причина сказана словами ---
{
  dropOK = false;
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-3333"));
  await settle();
  deepBtn(panel, "Перезапустить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (stepOf("/say") >= 0) fail("резюм пошёл поверх неснятой сессии: " + JSON.stringify(asked));
  const flash = dump(sandbox.document.getElementById("flashes"));
  if (!flash.includes("снимать")) fail("отказ снятия смолчал: " + flash);
  dropOK = true;
}

// --- плашка гаснет сама, когда сессия снова отвечает ---
{
  clear();
  const sid = "aaaa4660-4444";
  items = [{ key: "m-1", role: "user", text: "продолжай", time: "2026-08-22T19:00:00+03:00" },
    { key: "m-2", role: "assistant", text: BYE, logout: true, time: "2026-08-22T19:00:02+03:00" }];
  const panel = sandbox.chatPanel("demo", out(sid));
  await settle();
  const plate = byClass(panel, "cbye");
  if (plate.hidden) fail("лента с отказом входа плашку не подняла: " + dump(panel));
  const es = streams.find((s) => String(s.url).includes(sid) && String(s.url).includes("stream"));
  if (!es) fail("поток ленты не открыт");
  // Реплика человека состояния не трогает: разлогинен тут клиент, а не человек.
  es.onmessage({ data: JSON.stringify({ key: "m-3", role: "user", text: "ты тут?",
    time: "2026-08-22T19:05:00+03:00" }) });
  await settle();
  if (byClass(panel, "cbye").hidden) fail("реплика человека погасила плашку разлогина");
  // Настоящий ответ агента гасит: сессия отвечает, состояния больше нет.
  es.onmessage({ data: JSON.stringify({ key: "m-4", role: "assistant",
    text: "продолжаю с того места, где остановился", time: "2026-08-22T19:06:00+03:00" }) });
  await settle();
  if (!byClass(panel, "cbye").hidden) {
    fail("плашка не погасла после живого ответа: " + dump(byClass(panel, "cbye")));
  }
  items = [];
}

// --- здоровый разговор плашки не видит вовсе ---
{
  clear();
  const st = out("aaaa4660-5555");
  st.entry.login = "";
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const plate = byClass(panel, "cbye");
  if (plate && !plate.hidden) fail("плашка разлогина встала на здоровом разговоре: " + dump(plate));
}

// --- чужое окно: снимать отсюда нечего, и кнопка этого не обещает ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-6666", { tmux: "", own: false }));
  await settle();
  const plate = byClass(panel, "cbye");
  if (!plate || plate.hidden) fail("плашка разлогина пропала у разговора чужого окна");
  if (deepBtn(panel, "Перезапустить")) {
    fail("кнопка обещает перезапуск разговора, который дашборд не поднимал");
  }
  if (!dump(plate).includes("не дашбордом")) {
    fail("человеку не сказано, почему перезапуска тут нет: " + dump(plate));
  }
}

console.log("ок: разлогин виден плашкой отдельно от ленты, слова говорят порядок починки, " +
  "кнопка снимает сессию и поднимает резюм, живой ответ гасит состояние сам");

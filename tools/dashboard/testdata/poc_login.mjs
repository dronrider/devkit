// Стенд разлогиненного разговора (ветка poc-chat, DK-466).
//
// Живой случай: у клиента истёк OAuth-токен, и на каждую реплику сессия
// отвечала «Login expired. Please run /login». Дашборд знал про такой ответ
// только одно: не пускал его в заголовок разговора. На экране разговор
// выглядел живым, отказ стоял обычным пузырём ленты, а пути к починке не было
// вовсе, и с телефона его нет тем более.
//
// Предмет стенда: состояние говорится репликой в самом чате, а не плашкой над
// полем ввода (решение сменил пользователь на приёмке: «нужно сделать, чтобы
// весь процесс был в чате, а не какими-то плашками отдельными»). Реплика несёт
// две кнопки, кнопка перезапускает разговор двумя шагами в правильном порядке,
// перезапуск повторяет прерванный запрос, живой ответ агента гасит блок сам, а
// разговор из чужого окна кнопок не получает вовсе.
// Сам вход с телефона разобран отдельным стендом poc_loginlink.mjs.
//
// Зовётся: node testdata/poc_login.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

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

// --- состояние видно репликой в чате, и слова говорят, что делать ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-1111"));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("разлогин в чате не сказан: " + dump(panel));
  const said = dump(plate);
  for (const word of ["аутентификация", "Войти", "Перезапустить"]) {
    if (!said.includes(word)) fail("в реплике нет слова «" + word + "»: " + said);
  }
  // Говорится это репликой разговора, а не щитком при нём: тот же узел
  // сообщений, что у своих реплик, и тот же пузырь.
  if (!String(plate.className).includes("msgs")) {
    fail("блок разлогина стоит не среди реплик: " + plate.className);
  }
  if (!byClass(plate, "bb")) fail("слова стоят не пузырём реплики: " + said);
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
  const plate = byClass(panel, "cbyetalk");
  if (plate.hidden) fail("лента с отказом входа блок не подняла: " + dump(panel));
  const es = streams.find((s) => String(s.url).includes(sid) && String(s.url).includes("stream"));
  if (!es) fail("поток ленты не открыт");
  // Реплика человека состояния не трогает: разлогинен тут клиент, а не человек.
  es.onmessage({ data: JSON.stringify({ key: "m-3", role: "user", text: "ты тут?",
    time: "2026-08-22T19:05:00+03:00" }) });
  await settle();
  if (byClass(panel, "cbyetalk").hidden) fail("реплика человека погасила разлогин");
  // Настоящий ответ агента гасит: сессия отвечает, состояния больше нет.
  es.onmessage({ data: JSON.stringify({ key: "m-4", role: "assistant",
    text: "продолжаю с того места, где остановился", time: "2026-08-22T19:06:00+03:00" }) });
  await settle();
  if (!byClass(panel, "cbyetalk").hidden) {
    fail("плашка не погасла после живого ответа: " + dump(byClass(panel, "cbyetalk")));
  }
  // Вход истекает и во второй раз, тем же разговором: состояние не одноразовое,
  // и после починки плашка обязана встать заново, а не остаться погашенной.
  es.onmessage({ data: JSON.stringify({ key: "m-5", role: "assistant", text: BYE,
    logout: true, time: "2026-08-22T19:40:00+03:00" }) });
  await settle();
  if (byClass(panel, "cbyetalk").hidden) {
    fail("второй разлогин того же разговора плашку не поднял");
  }
  es.onmessage({ data: JSON.stringify({ key: "m-6", role: "assistant",
    text: "снова на связи", time: "2026-08-22T19:45:00+03:00" }) });
  await settle();
  if (!byClass(panel, "cbyetalk").hidden) fail("плашка не погасла после второй починки");
  items = [];
}

// --- здоровый разговор плашки не видит вовсе ---
{
  clear();
  const st = out("aaaa4660-5555");
  st.entry.login = "";
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (plate && !plate.hidden) fail("плашка разлогина встала на здоровом разговоре: " + dump(plate));
}

// --- чужое окно: снимать отсюда нечего, и кнопка этого не обещает ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa4660-6666", { tmux: "", own: false }));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("плашка разлогина пропала у разговора чужого окна");
  if (deepBtn(panel, "Перезапустить")) {
    fail("кнопка обещает перезапуск разговора, который дашборд не поднимал");
  }
  if (!dump(plate).includes("не дашбордом")) {
    fail("человеку не сказано, почему перезапуска тут нет: " + dump(plate));
  }
}

// --- погашенная плашка правда уходит с экрана ---
//
// hidden это всего лишь атрибут: прячет узел встроенное правило браузера
// [hidden]{display:none}, и любое авторское правило с display его перебивает.
// У плашки разговора display свой, она раскладывается флексом, и без парного
// правила [hidden] погашенная кодом плашка остаётся на экране. Ровно это и
// увидел человек: разговор ожил после /login и перезапуска, лента гасила
// состояние как надо, а «Сессия разлогинена» висела над рабочим разговором.
// Свойство узла тут проверять нечем, стенд читает настоящий style.css.
{
  const css = readFileSync(join(dirname(app), "style.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const rules = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    rules.push({ sel: m[1].replace(/^[\s}]+/, "").trim(), decl: m[2] });
  }
  // Правило про сам этот узел, а не про его соседей и потомков: селектор это
  // цепочка классов, все из которых есть у узла, и ничего сверх них.
  const own = (sel, classes, withHidden) => sel.split(",").some((part) => {
    const m = /^((?:\.[A-Za-z0-9_-]+)+)(\[hidden\])?$/.exec(part.trim());
    if (!m || Boolean(m[2]) !== withHidden) return false;
    return m[1].split(".").filter(Boolean).every((c) => classes.includes(c));
  });
  const panel = sandbox.chatPanel("demo", out("aaaa4660-7777"));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  const classes = String(plate.className).split(" ").filter(Boolean);
  const shows = rules.filter((r) => own(r.sel, classes, false) && /display\s*:/.test(r.decl));
  const hides = rules.filter((r) => own(r.sel, classes, true) && /display\s*:\s*none/.test(r.decl));
  if (shows.length && !hides.length) {
    fail("display плашке даёт правило " + shows.map((r) => r.sel).join(", ") +
      ", а парного [hidden]{display:none} в style.css нет: погашенная кодом " +
      "плашка останется на экране");
  }
}

// --- перезапуск доделывает прерванный запрос, а не начинает с чистого листа ---
{
  clear();
  const sid = "aaaa4660-8888";
  items = [
    { key: "m-1", role: "user", text: "посчитай остаток бюджета цели",
      time: "2026-08-22T19:00:00+03:00" },
    { key: "m-2", role: "assistant", text: BYE, logout: true,
      time: "2026-08-22T19:00:02+03:00" },
  ];
  const panel = sandbox.chatPanel("demo", out(sid));
  await settle();
  deepBtn(panel, "Перезапустить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const say = stepOf("/say");
  if (say < 0) fail("разговор не подняли: " + JSON.stringify(asked));
  if (String(bodies[say].text) !== "посчитай остаток бюджета цели") {
    fail("подъём не повторил прерванный запрос: " + bodies[say].text);
  }
  items = [];
}

console.log("ок: разлогин сказан репликой в чате, слова говорят порядок починки, " +
  "кнопка снимает сессию и поднимает резюм прерванным запросом, живой ответ " +
  "гасит состояние сам");

// Стенд входа в клиента с телефона (ветка poc-chat, DK-577).
//
// Живой случай 2026-08-29: у клиента вход истёк посреди работы, разговор встал,
// а починка требовала терминала на машине. С телефона хода не было вовсе, и
// смотрят POC в том числе с телефона.
//
// Предмет стенда: кнопка «Войти» поднимает вход и приносит ссылку авторизации
// кликабельной прямо на плашку, код уезжает отдельным полем мимо ленты и
// поля реплики, отказ каждого шага назван словами на самой плашке, а после
// входа разговор поднимается прежней кнопкой перезапуска.
//
// Зовётся: node testdata/poc_loginlink.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const URL_AUTH = "https://claude.ai/oauth/authorize?client_id=test&state=abc123";

let items = [];
const asked = [];
const bodies = [];
let loginFails = "";   // слова отказа подъёма входа, пусто значит подъём удался
let codeFails = "";    // слова отказа кода, пусто значит код принят
let codeStatus = 409;
let loginWay = "code";
let waitLeft = 0;

const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    bodies.push(init.body ? JSON.parse(init.body) : null);
    if (path.endsWith("/chats/login")) {
      if (loginFails) {
        return { raw: { status: 502, statusText: "Bad Gateway",
          text: JSON.stringify({ error: loginFails }) } };
      }
      return { tmux: "login-1", url: URL_AUTH, way: loginWay,
        message: "откройте ссылку и войдите" };
    }
    if (path.endsWith("/login/wait")) {
      if (waitLeft > 0) {
        waitLeft -= 1;
        return { waiting: true, message: "вход ещё идёт" };
      }
      return { ok: true, message: "вход сделан: свежий токен лёг в связку ключей" };
    }
    if (path.endsWith("/login/code")) {
      if (codeFails) {
        return { raw: { status: codeStatus, statusText: "Conflict",
          text: JSON.stringify({ error: codeFails }) } };
      }
      return { ok: true,
        message: "вход сделан: свежий токен лёг в связку ключей" };
    }
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

// Разлогиненный разговор: поле login у entry поднимает плашку.
const out = (sid) => ({
  addr: sid, sid, task: "DK-397", chats: [], models: [], project: "demo",
  fresh: false, error: "", note: "",
  entry: { id: sid, state: "live", tasks: ["DK-397"], model: "opus",
    tmux: "chat-DK-397-1", own: true, login: "сессия разлогинена" },
});

const clear = () => { asked.length = 0; bodies.length = 0; };
const stepOf = (what) => asked.findIndex((p) => p.endsWith(what));
// Слова исхода живут в своей реплике: разлогин, шаг входа и исход это три
// разных сообщения чата, и путать их нельзя.
const wordsOf = (panel) => dump(byClass(panel, "loginsaid"));

// --- до входа: кнопка есть, шаг входа скрыт ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-1111"));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("разлогин в чате не сказан: " + dump(panel));
  const enter = deepBtn(panel, "Войти");
  if (!enter) fail("кнопки входа в реплике нет: " + dump(plate));
  if (!deepBtn(panel, "Перезапустить")) fail("кнопки перезапуска нет: " + dump(plate));
  const flow = byClass(panel, "loginstep");
  if (flow && !flow.hidden) fail("шаг входа виден до нажатия кнопки: " + dump(flow));
}

// --- кнопка входа приносит ссылку кликабельной и поле кода ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-2222"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (stepOf("/chats/login") < 0) fail("кнопка входа не позвала сервер: " + JSON.stringify(asked));
  const flow = byClass(panel, "loginstep");
  if (!flow || flow.hidden) fail("шаг входа не показан после подъёма: " + dump(panel));
  const link = byClass(panel, "loginurl");
  if (!link || String(link.href) !== URL_AUTH) {
    fail("ссылка авторизации не кликабельна или не та: " + dump(flow));
  }
  if (String(link.target) !== "_blank") fail("ссылка не открывается новой вкладкой: " + dump(flow));
  // Полный адрес живёт в самой ссылке, а на экране стоит короткий текст:
  // портянка в четыреста знаков занимала весь экран (замечание пользователя).
  if (String(link.textContent).includes("://")) {
    fail("адрес напечатан портянкой вместо человеческого текста: " + link.textContent);
  }
  if (String(link.textContent).trim().length > 40) {
    fail("текст ссылки длиннее человеческого: " + link.textContent);
  }
  // Вход с другого устройства начинается с переноса адреса, и копировать его
  // человеку есть чем.
  if (!deepBtn(flow, "копировать") && !dump(flow).toLowerCase().includes("копи")) {
    fail("копировать адрес нечем: " + dump(flow));
  }
  const code = byClass(panel, "logincode");
  if (!code) fail("поля кода на шаге входа нет: " + dump(flow));
  if (!deepBtn(panel, "Подтвердить")) fail("кнопки отправки кода нет: " + dump(flow));
}

// --- код уезжает своим ходом и не попадает ни в ленту, ни в поле реплики ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-3333"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  const code = byClass(panel, "logincode");
  code.value = "SECRET1";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const step = stepOf("/login/code");
  if (step < 0) fail("код не отправлен на сервер: " + JSON.stringify(asked));
  if (!bodies[step] || bodies[step].code !== "SECRET1") {
    fail("код уехал не телом кода: " + JSON.stringify(bodies[step]));
  }
  // Подъём разговора после входа идёт репликой, и кода в ней быть не должно:
  // журнал разговора одноразовый ключ хранить не может.
  for (const body of bodies) {
    if (body && String(body.text || "").includes("SECRET1")) {
      fail("код уехал репликой разговора: " + JSON.stringify(body));
    }
  }
  if (code.value !== "") fail("код остался в поле после отправки: " + code.value);
  if (dump(panel).includes("SECRET1")) fail("код остался на экране панели: " + dump(panel));
  const feed = byClass(panel, "chatfeed");
  if (feed && dump(feed).includes("SECRET1")) fail("код попал в ленту разговора: " + dump(feed));
  const words = wordsOf(panel);
  if (!words.toLowerCase().includes("вход сделан")) fail("успех входа не назван словами: " + words);
  // После входа разговор поднимается сам и доделывает прерванное: второй
  // кнопки человеку нажимать не за чем.
  if (stepOf("/stop") < 0 || stepOf("/say") < 0) {
    fail("после входа разговор не поднялся сам: " + JSON.stringify(asked));
  }
  // Кнопка перезапуска остаётся: она и поднимает разговоры после входа.
  if (!deepBtn(panel, "Перезапустить")) fail("кнопки перезапуска после входа нет");
}

// --- отказ подъёма назван словами на плашке, кнопка входа жива ---
{
  loginFails = "клиент не напечатал ссылку авторизации за 20s: " +
    "вид панели входа, видимо, сменился, разбор надо чинить";
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-4444"));
  await settle();
  const enter = deepBtn(panel, "Войти");
  enter.handlers.click({ stopPropagation: () => {} });
  await settle();
  const words = wordsOf(panel);
  if (!words.includes("не напечатал ссылку")) {
    fail("пропажа ссылки не названа словами: " + words);
  }
  if (!wordsOf(panel).includes("разбор")) fail("слова отказа обрезаны: " + words);
  if (enter.disabled) fail("после отказа кнопка входа осталась запертой");
  loginFails = "";
}

// --- неверный код: слова отказа, поле остаётся для второй попытки ---
{
  codeFails = "код не принят: клиент снова ждёт код авторизации, введите другой";
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-5555"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  const code = byClass(panel, "logincode");
  code.value = "WRONG1";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!wordsOf(panel).includes("код не принят")) {
    fail("отказ кода не назван словами: " + wordsOf(panel));
  }
  if (byClass(panel, "loginstep").hidden) fail("шаг входа скрыт после отказа кода");
  // Вторая попытка в том же шаге доезжает до успеха.
  codeFails = "";
  code.value = "GOOD1";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!wordsOf(panel).toLowerCase().includes("вход сделан")) {
    fail("вторая попытка кода не дошла до успеха: " + wordsOf(panel));
  }
}

// --- вход оборвался на середине: слова про смерть сессии ---
{
  codeFails = "сессия входа умерла на середине входа: клиент закрылся";
  codeStatus = 502;
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-6666"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  byClass(panel, "logincode").value = "ANY1";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!wordsOf(panel).includes("умерла")) {
    fail("смерть сессии входа не названа словами: " + wordsOf(panel));
  }
  codeFails = "";
  codeStatus = 409;
}

// --- погашенный шаг входа уходит с экрана тем же правилом, что и плашка ---
{
  const css = (await import("node:fs")).readFileSync(
    (await import("node:path")).join(app, "..", "style.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const rules = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    rules.push({ sel: m[1].replace(/^[\s}]+/, "").trim(), decl: m[2] });
  }
  const own = (sel, classes, withHidden) => sel.split(",").some((part) => {
    const m = /^((?:\.[A-Za-z0-9_-]+)+)(\[hidden\])?$/.exec(part.trim());
    if (!m || Boolean(m[2]) !== withHidden) return false;
    return m[1].split(".").filter(Boolean).every((c) => classes.includes(c));
  });
  const panel = sandbox.chatPanel("demo", out("aaaa5770-7777"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  const flow = byClass(panel, "loginstep");
  const classes = String(flow.className).split(" ").filter(Boolean);
  const shows = rules.filter((r) => own(r.sel, classes, false) && /display\s*:/.test(r.decl));
  const hides = rules.filter((r) => own(r.sel, classes, true) && /display\s*:\s*none/.test(r.decl));
  if (shows.length && !hides.length) {
    fail("display шагу входа даёт правило " + shows.map((r) => r.sel).join(", ") +
      ", а парного [hidden]{display:none} в style.css нет: погашенный шаг " +
      "останется на экране");
  }
}

// --- вход с самой машины идёт без кода: шаг один, открыть ссылку ---
{
  loginWay = "local";
  waitLeft = 2;
  clear();
  const panel = sandbox.chatPanel("demo", out("aaaa5770-8888"));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  // Шаг входа к этой минуте уже отработал и погас: петля прошла целиком, и
  // человек видит исход, а не оставленную ссылку.
  const shown = dump(panel);
  if (!shown.includes("Код дашборд возьмёт сам")) {
    fail("слова петлевого входа не сказаны: " + shown);
  }
  const code = byClass(panel, "logincode");
  const row = code && code.parentNode;
  if (row && !row.hidden) {
    fail("поле кода показано там, где код берёт сам клиент: " + dump(panel));
  }
  if (stepOf("/login/wait") < 0) {
    fail("исход петлевого входа не ждали: " + JSON.stringify(asked));
  }
  if (!wordsOf(panel).toLowerCase().includes("вход сделан")) {
    fail("успех петлевого входа не назван словами: " + wordsOf(panel));
  }
  if (stepOf("/say") < 0) fail("после петлевого входа разговор не поднялся: " + JSON.stringify(asked));
  loginWay = "code";
  waitLeft = 0;
}

console.log("ок: вход идёт репликами чата: кнопка поднимает вход, ссылка " +
  "человеческая и копируется, с самой машины кода нет вовсе, с другого " +
  "устройства код едет своим полем мимо ленты, отказы названы словами, " +
  "успех сам поднимает разговор");

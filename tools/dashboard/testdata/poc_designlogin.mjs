// Стенд входа в Claude Design с плашки дашборда (DK-920).
//
// Живой случай 2026-09-10: сессия открылась с отказом подключения к серверу
// claude-design, api.anthropic.com ответил 403 на токен обычного входа и попросил
// /design-login. Из дашборда команду не подать, плашка входа знала одну команду
// /login, и человеку оставался терминал на машине. С телефона хода не было вовсе.
//
// Предмет стенда: блок в ленте один, а вид входа выбирает признак подъёма
// (решение пользователя 2026-09-26). Отказ сервера макетов даёт кнопку «Войти в
// Claude Design», разлогин клиента прежнюю «Войти», вид уезжает в теле всех трёх
// ручек входа, и ответ агента блок макетов не гасит: сервер молчит до конца жизни
// процесса. Здесь же два места из ревью: отказ, пришедший при открытой панели,
// поднимает блок сам, а реплика перезапуска называет тот вход, который делали.
//
// Тут же находка приёмки 2026-09-29: запись с кнопкой висела в ленте и после
// удачного входа, плашкой поверх разговора. Гаснет она удачей самого входа и
// снятым признаком отказа, а держат её неподнятый разговор и идущий вход.
//
// Зовётся: node testdata/poc_designlogin.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const URL_AUTH = "https://claude.ai/oauth/authorize?client_id=design&state=abc123";

let items = [];
const asked = [];
const bodies = [];
// Ответ ручки состояния разговора. Признаки входа едут им же: панель собрана
// однажды, а отказ приходит и позже, при открытой панели.
let status = { live: true, busy: false };
// Дорога входа: с телефона код руками, с самой машины клиент ловит его петлёй, и
// исход тогда ждётся своей ручкой.
let road = "code";
// Отказ снятия сессии: вход к этому месту принят, а перезапуститься не вышло.
let dropFail = "";

const { sandbox, streams, timers } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    asked.push(path);
    const body = init.body ? JSON.parse(init.body) : null;
    bodies.push(body);
    if (path.endsWith("/chats/login")) {
      return { tmux: "login-1", url: URL_AUTH, way: road,
        kind: body && body.kind, message: "откройте ссылку и войдите" };
    }
    if (path.endsWith("/login/code") || path.endsWith("/login/wait")) {
      return { ok: true, message: "вход сделан: свежий токен лёг в связку ключей" };
    }
    if (path.endsWith("/stop")) {
      if (dropFail) {
        return { raw: { status: 500, statusText: "",
          text: JSON.stringify({ error: dropFail }) } };
      }
      return { way: "drop", tmux: "chat-DK-909-1" };
    }
    if (path.endsWith("/say")) return { way: "resume", tmux: "chat-DK-909-2" };
    return {};
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.endsWith("/status")) return status;
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

// Разговор с признаком в строке: поле design у разговора считает сервер по записи
// транскрипта, панель английских слов не разбирает.
const out = (sid, entry) => {
  const e = Object.assign({ id: sid, state: "live", tasks: ["DK-909"], model: "opus",
    tmux: "chat-DK-909-1", own: true }, entry);
  // Признаки входа сервер считает одной шапкой транскрипта и отдаёт обоими
  // ходами: строкой разговора при сборке панели и ручкой состояния при опросе.
  // Пока стенд держал их врозь, опрос гасил запись, поднятую строкой.
  status = { live: true, busy: false };
  if (e.login) status.login = true;
  if (e.design) status.design = true;
  return {
    addr: sid, sid, task: "DK-909", chats: [], models: [], project: "demo",
    fresh: false, error: "", note: "", entry: e,
  };
};

// Круг опроса состояния: панель ставит его таймером, а в стенде время не идёт.
const beat = async () => {
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

const clear = () => { asked.length = 0; bodies.length = 0; };
const stepOf = (what) => asked.findIndex((p) => p.endsWith(what));
const btnText = (node) => String(node.textContent || "").trim();

// --- отказ сервера макетов поднимает блок сам, и кнопка называет свой вход ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-1111",
    { design: "нужен вход в Claude Design" }));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("отказ сервера макетов в чате не сказан: " + dump(panel));
  const said = dump(plate);
  if (!said.includes("Claude Design")) fail("в записи не сказано, чего не хватает: " + said);
  // Разлогином это звать нельзя: вход в клиента тут жив, и «повторная
  // аутентификация» отправила бы человека делать не тот вход.
  if (said.includes("повторная аутентификация")) {
    fail("отказ макетов назван разлогином клиента: " + said);
  }
  const enter = deepBtn(panel, "Войти");
  if (!enter) fail("кнопки входа в записи нет: " + said);
  if (btnText(enter) !== "Войти в Claude Design") {
    fail("кнопка не называет вход в макеты: " + btnText(enter));
  }
  // Кнопка одна: вторая рядом обещала бы выбор, которого человеку делать не надо.
  if (deepBtn(plate, "Войти в клиента")) fail("рядом встала вторая кнопка входа: " + said);
}

// --- вид входа уезжает в теле всех трёх ручек ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-2222",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  const raise = stepOf("/chats/login");
  if (raise < 0) fail("кнопка не позвала сервер: " + JSON.stringify(asked));
  if (!bodies[raise] || bodies[raise].kind !== "design") {
    fail("подъём пошёл без вида входа: " + JSON.stringify(bodies[raise]));
  }
  const link = byClass(panel, "loginurl");
  if (!link || String(link.href) !== URL_AUTH) {
    fail("ссылка авторизации не приехала: " + dump(byClass(panel, "loginstep")));
  }
  const code = byClass(panel, "logincode");
  if (!code) fail("поля кода нет: " + dump(byClass(panel, "loginstep")));
  code.value = "SECRET1";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const sent = stepOf("/login/code");
  if (sent < 0) fail("код не отправлен: " + JSON.stringify(asked));
  if (bodies[sent].kind !== "design") {
    fail("код уехал без вида входа, и сервер подал бы его в чужой диалог: " +
      JSON.stringify(bodies[sent]));
  }
  if (bodies[sent].code !== "SECRET1") fail("код уехал не тот: " + JSON.stringify(bodies[sent]));
  // Код это одноразовый ключ: поле после отправки чистое, а ленте и полю реплики
  // он не достаётся.
  if (code.value !== "") fail("код остался в поле после отправки: " + code.value);
}

// --- дорога с самой машины: ожидание входа тоже знает свой вид ---
{
  clear();
  road = "local";
  const panel = sandbox.chatPanel("demo", out("dddd9200-3333",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  const wait = stepOf("/login/wait");
  if (wait < 0) fail("исход входа петлёй не ждётся: " + JSON.stringify(asked));
  if (bodies[wait].kind !== "design") {
    fail("ожидание входа пошло без вида: " + JSON.stringify(bodies[wait]));
  }
  // С самой машины поля кода нет вовсе: шаг один, открыть ссылку.
  const codeRow = byClass(panel, "logincode");
  if (codeRow && !codeRow.hidden && codeRow.parentNode && !codeRow.parentNode.hidden) {
    fail("поле кода встало на дороге петлёй: " + dump(byClass(panel, "loginstep")));
  }
  road = "code";
}

// --- ответ агента блок макетов не гасит ---
{
  clear();
  const sid = "dddd9200-4444";
  items = [{ key: "m-1", role: "user", text: "собери макет",
    time: "2026-09-15T20:32:00+03:00" }];
  const panel = sandbox.chatPanel("demo", out(sid, { design: "нужен вход в Claude Design" }));
  await settle();
  if (byClass(panel, "cbyetalk").hidden) fail("блок макетов не поднялся: " + dump(panel));
  const es = streams.find((s) => String(s.url).includes(sid) && String(s.url).includes("stream"));
  if (!es) fail("поток ленты не открыт");
  es.onmessage({ data: JSON.stringify({ key: "m-2", role: "assistant",
    text: "сервер макетов не отвечает, делаю остальное",
    time: "2026-09-15T20:33:00+03:00" }) });
  await settle();
  if (byClass(panel, "cbyetalk").hidden) {
    fail("ответ агента погасил блок входа в макеты, хотя сервер молчит до конца " +
      "жизни процесса");
  }
  items = [];
}

// --- разлогин клиента остался прежним входом ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-5555",
    { login: "нужен вход" }));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("разлогин в чате не сказан: " + dump(panel));
  const enter = deepBtn(panel, "Войти");
  if (btnText(enter) !== "Войти") fail("кнопка обычного входа сменила слова: " + btnText(enter));
  enter.handlers.click({ stopPropagation: () => {} });
  await settle();
  const raise = stepOf("/chats/login");
  if (bodies[raise].kind !== "client") {
    fail("обычный вход пошёл не своим видом: " + JSON.stringify(bodies[raise]));
  }
}

// --- слова записи: расходится первая фраза, порядок действий сказан один ---
//
// Первая фраза называет беду, а дальше у обоих видов одно и то же. Пользователь
// на приёмке назвал этот текст дословно: устройство беды человеку не нужно, ему
// нужно, какой вход сделать. Хвост тут сверяется знак в знак у обоих видов, чтобы
// две копии слов не разъехались на первой же правке.
{
  clear();
  const TAIL = "Войдите заново или нажмите «Перезапустить», если уже прошли " +
    "аутентификацию в консоли или другом чате.";
  const saidOf = async (sid, entry) => {
    const panel = sandbox.chatPanel("demo", out(sid, entry));
    await settle();
    return dump(byClass(panel, "cbyetalk")).replace(/\s+/g, " ");
  };
  const design = await saidOf("dddd9200-cccc", { design: "нужен вход в Claude Design" });
  if (!design.includes("Требуется аутентификация Claude Design. " + TAIL)) {
    fail("слова записи входа в макеты разошлись с приёмкой: " + design);
  }
  const client = await saidOf("dddd9200-dddd", { login: "нужен вход" });
  if (!client.includes("Требуется повторная аутентификация. " + TAIL)) {
    fail("слова записи обычного входа разошлись с приёмкой: " + client);
  }
}

// --- оба признака сразу: сперва вход в клиента, он старше ---
//
// Заходы всё равно идут подряд, а без живого входа в клиента вход в макеты
// делать нечем: разлогиненный клиент не откроет и диалога.
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-6666",
    { login: "нужен вход", design: "нужен вход в Claude Design" }));
  await settle();
  const enter = deepBtn(panel, "Войти");
  if (btnText(enter) !== "Войти") {
    fail("при обоих признаках первым пошёл вход в макеты: " + btnText(enter));
  }
}

// --- здоровый разговор блока не видит вовсе ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-7777", {}));
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (plate && !plate.hidden) fail("блок входа встал на здоровом разговоре: " + dump(plate));
}

// --- чужое окно: сказана та команда, которую человек сделает руками ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-8888",
    { design: "нужен вход в Claude Design", tmux: "", own: false }));
  await settle();
  const said = dump(byClass(panel, "cbyetalk"));
  if (!said.includes("/design-login")) {
    fail("человеку не сказано, какую команду делать руками: " + said);
  }
}

// --- отказ при открытой панели поднимает блок сам ---
//
// Разговор открыт здоровым, умер и поднялся заново уже без сервера макетов.
// Состояние панели собрано однажды, и блок вставал только переоткрытием
// разговора (замечание ревью). Признаки входа едут ответом о состоянии, который
// панель и так опрашивает.
{
  clear();
  const st = out("dddd9200-9999", {});
  status = { live: true, busy: false, design: true };
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) {
    fail("отказ, пришедший при открытой панели, блока не поднял: " + dump(panel));
  }
  const enter = deepBtn(panel, "Войти");
  if (btnText(enter) !== "Войти в Claude Design") {
    fail("поднятый блок называет не тот вход: " + btnText(enter));
  }
  if (!dump(plate).includes("Claude Design")) {
    fail("в поднятом блоке не сказано, чего не хватает: " + dump(plate));
  }
}

// --- снятый признак гасит запись сам ---
//
// Вход сделали в другом окне, разговор поднялся заново и подключился к серверу
// макетов. Живой сервер ответ называет своим полем, и записи в ленте висеть не с
// чего.
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-eeee",
    { design: "нужен вход в Claude Design" }));
  await settle();
  if (byClass(panel, "cbyetalk").hidden) fail("блок макетов не поднялся: " + dump(panel));
  status = { live: true, busy: false, designUp: true };
  await beat();
  if (!byClass(panel, "cbyetalk").hidden) {
    fail("живой сервер запись не погасил: " + dump(byClass(panel, "cbyetalk")));
  }
}

// --- а неузнанное состояние сервера записи не трогает ---
//
// Замечание третьего круга ревью. Ответ о состоянии разговора молчит про макеты
// и когда сервер подключён, и когда транскрипта не нашлось вовсе. Пока поле было
// одно, запись с кнопкой гасило любое молчание, и человек оставался без кнопки до
// переоткрытия разговора.
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-dddd",
    { design: "нужен вход в Claude Design" }));
  await settle();
  if (byClass(panel, "cbyetalk").hidden) fail("блок макетов не поднялся: " + dump(panel));
  status = { live: true, busy: false };
  await beat();
  await beat();
  if (byClass(panel, "cbyetalk").hidden) {
    fail("запись погасла на ответе, который про сервер макетов ничего не знает");
  }
}

// --- запись уходит удачей входа и обратно не возвращается ---
//
// Находка приёмки: человек проходил вход, разговор перезапускался и доделывал
// прерванный запрос, а запись с кнопкой продолжала висеть сверху до
// переоткрытия разговора. Прежний признак её не возвращает: запись транскрипта
// про подключённый сервер макетов приходит секундой позже, и до неё опрос несёт
// всё тот же отказ.
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-ffff",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  byClass(panel, "logincode").value = "SECRET2";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle(300);
  if (stepOf("/say") < 0) fail("разговор после входа не перезапущен: " + JSON.stringify(asked));
  if (!byClass(panel, "cbyetalk").hidden) {
    fail("после удачного входа запись с кнопкой осталась в ленте: " +
      dump(byClass(panel, "cbyetalk")));
  }
  // Опрос состояния идёт кругами, и прежний признак приходит ещё не раз.
  await beat();
  await beat();
  if (!byClass(panel, "cbyetalk").hidden) {
    fail("прежний признак вернул запись с кнопкой после входа: " +
      dump(byClass(panel, "cbyetalk")));
  }
}

// --- вход принят, а перезапуститься не вышло: запись остаётся ---
//
// Беда тут не кончилась: разговор живёт в окне, которого у дашборда в реестре
// нет, и человеку сказано послать реплику ещё раз. Гасить запись нечем.
{
  clear();
  dropFail = "сессию разговора не удалось снять";
  const panel = sandbox.chatPanel("demo", out("dddd9200-1010",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  byClass(panel, "logincode").value = "SECRET3";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle(300);
  if (stepOf("/say") >= 0) fail("разговор поднят резюмом поверх живого клиента");
  if (byClass(panel, "cbyetalk").hidden) {
    fail("запись ушла с неподнятого разговора, и беда осталась без слов");
  }
  dropFail = "";
}

// --- гашения посреди идущего входа нет ---
//
// Человек держит перед собой ссылку авторизации и поле кода. Признак снимается
// мимо него (вход в другом окне), и убрать эту запись из-под руки нельзя: код
// вводить станет некуда.
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-1111ab",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  status = { live: true, busy: false, designUp: true };
  await beat();
  if (byClass(panel, "cbyetalk").hidden) {
    fail("запись погасла посреди входа, и код вводить стало некуда");
  }
}

// --- после входа в клиента остаётся вход в макеты ---
//
// При обоих признаках заходы идут подряд. Вход в клиента сделан, запись его
// погасил свежий ответ агента в ленте, а сервер макетов по-прежнему не дан:
// запись поднимается заново, своими словами и с отпертой кнопкой.
{
  clear();
  const sid = "dddd9200-1212";
  items = [{ key: "m-1", role: "user", text: "собери макет",
    time: "2026-09-15T20:32:00+03:00" }];
  const panel = sandbox.chatPanel("demo", out(sid,
    { login: "нужен вход", design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Войти").handlers.click({ stopPropagation: () => {} });
  await settle();
  byClass(panel, "logincode").value = "SECRET4";
  deepBtn(panel, "Подтвердить").handlers.click({ stopPropagation: () => {} });
  await settle(300);
  const es = streams.filter((st_) => String(st_.url).includes(sid) &&
    String(st_.url).includes("stream")).pop();
  if (!es) fail("поток ленты не открыт");
  es.onmessage({ data: JSON.stringify({ key: "m-2", role: "assistant",
    text: "поднялся заново, продолжаю", time: "2026-09-15T20:34:00+03:00" }) });
  await settle();
  if (!byClass(panel, "cbyetalk").hidden) {
    fail("запись входа в клиента не погасил свежий ответ агента: " +
      dump(byClass(panel, "cbyetalk")));
  }
  status = { live: true, busy: false, design: true };
  await beat();
  const plate = byClass(panel, "cbyetalk");
  if (!plate || plate.hidden) fail("вход в макеты после входа в клиента не сказан");
  if (!dump(plate).includes("Claude Design")) {
    fail("поднятая запись говорит не про макеты: " + dump(plate));
  }
  const enter = deepBtn(panel, "Войти");
  if (!enter || btnText(enter) !== "Войти в Claude Design") {
    fail("кнопка второго захода не та: " + (enter ? btnText(enter) : "нет вовсе"));
  }
  if (enter.disabled || enter.parentNode.hidden) {
    fail("кнопка второго захода заперта, и нажать на записи нечего");
  }
  items = [];
}

// --- реплика перезапуска называет тот вход, который делали ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-aaaa",
    { design: "нужен вход в Claude Design" }));
  await settle();
  deepBtn(panel, "Перезапустить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = stepOf("/say");
  if (said < 0) fail("перезапуск разговор не поднял: " + JSON.stringify(asked));
  const text = String((bodies[said] || {}).text || "");
  // Вход в клиента тут был жив, и слова про истёкший вход агенту врали бы.
  if (text.includes("истёк вход")) {
    fail("агенту сказано про истёкший вход клиента, которого не было: " + text);
  }
  if (!text.includes("Claude Design")) {
    fail("реплика перезапуска не называет, чем разговор встал: " + text);
  }
}

// --- а разлогин остался при своих словах ---
{
  clear();
  const panel = sandbox.chatPanel("demo", out("dddd9200-bbbb", { login: "нужен вход" }));
  await settle();
  deepBtn(panel, "Перезапустить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = stepOf("/say");
  if (said < 0) fail("перезапуск разговор не поднял: " + JSON.stringify(asked));
  const text = String((bodies[said] || {}).text || "");
  if (!text.includes("истёк вход")) {
    fail("реплика перезапуска после разлогина сменила слова: " + text);
  }
}

console.log("ок: блок в ленте один, вид входа выбирает признак подъёма, вид едет " +
  "в теле ручек, слова записи расходятся одной первой фразой, ответ агента блок " +
  "макетов не гасит, отказ при открытой панели поднимает блок сам, реплика " +
  "перезапуска называет свой вход, удачный вход запись гасит и обратно её не " +
  "пускает, гасит её и узнанный живой сервер, а неподнятый разговор, идущий вход " +
  "и неузнанное состояние сервера её держат");

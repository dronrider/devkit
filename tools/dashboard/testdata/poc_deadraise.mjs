// Стенд смерти подъёма (DK-728).
//
// Сессию поднимает сервер, а ждёт её панель: опрос ходит в поиск по имени
// tmux-сессии, пока разговор не родится. Пока смерть подъёма была немой, опрос
// кончался молчанием, и на экране пять минут стояло «сессия вот-вот назовётся».
// Отсюда предмет стенда: ответ сервера со смертью кончает ожидание, гасит
// плашку подъёма и ставит причину с хвостом терминала в пустую ленту.
//
// Зовётся: node testdata/poc_deadraise.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "DK", sections: [{ key: "check", rows: [] }] };
const models = [{ model: "fable", tier: "max", harness: "claude-code" }];

const WHY = "сессия chat-7 прожила 3 с и умерла, не начав хода: клиент вышел, " +
  "не назвавшись в реестре, и реплика до агента не доехала";
const TAIL = "Invalid API key. Please run /login\nдо свидания";

// Память подъёма прошлой вкладки: панель обещает по ней сессию, и снять
// обещание обязана именно смерть.
const liftKey = "devkit.chat.lift.devkit/new";
const { sandbox, store } = makeSandbox(app, (path) => {
  const p = String(path);
  if (p.includes("tmux=chat-7")) {
    return { chats: [], dead: { tmux: "chat-7", why: WHY, tail: TAIL } };
  }
  if (p.includes("tmux=chat-9")) {
    return { chats: [], dead: { tmux: "chat-9", why: "сессия chat-9 прожила 1 с и умерла, не начав хода", tail: "" } };
  }
  if (p.includes("/sessions/")) return { session: "", head: {}, items: [], total: 0 };
  if (p.includes("/chats")) return { chats: [], models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
}, { realTimers: true, store: { [liftKey]: JSON.stringify({ tmux: "chat-7", born: Date.now() }) } });
await settle();

// Пометка ленты с хвостом терминала: первая строка идёт разделителем, хвост
// отдельным блоком с сохранёнными переносами.
const mark = sandbox.markEl(WHY + "\nПоследние строки терминала:\n" + TAIL);
const tailBox = byClass(mark, "marktail");
if (!tailBox) fail("хвост терминала не встал своим блоком: " + dump(mark));
if (!tailBox.textContent.includes("Invalid API key")) {
  fail("в блоке хвоста нет строк терминала: " + tailBox.textContent);
}
if (!dump(mark).includes(WHY)) fail("разделитель не назвал причину: " + dump(mark));
const plain = sandbox.markEl("модель изменена: fable -> opus");
if (byClass(plain, "marktail")) fail("однострочная пометка обзавелась блоком хвоста");
if (!plain.className.includes("day")) fail("однострочная пометка перестала быть разделителем");

// Опрос ожидания кончается смертью: панель стоит на адресе нового чата, ответ
// сервера несёт исход, и цикл возвращает его словом.
sandbox.location.hash = "#devkit/chat/new";
const sewn = await sandbox.chatSewLoop("devkit", "chat-7", "new", 1, 3);
if (sewn !== "dead") fail("опрос ожидания не кончился смертью: " + sewn);
if (store.has(liftKey)) fail("память подъёма пережила смерть сессии");
const said = dump(sandbox.document.getElementById("flashes"));
if (!said.includes("умерла")) fail("причина смерти не показана человеку: " + said);

// Повторный заход той же смерти второй строки не пишет: сервер отвечает ею
// каждому опросу, а карточка на экране одна.
sandbox.document.getElementById("flashes").replaceChildren();
const again = await sandbox.chatSewLoop("devkit", "chat-7", "new", 1, 3);
if (again !== "dead") fail("повторный опрос потерял смерть: " + again);
if (dump(sandbox.document.getElementById("flashes")).trim() !== "") {
  fail("та же смерть показана дважды: " + dump(sandbox.document.getElementById("flashes")));
}

// Панель нового чата: слова о смерти стоят на месте обещания, хвост терминала
// при них, плашка подъёма не мигает. Память подъёма тут нарочно возвращена:
// её ключ у экрана задачи свой, и гасить плашку обязан признак смерти.
store.set(liftKey, JSON.stringify({ tmux: "chat-7", born: Date.now() }));
if (!sandbox.chatDeadOf("devkit", "new")) fail("память о смерти не досталась панели");
const st = await sandbox.chatState("devkit", "new", board, []);
if (!st.dead || !st.dead.why) fail("состояние панели не знает о смерти: " + JSON.stringify(st.dead));
if (!st.lift) fail("стенд не удержал память подъёма, гасить нечего");
const panel = sandbox.chatPanel("devkit", st);
await settle();
const shown = dump(panel);
if (!shown.includes("умерла")) fail("лента не назвала смерть: " + shown.slice(0, 400));
if (!shown.includes("Invalid API key")) fail("в ленте нет хвоста терминала: " + shown.slice(0, 400));
if (shown.includes("вот-вот назовётся")) fail("панель по-прежнему обещает сессию: " + shown.slice(0, 400));
const busy = byClass(panel, "busyrow");
if (!busy) fail("плашки подъёма в панели нет вовсе");
if (!busy.hidden) fail("плашка подъёма мигает над мёртвой сессией");

// Дорога первой реплики (chatWait) кончается тем же словом. Проверяется она
// последней: своя смерть у неё другая, и памяти панели она достаётся тем же
// адресом, затирая прежнюю.
const waited = await sandbox.chatWait("devkit", "chat-9", "new");
if (waited !== "dead") fail("ожидание первой реплики не кончилось смертью: " + waited);

console.log("смерть подъёма кончает ожидание, гасит плашку и стоит в ленте с хвостом терминала");

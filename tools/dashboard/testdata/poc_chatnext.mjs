// Стенд переключения панели при уборке открытого чата (DK-656, DK-970).
//
// Живой случай: список чатов открыт затем, чтобы отправить открытый чат в
// архив. После уборки панель оставалась на убранном разговоре, и человеку
// приходилось лезть в список второй раз, чтобы выбрать следующий (разбор
// автора: «после закрытия чата диалог сам не закрывается»).
//
// Второй живой случай (DK-970): у разговора есть задача, других чатов у неё
// нет, и панель уходила в «Новый чат», хотя список проекта полон. Замена
// искалась среди чатов задачи даже там, где задачу назвал не адрес панели, а
// поле tasks самого разговора.
//
// Предмет стенда: уборка открытого чата в архив переводит панель на
// следующий из оставшихся, без второго захода в список.
//
// Зовётся: node testdata/poc_chatnext.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID1 = "aaaa1111-1111-1111-1111-111111111111";
const SID2 = "bbbb2222-2222-2222-2222-222222222222";

const feeds = {
  [SID1]: [{ key: SID1 + ":1", seq: 1, role: "user",
    time: "2026-08-31T10:00:00+03:00", text: "первая реплика текущего разговора" }],
  [SID2]: [{ key: SID2 + ":1", seq: 1, role: "user",
    time: "2026-08-31T09:00:00+03:00", text: "первая реплика следующего разговора" }],
};

// Один заход стенда: панель открывает разговор своим адресом, человек лезет в
// список и убирает открытый чат в архив. Что стало с панелью дальше, сверяет
// сам сценарий.
async function archiveCurrent(chats, addr, what) {
  let archived = null;
  const { sandbox } = makeSandbox(app, (path, init) => {
    if (path.includes("/archive")) {
      const body = JSON.parse((init && init.body) || "{}");
      archived = { path, body };
      return { session: "x", archived: body.archived, message: "сессия снята" };
    }
    if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
    if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
    if (path.includes("/chats")) return { chats, models: [] };
    if (path.includes("/sessions/")) {
      const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
      const items = feeds[sid] || [];
      return { session: sid, head: { id: sid }, items, total: items.length };
    }
    if (path === "/api/notifications") return { items: [] };
    return {};
  });

  const pin = sandbox.document.getElementById("cpin");
  const slotOf = () => allByClass(pin, "cslot").find((s) => !String(s.className).includes("off"));

  sandbox.location.hash = addr;
  await sandbox.refresh();
  await settle();

  const first = slotOf();
  if (!first) fail(what + ": панель не собрала слот текущего разговора: " + dump(pin));
  if (!dump(first).includes("первая реплика текущего разговора")) {
    fail(what + ": лента текущего разговора не собралась: " + dump(first));
  }

  // Список открывается той же кнопкой, что у человека.
  const pick = byClass(first, "cdpick");
  if (!pick) fail(what + ": кнопки списка чатов в шапке нет: " + dump(first).slice(0, 300));
  pick.handlers.click({ stopPropagation: () => {} });
  await settle();
  const drop = byClass(first, "cdrop");
  if (!drop) fail(what + ": список чатов не открылся: " + dump(first).slice(0, 300));

  // Строка открытого чата отмечена подсветкой и стоит первой (группа
  // «открытый чат»): по ней и находим кнопку уборки.
  const rows = allByClass(drop, "cdrow");
  const curRow = rows.find((r) => String(r.className).includes(" on"));
  if (!curRow) fail(what + ": открытый чат в списке не отмечен: " + dump(drop).slice(0, 300));
  if (!dump(curRow).includes("открытый чат")) {
    fail(what + ": отмеченная строка не про открытый чат: " + dump(curRow));
  }
  const put = byClass(curRow, "cdarch");
  if (!put) fail(what + ": у строки открытого чата нет уборки в архив: " + dump(curRow));

  put.handlers.click({ stopPropagation: () => {} });
  await settle();

  if (!archived || !archived.path.includes("/chats/" + SID1 + "/archive") ||
      archived.body.archived !== true) {
    fail(what + ": уборка не позвала ручку архива для текущего разговора: " +
      JSON.stringify(archived));
  }
  return { sandbox, pin, slotOf };
}

// --- разговоры без задачи: панель встаёт на следующий из списка (DK-656) ---

const plain = [
  { id: SID1, title: "открытый чат", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T10:00:00+03:00", tasks: [] },
  { id: SID2, title: "следующий разговор", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T09:00:00+03:00", tasks: [] },
];

{
  const { sandbox, pin, slotOf } = await archiveCurrent(plain, "#demo/chat/" + SID1, "без задачи");
  if (!sandbox.location.hash.includes(SID2)) {
    fail("панель не переключилась на следующий разговор: " + sandbox.location.hash);
  }
  const now = slotOf();
  if (!dump(now).includes("первая реплика следующего разговора")) {
    fail("панель не показала следующий разговор: " + dump(pin));
  }
}

// --- у разговоров стоят задачи, и они разные (DK-970) ---
//
// Задача приезжает панели из поля tasks самого разговора, а не из адреса.
// Соседей по задаче у открытого чата нет, и до правки панель уходила в
// «Новый чат» мимо полного списка проекта.

const byTask = [
  { id: SID1, title: "открытый чат", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T10:00:00+03:00", tasks: ["XR-970"] },
  { id: SID2, title: "следующий разговор", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T09:00:00+03:00", tasks: ["XR-971"] },
];

{
  const { sandbox, pin, slotOf } = await archiveCurrent(byTask, "#demo/chat/" + SID1, "с задачей");
  if (!sandbox.location.hash.includes(SID2)) {
    fail("панель с задачей у разговора не встала на соседний разговор проекта: " +
      sandbox.location.hash);
  }
  const now = slotOf();
  if (!dump(now).includes("первая реплика следующего разговора")) {
    fail("панель с задачей у разговора не показала соседний разговор: " + dump(pin));
  }
}

// --- адрес назвал задачу, и соседей по ней нет: «Новый чат» тут законен ---

{
  const { sandbox } = await archiveCurrent(byTask, "#demo/chat/XR-970", "адрес задачи");
  if (sandbox.location.hash.includes(SID2)) {
    fail("панель, открытая адресом задачи, ушла в чужой разговор: " + sandbox.location.hash);
  }
  if (!sandbox.location.hash.includes("/chat/new")) {
    fail("панель, открытая адресом задачи, не ушла на новый чат: " + sandbox.location.hash);
  }
}

console.log("poc_chatnext: ok, уборка текущего разговора переводит панель на соседний " +
  "разговор проекта, а сужение по задаче остаётся адресу задачи");

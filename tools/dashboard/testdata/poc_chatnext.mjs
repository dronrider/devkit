// Стенд переключения панели при уборке текущего разговора (DK-656).
//
// Живой случай: список чатов открыт затем, чтобы отправить текущий разговор в
// архив. После уборки панель оставалась на убранном разговоре, и человеку
// приходилось лезть в список второй раз, чтобы выбрать следующий (разбор
// автора: «после закрытия чата диалог сам не закрывается»).
//
// Предмет стенда: уборка текущего разговора в архив переводит панель на
// следующий из оставшихся, без второго захода в список.
//
// Зовётся: node testdata/poc_chatnext.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID1 = "aaaa1111-1111-1111-1111-111111111111";
const SID2 = "bbbb2222-2222-2222-2222-222222222222";
const chats = [
  { id: SID1, title: "текущий разговор", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T10:00:00+03:00", tasks: [] },
  { id: SID2, title: "следующий разговор", project: "demo", state: "dead",
    idle: true, mtime: "2026-08-31T09:00:00+03:00", tasks: [] },
];
const feeds = {
  [SID1]: [{ key: SID1 + ":1", seq: 1, role: "user",
    time: "2026-08-31T10:00:00+03:00", text: "первая реплика текущего разговора" }],
  [SID2]: [{ key: SID2 + ":1", seq: 1, role: "user",
    time: "2026-08-31T09:00:00+03:00", text: "первая реплика следующего разговора" }],
};

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

sandbox.location.hash = "#demo/chat/" + SID1;
await sandbox.refresh();
await settle();

const first = slotOf();
if (!first) fail("панель не собрала слот текущего разговора: " + dump(pin));
if (!dump(first).includes("первая реплика текущего разговора")) {
  fail("лента текущего разговора не собралась: " + dump(first));
}

// Список открывается той же кнопкой, что у человека.
const pick = byClass(first, "cdpick");
if (!pick) fail("кнопки списка чатов в шапке нет: " + dump(first).slice(0, 300));
pick.handlers.click({ stopPropagation: () => {} });
await settle();
const drop = byClass(first, "cdrop");
if (!drop) fail("список чатов не открылся: " + dump(first).slice(0, 300));

// Строка текущего разговора отмечена подсветкой и стоит первой (группа
// «текущий разговор»): по ней и находим кнопку уборки.
const rows = allByClass(drop, "cdrow");
const curRow = rows.find((r) => String(r.className).includes(" on"));
if (!curRow) fail("текущий разговор в списке не отмечен: " + dump(drop).slice(0, 300));
if (!dump(curRow).includes("текущий разговор")) {
  fail("отмеченная строка не про текущий разговор: " + dump(curRow));
}
const put = byClass(curRow, "cdarch");
if (!put) fail("у строки текущего разговора нет уборки в архив: " + dump(curRow));

// --- уборка текущего разговора в архив ---
put.handlers.click({ stopPropagation: () => {} });
await settle();

if (!archived || !archived.path.includes("/chats/" + SID1 + "/archive") || archived.body.archived !== true) {
  fail("уборка не позвала ручку архива для текущего разговора: " + JSON.stringify(archived));
}
if (!sandbox.location.hash.includes(SID2)) {
  fail("панель не переключилась на следующий разговор: " + sandbox.location.hash);
}
const now = slotOf();
if (!dump(now).includes("первая реплика следующего разговора")) {
  fail("панель не показала следующий разговор: " + dump(pin));
}

console.log("poc_chatnext: ok, уборка текущего разговора переводит панель на следующий " +
  "без повторного захода в список");

// Стенд смены проекта под открытым разговором (замечание пользователя: «если
// находясь в этом чате выбрать другой проект, содержимое чата пропадает»).
// Разговоры общие по машине, и по своему адресу разговор один и тот же, с
// какой доски на него ни смотри: смена проекта в шапке меняет доску под
// панелью, а саму панель не трогает. Ручки чата при этом адресуются его
// собственным проектом: транскрипт ищется по дереву проекта, и лента,
// спрошенная у соседней доски, отвечала пустотой.
//
// Зовётся: node testdata/poc_chatproj.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const SID = "aaaa1111-2222";
const SAID = "реплика разговора, которая обязана пережить смену проекта";

const chats = [
  { id: SID, project: "demo", title: "Разговор devkit", tasks: [], state: "dead",
    model: "opus", mtime: "2026-08-23T10:00:00+03:00" },
];

const { sandbox, byId, asked } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", works: [], sections: {} },
      { name: "other", works: [], sections: {} }] };
  }
  if (path.endsWith("/board")) return {};
  if (path.includes("/chats/") && path.endsWith("/status")) return { live: false, busy: false };
  if (path.includes("/chats")) return { chats };
  // Транскрипт живёт в дереве своего проекта: у соседней доски его нет, как и
  // у настоящего сервера.
  if (path.includes("/api/projects/other/sessions/")) {
    return { raw: { status: 404, statusText: "Not Found", text: '{"error":"сессии нет в проекте"}' } };
  }
  if (path.includes("/sessions/" + SID)) {
    return { session: SID, head: { id: SID },
      items: [{ key: "m:1", role: "user", text: SAID, time: "2026-08-23T10:00:00+03:00" }],
      start: true };
  }
  return {};
});
await settle();

const pin = byId.get("cpin");

sandbox.location.hash = "#demo/chat/" + SID;
await sandbox.refresh();
await settle();
if (!dump(pin).includes(SAID)) fail("панель не открыла разговор своего проекта: " + dump(pin).slice(0, 200));

// Смена проекта в шапке: доска под панелью другая, разговор тот же.
const was = asked.length;
sandbox.location.hash = "#other/chat/" + SID;
await sandbox.refresh();
await settle();
if (!dump(pin).includes(SAID)) {
  fail("смена проекта погасила содержимое чата: " + dump(pin).slice(0, 300));
}
const wrong = asked.slice(was).filter((p) => p.includes("/api/projects/other/sessions/"));
if (wrong.length) {
  fail("лента разговора спрошена у чужой доски: " + JSON.stringify(wrong));
}

// Обратный случай: адрес панели с чужим проектом (вид проект~sid) переживает
// смену проекта доски.
sandbox.location.hash = "#other/chat/demo~" + SID;
await sandbox.refresh();
await settle();
if (!dump(pin).includes(SAID)) {
  fail("адрес с чужим проектом не открыл разговор: " + dump(pin).slice(0, 300));
}

console.log("ok: смена проекта доски не гасит открытый разговор, ленту он берёт " +
  "у своего проекта, адрес с чужим проектом жив");

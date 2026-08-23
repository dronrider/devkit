// Стенд входа в чат по задаче (живой случай: на форме DK-459 кнопка чата
// открыла чат DK-397). Адрес панели с ID задачи обязан открывать диалог самой
// задачи: последний её собственный, а не последний разговор всего проекта.
// Кандидатов даёт задача, а не chatVisible: тот слушает переключатель фильтра
// списка, и с выключенным фильтром выбор падал на первый чат проекта. Задача
// без своих диалогов открывает новый чат с её привязкой, память последнего
// чата панели (devkit.chat.last) во входе по задаче не участвует.
//
// Зовётся: node testdata/poc_chattask.mjs static/app.js

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const chats = [
  { id: "aaaa1111-0397", title: "Чат DK-397", tasks: ["DK-397"], state: "dead" },
  { id: "bbbb2222-0459", title: "Чат DK-459", tasks: ["DK-459"], state: "dead" },
];

const { sandbox } = makeSandbox(appPathArg(), (path) => {
  if (path.includes("/chats")) return { chats };
  return {};
});
await settle();

// Живой случай: фильтр списка выключен, последним в панели был чужой чат.
sandbox.localStorage.setItem("devkit.chat.filter", "0");
sandbox.localStorage.setItem("devkit.chat.last", "aaaa1111-0397");

let st = await sandbox.chatState("demo", "DK-459", null);
if (st.sid !== "bbbb2222-0459") {
  fail("вход по задаче DK-459 открыл не её диалог: " + JSON.stringify(st.sid));
}
if (st.task !== "DK-459") fail("привязка задачи потерялась: " + JSON.stringify(st.task));

// Задача без своих диалогов: пустой sid, панель откроет новый чат с её
// привязкой, а не последний разговор проекта.
st = await sandbox.chatState("demo", "DK-500", null);
if (st.sid) {
  fail("задача без диалогов открыла чужой разговор: " + JSON.stringify(st.sid));
}
if (st.task !== "DK-500") {
  fail("новый чат остался без привязки задачи: " + JSON.stringify(st.task));
}

// Включённый фильтр ведёт себя так же: свой диалог, а не первый в списке.
sandbox.localStorage.setItem("devkit.chat.filter", "1");
st = await sandbox.chatState("demo", "DK-459", null);
if (st.sid !== "bbbb2222-0459") {
  fail("со включённым фильтром вход по задаче сломался: " + JSON.stringify(st.sid));
}

// Память последнего чата задачи работает как раньше: из двух её диалогов
// открывается тот, где разговаривали последним.
chats.push({ id: "cccc3333-0459", title: "Чат DK-459 второй", tasks: ["DK-459"], state: "dead" });
sandbox.localStorage.setItem("devkit.chat.task.DK-459", "cccc3333-0459");
st = await sandbox.chatState("demo", "DK-459", null);
if (st.sid !== "cccc3333-0459") {
  fail("память последнего чата задачи не сработала: " + JSON.stringify(st.sid));
}

console.log("ok: вход по задаче открывает её собственный диалог при любом " +
  "фильтре, без диалогов новый чат с привязкой, чужая память не участвует");

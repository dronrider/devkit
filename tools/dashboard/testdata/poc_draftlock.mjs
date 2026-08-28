// Стенд замка редактора записи накопителя (ветка poc-chat).
//
// Писателей у файла записи двое: человек с экрана и агент разбора, который её
// читает, дописывает и уносит исходом. Экран не знал об этом ничего: карандаш
// стоял и под живым разбором, а сохранение уезжало без базы, то есть последняя
// запись затирала предыдущую молча.
//
// Предмет стенда: пока разбор идёт, правка заперта и плашка говорит, у кого
// запись; живое ожидание ответа замок отпирает; сохранение везёт базу, с
// которой экран открылся; разошедшаяся база показывает текст с диска, не теряя
// набранного.
//
// Зовётся: node testdata/poc_draftlock.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: [{ tier: "pro", model: "opus" }] },
];

const state = {
  text: "# XR-9: две записи об одном и том же\n",
  hash: "b1b1b1b1",
  waiting: null,
  works: [],
};
const puts = [];
let putReply = null;

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: state.works }] };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path.includes("/drafts/XR-9")) {
    if (init && init.method === "PUT") {
      puts.push(JSON.parse(init.body));
      return putReply || { id: "XR-9", file: "docs/tasks/drafts/XR-9.md",
        hash: "c2c2c2c2", message: "текст docs/tasks/drafts/XR-9.md записан" };
    }
    const out = { id: "XR-9", file: "docs/tasks/drafts/XR-9.md", text: state.text,
      hash: state.hash, order: "Разбери черновик XR-9" };
    if (state.waiting) out.waiting = state.waiting;
    return out;
  }
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: state.works };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
const go = async () => {
  sandbox.location.hash = "demo/draft/XR-9";
  await sandbox.refresh();
  await settle();
};
const pen = () => allByClass(groups, "tpen").find((b) => String(b.title) === "Править запись");
const field = () => {
  const found = [];
  const walk = (node) => {
    for (const kid of node.children || []) {
      if (kid.tagName === "TEXTAREA") found.push(kid);
      walk(kid);
    }
  };
  walk(groups);
  return found[0];
};
const type = (said) => {
  const ta = field();
  if (!ta) fail("поля правки на экране записи нет вовсе");
  ta.value = said;
  ta.handlers.input({});
};

await settle();
await sandbox.loadHarnesses();

// --- запись свободна: правка уезжает с базой ---
{
  await go();
  if (!pen()) fail("карандаша на свободной записи нет: " + dump(groups).slice(0, 300));
  if (byClass(groups, "dlock")) fail("плашка замка стоит над свободной записью");
  pen().handlers.click({});
  type(state.text + "\nдописано с экрана\n");
  const save = deepBtn(groups, "Сохранить");
  if (!save || save.hidden) fail("кнопки сохранения у тронутой формы нет");
  save.handlers.click({});
  await settle();
  if (!puts.length) fail("правка до ручки не доехала");
  if (puts[0].base !== state.hash) {
    fail("правка уехала без базы, с которой экран открылся: " + JSON.stringify(puts[0].base));
  }
  if (!String(puts[0].text).includes("дописано с экрана")) {
    fail("правка уехала не тем текстом: " + JSON.stringify(puts[0].text));
  }
}

// --- разбор идёт: правка заперта, плашка говорит, у кого запись ---
{
  state.works = [{ id: "XR-9", via: "tmux" }];
  await go();
  if (pen()) fail("карандаш стоит под живым разбором: правка затёрла бы работу агента");
  const lock = byClass(groups, "dlock");
  if (!lock) fail("плашки замка под живым разбором нет: " + dump(groups).slice(0, 300));
  const said = dump(lock).replace(/\s+/g, " ");
  if (!said.includes("Разбор идёт, запись у агента")) {
    fail("плашка не говорит, у кого запись: " + said);
  }
  if (!deepBtn(lock, "Стоп")) fail("забрать запись у агента с плашки нечем: " + said);
}

// --- живое ожидание отпирает замок ---
{
  state.waiting = { state: "ждёт ответа", source: "ask", note: "спросил агент" };
  await go();
  if (!pen()) fail("карандаш заперт на живом ожидании: ответ правкой текста это законная дорога");
  const said = dump(byClass(groups, "dlock") || { children: [] }).replace(/\s+/g, " ");
  if (!said.includes("Агент ждёт ответа, правка открыта")) {
    fail("плашка на ожидании говорит теми же словами, что и под разбором: " + said);
  }
}

// --- база разошлась: текст с диска показан, набранное осталось в поле ---
{
  state.works = [];
  state.waiting = null;
  await go();
  pen().handlers.click({});
  type("моя правка");
  putReply = { raw: { status: 409, statusText: "Conflict", text: JSON.stringify({
    error: "запись XR-9 изменилась с тех пор, как экран её прочитал",
    text: "# XR-9: правка второго окна\n", hash: "d3d3d3d3" }) } };
  deepBtn(groups, "Сохранить").handlers.click({});
  await settle();
  const clash = byClass(groups, "dclash");
  if (!clash) fail("разошедшаяся база молчит: " + dump(groups).replace(/\s+/g, " ").slice(0, 400));
  if (!dump(clash).includes("правка второго окна")) {
    fail("отказ не показал текст с диска: " + dump(clash).replace(/\s+/g, " "));
  }
  if (field().value !== "моя правка") {
    fail("набранное пропало из поля после отказа: " + JSON.stringify(field().value));
  }
}

console.log("poc_draftlock: ok");

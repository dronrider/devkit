import { makeSandbox, settle, allByClass, byClass, tag, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

// Ручная подготовка задачи с экрана записи (DK-1564): путь из README,
// «Подготовить задачу вручную». Приём на сервере требует первую редакцию 1,
// а правка сохранённой задачи идёт следующей редакцией. Пустая форма ручной
// подготовки открывается сразу в правке, сохранённая задача чтением.
const writes = [];
const state = { version: 1, state: "active", analysis: "", proposals: [] };
let snapshot = "s1";
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path === "/api/projects/demo/drafts/XR-9/prepared" && init && init.method === "PUT") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[0] = body.proposal; snapshot = "s2"; return { message: "задача сохранена" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/prepared") return { id: "XR-9", snapshot, state };
  if (path === "/api/projects/demo/drafts/XR-9") return { id: "XR-9", text: "# XR-9: встреча\n", hash: "h", type: "task", source: "thought", lock: "unlocked" };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
async function openScreen(hash) {
  sandbox.location.hash = hash;
  await sandbox.refresh(); await settle();
}
function byAria(root, part) {
  const walk = (n) => {
    if (!n) return null;
    const label = String((n.attrs && n.attrs["aria-label"]) || "");
    if ((n.tagName === "INPUT" || n.tagName === "TEXTAREA") && label.includes(part)) return n;
    for (const kid of n.children || []) {
      const hit = walk(kid);
      if (hit) return hit;
    }
    return null;
  };
  return walk(root);
}
await openScreen("demo/draft/XR-9/prepared/new");
const groups = byId.get("groups");
if (!deepBtn(groups, "Не заводить")) fail("на форме заведения нет выхода");
const title = byAria(groups, "заголовок задачи"); if (!title) fail("нет поля заголовка");
title.value = "ручная задача"; title.handlers.input({});
const text = tag(byClass(groups, "fbody"), "TEXTAREA"); if (!text) fail("нет поля описания");
text.value = "описание руками"; text.handlers.input({});
const save = deepBtn(groups, "Сохранить задачу"); if (!save) fail("нет сохранения");
save.handlers.click({}); await settle();
const first = writes[0];
if (!first || first.base !== "s1" || !String(first.proposal.id).startsWith("manual-")) fail("ручная задача не уехала на сервер");
if (first.proposal.revision !== 1) fail("первая редакция ушла revision " + first.proposal.revision + ", а не 1");
if (first.proposal.title !== "ручная задача" || first.proposal.outcome !== "add") fail("поля ручной задачи не доехали");
// Дальше правка сохранённой задачи растёт редакцией и идёт с карандаша:
// сохранённая задача открывается чтением.
await openScreen("demo/draft/XR-9/prepared/" + first.proposal.id);
const pen = deepBtn(byId.get("groups"), "tpen"); if (!pen) fail("нет карандаша правки");
pen.handlers.click({});
const saved = tag(byClass(byId.get("groups"), "fbody"), "TEXTAREA"); if (!saved) fail("нет поля описания правки");
saved.value = "описание поправлено"; saved.handlers.input({});
const edit = deepBtn(byId.get("groups"), "Сохранить"); if (!edit) fail("нет сохранения правки");
edit.handlers.click({}); await settle();
const second = writes[1];
if (!second || second.base !== "s2" || second.proposal.id !== first.proposal.id) fail("правка не уехала на сервер");
if (second.proposal.revision !== 2) fail("правка ушла revision " + second.proposal.revision + ", а не 2");
console.log("ручная подготовка сохраняется первой редакцией и растёт дальше");

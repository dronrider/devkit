import { makeSandbox, settle, allByClass, byClass, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

// Задачи готовит агент. Пользователь правит существующие задачи, а старый new возвращает исходник.
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
await openScreen("demo/draft/XR-9");
{
  const board = byId.get("groups");
  const comp = allByClass(board, "comp").find((n) => dump(n).includes("Задачи разбора"));
  if (!comp) fail("на экране записи нет карточки состава задач разбора");
  if (!dump(comp).includes("Подготовленных задач нет")) fail("пустой состав не сказал об этом словами");
  if (deepBtn(comp, "Подготовить вручную")) fail("ручная подготовка осталась на форме");
}
await openScreen("demo/draft/XR-9/prepared/new/chat/XR-9");
if (sandbox.location.hash !== "#demo/draft/XR-9/chat/XR-9") fail("старый new не вернул источник с чатом: " + sandbox.location.hash);
if (deepBtn(byId.get("groups"), "Сохранить задачу")) fail("старый new открыл создание");
if (writes.length) fail("новая задача записана без агента");
state.proposals.push({id:"legacy-p1",board_id:"XR-16",revision:1,outcome:"add",title:"задача агента",text:"описание агента",board_type:"task",size:"M",accept:"agent"});
const first = {proposal:state.proposals[0]};
// Дальше правка сохранённой задачи растёт редакцией и идёт с карандаша:
// сохранённая задача открывается чтением.
await openScreen("demo/draft/XR-9/prepared/" + first.proposal.id);
const pen = deepBtn(byId.get("groups"), "tpen"); if (!pen) fail("нет карандаша правки");
pen.handlers.click({});
const saved = tag(byClass(byId.get("groups"), "fbody"), "TEXTAREA"); if (!saved) fail("нет поля описания правки");
saved.value = "описание поправлено"; saved.handlers.input({});
const edit = deepBtn(byId.get("groups"), "Сохранить"); if (!edit) fail("нет сохранения правки");
edit.handlers.click({}); await settle();
const second = writes[0];
if (!second || second.base !== "s1" || second.proposal.id !== first.proposal.id) fail("правка не уехала на сервер");
if (second.proposal.revision !== 2) fail("правка ушла revision " + second.proposal.revision + ", а не 2");
console.log("ручного создания нет, старый new сохраняет чат, существующая задача редактируется");

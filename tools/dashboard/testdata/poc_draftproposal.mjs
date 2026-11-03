import { makeSandbox, settle, allByClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const writes = [];
const state = { version: 1, state: "active", analysis: "ok", proposals: [
  { id: "p1", revision: 1, outcome: "attach", target: "XR-1", text: "первая приписка" },
  { id: "p2", revision: 1, outcome: "drop", text: "старый фрагмент", reason: "нет нужды" },
] };
let snapshot = "s1";
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path === "/api/projects/demo/drafts/XR-9/prepared" && init && init.method === "PUT") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[0] = body.proposal; snapshot = "s2"; return { message: "задача сохранена" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/prepared/reject" && init && init.method === "POST") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[1].status = "rejected"; state.proposals[1].result = { reason: body.reason }; return { message: "задача отклонена" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/prepared") return { id: "XR-9", snapshot, state };
  if (path === "/api/projects/demo/drafts/XR-9") return { id: "XR-9", text: "# XR-9: встреча\n", hash: "h", type: "task", source: "thought", lock: "unlocked" };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
function cardText(node) {
  let out = typeof node.textContent === "string" ? node.textContent : "";
  for (const kid of node.children || []) out += "\n" + cardText(kid);
  return out;
}
async function openScreen(hash) {
  sandbox.location.hash = hash;
  await sandbox.refresh(); await settle();
}
await openScreen("demo/draft/XR-9");
const groups = byId.get("groups");
let rows = allByClass(groups, "draft-prepared-task");
if (rows.length !== 2) fail("две подготовленные задачи не показаны");
if (!cardText(rows[0]).includes("Подготовлена")) fail("приписка до решения не названа подготовленной");
if (!cardText(rows[1]).includes("Подготовлено")) fail("удаление до решения не названо подготовленным");
// Правка и отказ живут на экране самой задачи, куда ведёт строка списка
// (DK-1469, решение 4).
await openScreen("demo/draft/XR-9/prepared/p1");
let fields = allByClass(groups, "dfield");
function field(label) {
  const wrap = fields.find((n) => n.children[0] && n.children[0].textContent === label);
  if (!wrap) fail("нет поля " + label);
  return wrap.children[1];
}
const text = field("Описание");
text.value = "поправленная приписка"; text.handlers.input({});
const save = deepBtn(groups, "Сохранить"); if (!save) fail("нет сохранения");
save.handlers.click({}); await settle();
if (writes.length !== 1 || writes[0].base !== "s1" || writes[0].proposal.text !== "поправленная приписка" || writes[0].proposal.revision !== 2) fail("правка не доехала");
await openScreen("demo/draft/XR-9/prepared/p2");
fields = allByClass(groups, "dfield");
const reason = field("Причина отказа"); reason.value = "не относится"; reason.handlers.input({});
const reject = deepBtn(groups, "Отклонить"); if (!reject) fail("нет отказа");
reject.handlers.click({}); await settle();
if (writes.length !== 2 || writes[1].base !== "s2" || writes[1].proposal !== "p2" || writes[1].reason !== "не относится") fail("отказ не доехал");
await openScreen("demo/draft/XR-9");
rows = allByClass(groups, "draft-prepared-task");
if (!cardText(rows[1]).includes("Отклонено")) fail("отклонённое удаление не названо отклонённым");
console.log("подготовленные задачи правятся и отклоняются на своём экране");

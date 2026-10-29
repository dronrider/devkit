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
  if (path === "/api/projects/demo/drafts/XR-9/proposals" && init && init.method === "PUT") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[0] = body.proposal; snapshot = "s2"; return { message: "сохранено" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/proposals/reject" && init && init.method === "POST") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[1].status = "rejected"; state.proposals[1].result = { reason: body.reason }; return { message: "отклонено" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/proposals") return { snapshot, state };
  if (path === "/api/projects/demo/drafts/XR-9") return { id: "XR-9", text: "# XR-9: встреча\n", hash: "h", type: "task", source: "thought", lock: "unlocked" };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
sandbox.location.hash = "demo/draft/XR-9";
await sandbox.refresh(); await settle();
const groups = byId.get("groups");
let cards = allByClass(groups, "draft-proposal");
if (cards.length !== 2) fail("две карточки не показаны");
function field(card, label) {
  const wrap = allByClass(card, "dfield").find((n) => n.children[0] && n.children[0].textContent === label);
  if (!wrap) fail("нет поля " + label);
  return wrap.children[1];
}
const text = field(cards[0], "Текст результата");
text.value = "поправленная приписка"; text.handlers.input({});
const save = deepBtn(cards[0], "Сохранить предложение"); if (!save) fail("нет сохранения");
save.handlers.click({}); await settle();
if (writes.length !== 1 || writes[0].base !== "s1" || writes[0].proposal.text !== "поправленная приписка" || writes[0].proposal.revision !== 2) fail("правка не доехала");
cards = allByClass(groups, "draft-proposal");
const reason = field(cards[1], "Причина отказа"); reason.value = "не относится"; reason.handlers.input({});
const reject = deepBtn(cards[1], "Отклонить"); if (!reject) fail("нет отказа");
reject.handlers.click({}); await settle();
if (writes.length !== 2 || writes[1].base !== "s2" || writes[1].proposal !== "p2" || writes[1].reason !== "не относится") fail("отказ не доехал");
console.log("карточки правятся и отклоняются");

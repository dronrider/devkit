import assert from "node:assert/strict";
import { makeSandbox, settle, allByClass, byClass, tag, deepBtn, dump, appPathArg } from "./poc_dom.mjs";

const writes = [], constructors = [];
let snapshot = "s1";
const original = { id: "prepared-one", board_id: "XR-005", revision: 1, outcome: "add", title: "Доставка чата",
  board_type: "bug", rank: "25+5+3+5+4", size: "L", accept: "mixed", barrier: "глаза: проверить вид",
  due: "2026-11-15", control: "3", after: ["XR-1"], text: "# Доставка чата\n\nОписание доставки.\n", rationale: "сохранённый довод" };
const projectedText = original.text.trimEnd() + "\n\nБарьер приёмки: " + original.barrier + "\n";
const state = { version: 1, state: "active", pack_revision: 2, proposals: [structuredClone(original),
  { id: "drop-one", revision: 1, outcome: "drop", reason: "устарело", text: "" }, { ...original, id:"prepared-two",board_id:"XR-006",title:"Соседняя подготовленная задача" }] };
const row = { id: "XR-7", title: original.title, type: original.board_type, cost: original.size,
  r_parts: [25,5,3,5,4], p: "P2", sect: "backlog", section: "Backlog", due: original.due, control: 3 };
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path.endsWith("/drafts/XR-9/prepared") && init?.method === "PUT") {
    const body = JSON.parse(init.body); writes.push({ path, ...body }); state.proposals[0] = body.proposal;
    return { message: "задача сохранена" };
  }
  if (path.endsWith("/drafts/XR-9/prepared")) return { id: "XR-9", snapshot, basis: "b1", ready: true, state };
  if (path.endsWith("/drafts/XR-9")) return { id: "XR-9", text: "# Встреча по доставке\n", type: "task", source: "thought", lock: "unlocked" };
  if (path.includes("/tasks/") && init?.method) { writes.push({ path, method: init.method, body: JSON.parse(init.body) }); return {}; }
  if (path.endsWith("/tasks/XR-005")) return { row:{...row,id:"XR-005"},file:"docs/tasks/XR-005.md",text:original.text,after:[],blocks:[] };
  if (path.endsWith("/tasks/XR-7")) return { row, file: "docs/tasks/XR-7.md", text: original.text, after: [{ id: "XR-1" }], blocks: [] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [{key:"backlog",rows:[{id:"XR-2",title:"Вторая зависимость"}]}] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});
const sharedForm = sandbox.taskForm;
if (sharedForm) sandbox.taskForm = (cfg) => { constructors.push(cfg); return sharedForm(cfg); };
const groups = byId.get("groups");
async function open(hash) { sandbox.location.hash = hash; await sandbox.refresh(); await settle(); }
const due = () => allByClass(groups, "duein").find(n => n.type === "date");
const title = () => byClass(groups, "tedit");
const text = () => tag(byClass(groups, "fbody"), "TEXTAREA");
const save = () => deepBtn(groups, "Сохранить");
const pen = () => deepBtn(groups, "tpen");
const fields = () => ["tedit", "rcard", "duebox", "fbody", "dcard"].map(c => allByClass(groups, c).length);
await open("demo/XR-7");
const normalFields = fields();
assert.deepEqual(normalFields, [1,1,1,1,1]);
assert.ok(byClass(groups, "fview") && !byClass(groups, "fview").hidden);
await open("demo/draft/XR-9/prepared/prepared-one");
assert.deepEqual(fields(), normalFields, "normal/prepared показывают разные поля полной формы");
await open("demo/XR-7");
pen().handlers.click({}); title().value = "Не записывать"; title().handlers.input({});
assert.ok(save() && !save().hidden);
await deepBtn(groups, "Отменить правку").handlers.click({}); await settle();
assert.equal(title().value, original.title); assert.equal(writes.length, 0);
due().value = "2026-11-16"; due().handlers.input({});
await save().handlers.click({}); await settle();
assert.equal(writes[0].method, "PATCH"); assert.equal(writes[0].body.due, "2026-11-16");
await open("demo/draft/XR-9/prepared/prepared-one");
assert.deepEqual(fields(), normalFields, "normal/prepared показывают разные поля полной формы");
assert.ok(constructors.some(c => c.capabilities.work && c.capabilities.ownChat));
assert.ok(constructors.some(c => !c.capabilities.work && !c.capabilities.ownChat && c.localDeps));
assert.equal(dump(byClass(groups, "idbig")), "XR-005");
assert.ok(dump(groups).includes("из XR-9: Встреча по доставке"));
assert.equal(dump(byClass(groups, "rbig")).includes("42"), true);
assert.equal(due().value, original.due); assert.equal(byClass(groups, "ctrl").value, "3");
for (const cls of ["pedit", "pshort", "parea", "darm"]) assert.equal(allByClass(groups, cls).length, 0, cls);
for (const label of ["Исход", "Вернуться до", "Довод разбора", "Причина", "Свойства задачи", "Выполнить", "Продолжить", "Начать разговор"]) {
  assert.equal(Boolean(deepBtn(groups, label)), false, label);
}
assert.equal(groups.querySelectorAll("button").some(n => String(n.attrs?.["aria-label"] || "").startsWith("Чат по задаче")), false);
assert.ok(!save() || save().hidden);
pen().handlers.click({});
assert.ok(!save() || save().hidden, "карандаш создал фиктивную правку");
assert.equal(byClass(groups, "rcard").classList.contains("redit"), true);
assert.equal(text().hidden, false);
text().value += "Отменить"; text().handlers.input({});
await deepBtn(groups, "Отменить правку").handlers.click({}); await settle();
assert.equal(text().value, projectedText); assert.ok(!save() || save().hidden);
pen().handlers.click({});
text().value += "Поправка"; text().handlers.input({});
byClass(groups, "ctrl").value = "5"; byClass(groups, "ctrl").handlers.input({});
const remove = deepBtn(byClass(groups, "dcard"), "Снять"); assert.ok(remove);
remove.handlers.click({ stopPropagation() {} });
assert.ok(!dump(byClass(groups, "dcard")).includes("XR-1"));
await settle();
const candidate = allByClass(groups, "tprow").find(n => dump(n).includes("XR-2")); assert.ok(candidate);
candidate.handlers.click({ stopPropagation() {} });
assert.ok(dump(byClass(groups, "dcard")).includes("XR-2"));
await settle();
const sibling = allByClass(groups, "tprow").find(n => dump(n).includes("XR-006")); assert.ok(sibling);
sibling.handlers.click({ stopPropagation() {} });
assert.equal(allByClass(groups, "tprow").some(n => dump(n).includes("drop-one")), false);
const pendingText = text().value;
await sandbox.refresh(); await settle(); assert.equal(text().value, pendingText, "refresh затёр правку подготовленной задачи");
await save().handlers.click({}); await settle();
const put = writes.find(w => w.proposal);
assert.equal(put.base, "s1"); assert.equal(put.proposal.revision, 2);
assert.equal(put.proposal.text, projectedText + "Поправка"); assert.equal(put.proposal.control, "5");
assert.deepEqual(put.proposal.after, ["XR-2", "XR-006"]);
assert.equal(put.proposal.rationale, original.rationale); assert.equal(put.proposal.barrier, "глаза");
assert.deepEqual(state.proposals[1], { id: "drop-one", revision: 1, outcome: "drop", reason: "устарело", text: "" });
assert.equal(writes.some(w => w.path.includes("/tasks/XR-005")), false, "зависимости prepared ушли на ручку задачи");
assert.ok(!save() || save().hidden); assert.equal(byClass(groups, "fview").hidden, false);
const boardDependency = allByClass(groups,"drow").find(n=>dump(n).includes("XR-2")); assert.ok(boardDependency);
boardDependency.handlers.click({}); await settle();
assert.equal(sandbox.location.hash.replace(/^#/,""),"demo/XR-2","зависимость доски потеряла обычную дорогу");
await open("demo/draft/XR-9/prepared/prepared-one");
const siblingDependency = allByClass(groups,"drow").find(n=>dump(n).includes("XR-006")); assert.ok(siblingDependency);
siblingDependency.handlers.click({}); await settle();
assert.equal(sandbox.location.hash.replace(/^#/,""),"demo/draft/XR-9/prepared/prepared-two","зависимость prepared не открывает подготовленную задачу");
state.proposals[2].status = "accepted"; state.proposals[2].result = {outcome:"add",ids:["XR-006"]}; snapshot = "s2";
await open("demo/draft/XR-9/prepared/prepared-one");
allByClass(groups,"drow").find(n=>dump(n).includes("XR-006")).handlers.click({}); await settle();
assert.equal(sandbox.location.hash.replace(/^#/,""),"demo/XR-006","принятая зависимость не ведёт на доску");
delete state.proposals[2].status; delete state.proposals[2].result;
state.proposals[0].after = ["prepared-two"]; snapshot = "s3";
await open("demo/draft/XR-9/prepared/prepared-one");
const legacyDependency = allByClass(groups,"drow").find(n=>dump(n).includes("XR-006")); assert.ok(legacyDependency);
legacyDependency.handlers.click({}); await settle();
assert.equal(sandbox.location.hash.replace(/^#/,""),"demo/draft/XR-9/prepared/prepared-two","legacy key зависимости не ведёт на prepared");
state.proposals[0].status = "accepted"; state.proposals[0].result = {outcome:"add",ids:["XR-005"]};
await open("demo/draft/XR-9/prepared/prepared-one");
assert.equal(sandbox.location.hash.replace(/^#/,""), "demo/XR-005", "ссылка принятой add не ведёт к задаче на доске");
assert.equal(constructors.at(-1).capabilities.work,true);
assert.equal(constructors.at(-1).capabilities.ownChat,true);
console.log("normal/prepared используют общую полную форму, правка, отмена, срок и локальные зависимости работают");

import assert from "node:assert/strict";
import { makeSandbox, settle, deepBtn, dump, allByClass, byClass, tag, appPathArg } from "./poc_dom.mjs";

const writes = [], savedTasks = [];
let failConfirm = true, ready = true, reason = "";
let releaseConfirm;
const source = "demo/draft/XR-9";
const start = source + "/chat/XR-9";
const state = { version: 1, state: "active", pack_revision: 7, proposals: [
  { id: "first", board_id: "XR-099", revision: 2, outcome: "add", title: "Доставка", text: "# Доставка\n\nОписание", board_type: "bug", rank: "50+8+3+5+3", size: "L", accept: "mixed", barrier: "глаза: проверить вид" },
  { id: "last", board_id: "XR-100", revision: 3, outcome: "drop", title: "Лишнее", text: "Лишнее", reason: "устарело" },
] };
const view = () => ({ conclusion: "Удалить лишнее: устарело. Создать задачу доставки.", id: "XR-9", snapshot: "s1", basis: "b1", ready, reason, state });
const { sandbox, byId } = makeSandbox(appPathArg(), async (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/projects/demo/drafts/XR-9/prepared" && init && init.method === "PUT") {
    const body = JSON.parse(init.body); savedTasks.push(body);
    state.proposals[0] = body.proposal;
    state.pack_revision = 1; state.prepared_source = "checked-source";
    ready = true; reason = "";
    return view();
  }
  if (path === "/api/projects/demo/drafts/prepared") return { views: [view()] };
  if (path === "/api/projects/demo/drafts/XR-9/prepared") return view();
  if (path === "/api/projects/demo/drafts/XR-9") return { id: "XR-9", file: "docs/tasks/drafts/XR-9.md", text: "# Исходник\n\nМатериал", type: "task", source: "thought", lock: "unlocked", groomed: true };
  if (path === "/api/projects/demo/drafts") return { drafts: state.state === "history" ? [] : [{ id: "XR-9", title: "Исходник", type: "task", prio: "mid" }] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.endsWith("/drafts/prepared/confirm") && init && init.method === "POST") {
    writes.push(JSON.parse(init.body));
    if (releaseConfirm) await new Promise((resolve) => { releaseConfirm = resolve; });
    if (failConfirm) return { raw: { status: 400, text: JSON.stringify({ error: "последняя задача требует правки" }) } };
    state.state = "history";
    for (const task of state.proposals) { task.status = "accepted"; task.result = { ids: task.outcome === "add" ? ["XR-099"] : [], outcome: task.outcome }; }
    return { message: "разбор подтверждён", results: [{ ids: ["XR-099"] }, { ids: [] }] };
  }
  return {};
});
async function open(hash) { sandbox.location.hash = hash; await sandbox.refresh(); await settle(); }
const groups = byId.get("groups");
await open("demo/drafts");
const prepared = allByClass(groups, "tsec")[0];
const rows = allByClass(prepared, "dsrow");
assert.equal(rows.length, 1, "согласование должно показывать одну строку черновика");
assert.equal(allByClass(groups, "dsrow").length, 1, "исходник повторяется в секциях списка");
assert.equal(rows[0].children.length, 8);
assert.equal(allByClass(rows[0], "dpick").length, 0, "в согласовании остался выбор задач");
assert.ok(dump(rows[0]).includes("XR-9") && dump(rows[0]).includes("1 задача"));
assert.equal(deepBtn(groups, "Подтвердить выбранные"), null);
rows[0].handlers.click({});
assert.ok(sandbox.location.hash.includes("draft/XR-9") && !sandbox.location.hash.includes("prepared/"));
await open(start);
const comp = byClass(groups, "comp"), body = byClass(groups, "fbody");
assert.ok(comp && body);
let accept = deepBtn(groups, "Подтвердить разбор");
assert.ok(accept && !accept.disabled);
await accept.handlers.click({}); await settle();
assert.equal(writes.length, 1);
assert.equal(writes[0].decisions.length, 2);
assert.deepEqual(writes[0].snapshots, [{ source: "XR-9", digest: "b1", revision: 7 }]);
assert.equal(sandbox.location.hash, start, "ошибка увела с формы исходника");
assert.ok(dump(byId.get("flashes")).includes("последняя задача требует правки"));
assert.equal(state.state, "active");
await sandbox.refresh(); await settle();
assert.ok(dump(byId.get("flashes")).includes("последняя задача требует правки"));
await open(source + "/prepared/first/chat/XR-9");
assert.equal(deepBtn(groups, "Подтвердить"), null);
assert.equal(deepBtn(groups, "Отклонить"), null);
const pen = deepBtn(groups, "tpen"); assert.ok(pen); pen.handlers.click({});
assert.ok(!deepBtn(groups, "Сохранить") || deepBtn(groups, "Сохранить").hidden, "готовый разбор требует сохранения без правки");
pen.handlers.click({});
ready = false; reason = "Появились неразобранные мысли";
await open(start);
accept = deepBtn(groups, "Подтвердить разбор");
assert.ok(accept.disabled && dump(groups).includes(reason));
await accept.handlers.click({}); assert.equal(writes.length, 1);
ready = true; reason = "";
await open(start);
accept = deepBtn(groups, "Подтвердить разбор");
deepBtn(groups, "tpen").handlers.click({});
const text = tag(byClass(groups, "fbody"), "TEXTAREA");
text.value += "\nПравка"; text.handlers.input({});
assert.ok(accept.disabled && dump(groups).includes("Сначала сохраните правку"));
await accept.handlers.click({}); assert.equal(writes.length, 1);
await sandbox.renderDraft("demo", [{ id: "XR-9", live: "busy", via: "tmux" }], "XR-9"); await settle();
// Несохранённая правка не теряется при обновлении.
assert.ok(accept.disabled);
text.value = "# Исходник\n\nМатериал"; text.handlers.input({});
assert.ok(!accept.disabled);
await sandbox.renderDraft("demo", [{ id: "XR-9", via: "tmux", live: "busy" }], "XR-9"); await settle();
accept = deepBtn(groups, "Подтвердить разбор");
assert.ok(accept.disabled);
assert.ok(!dump(groups).includes("Сначала завершите разбор"));
await accept.handlers.click({}); assert.equal(writes.length, 1);
await sandbox.renderDraft("demo", [], "XR-9"); await settle();
accept = deepBtn(groups, "Подтвердить разбор");
failConfirm = false; releaseConfirm = true;
const confirming = accept.handlers.click({}); await settle();
assert.ok(accept.disabled && dump(groups).includes("Подтверждение разбора идёт"));
await accept.handlers.click({}); assert.equal(writes.length, 2, "повторное нажатие отправило второй запрос");
releaseConfirm(); await confirming; await settle();
assert.equal(sandbox.location.hash.replace(/^#/, ""), "demo/drafts/chat/XR-9");
sandbox.window.fire("hashchange"); await settle();
assert.ok(dump(byId.get("flashes")).includes("XR-099"));
assert.equal(allByClass(byId.get("groups"), "tsec").length, 0);
await open(source);
assert.ok(dump(groups).includes("История разбора"));
assert.equal(deepBtn(groups, "Подтвердить разбор"), null);
assert.equal(deepBtn(groups, "Подготовить вручную"), null);
assert.ok(allByClass(groups, "srow").some((n) => dump(n).includes("XR-099")));
assert.equal(await sandbox.confirmDraftPrepared("/api/projects/demo", []), false);
// Сервер уже рассчитал готовность legacy без записи и фиктивного Save.
state.state = "active";
for (const task of state.proposals) { delete task.status; delete task.result; }
state.pack_revision = 1; state.prepared_source = "effective-legacy-source";
ready = true; reason = ""; releaseConfirm = null;
await open(source);
const reviewedConfirm = deepBtn(groups, "Подтвердить разбор");
assert.ok(reviewedConfirm && !reviewedConfirm.disabled);
assert.ok(dump(groups).includes("Удалить лишнее: устарело"));
assert.equal(allByClass(groups, "srow").length, 1);
await reviewedConfirm.handlers.click({}); await settle();
assert.equal(savedTasks.length, 0, "legacy потребовал сохранить подготовленную задачу");
assert.equal(writes.length, 3); assert.equal(writes[2].decisions.length, 2);
assert.equal(writes[2].snapshots[0].revision, 1); assert.equal(state.state, "history");
console.log("весь разбор подтверждается с исходника, готовый legacy согласуется без Save");

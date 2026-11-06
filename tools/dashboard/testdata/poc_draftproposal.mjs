import assert from "node:assert/strict";
import { makeSandbox, settle, allByClass, deepBtn, dump, appPathArg } from "./poc_dom.mjs";
const writes = [];
const conclusion = "Удалить устаревший фрагмент: нет нужды.\n\nПриписать описание к XR-1.\n\nОтложить обсуждение до встречи.";
const state = { version: 1, state: "active", pack_revision: 1, proposals: [
  { id: "p1", board_id: "XR-010", revision: 1, outcome: "attach", target: "XR-1", text: "приписка" },
  { id: "p2", board_id: "XR-011", revision: 1, outcome: "drop", reason: "нет нужды" },
  { id: "p3", board_id: "XR-012", revision: 1, outcome: "defer", until: "2026-12-01", reason: "до встречи" },
] };
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (init?.method) writes.push({path,method:init.method});
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/drafts/XR-9/prepared")) return { id: "XR-9", snapshot: "s1", basis: "b1", ready: true, conclusion, state };
  if (path.endsWith("/drafts/XR-9")) return { id: "XR-9", text: "# Встреча\n", type: "task", source: "thought", lock: "unlocked" };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});
async function open(hash) { sandbox.location.hash = hash; await sandbox.refresh(); await settle(); }
const groups = byId.get("groups");
await open("demo/draft/XR-9");
assert.ok(dump(groups).includes("Заключение разбора") && dump(groups).includes("Удалить устаревший фрагмент"));
assert.equal(allByClass(groups, "srow").length, 0, "внутренние операции показаны задачами");
for (const id of ["XR-010","XR-011","XR-012"]) assert.equal(dump(groups).includes(id), false, id);
assert.ok(deepBtn(groups, "Подтвердить разбор") && !deepBtn(groups, "Подтвердить разбор").disabled);
for (const key of ["p1", "p2", "p3"]) {
  await open("demo/draft/XR-9/prepared/" + key);
  assert.equal(sandbox.location.hash.replace(/^#/, ""), "demo/draft/XR-9", "старая ссылка операции не ведёт на заключение");
  assert.equal(allByClass(groups, "pedit").length, 0);
  assert.ok(dump(groups).includes("Заключение разбора"));
}
assert.equal(writes.length, 0, "переходы записали данные");
console.log("внутренние операции читаются заключением, старые адреса ведут к исходнику");

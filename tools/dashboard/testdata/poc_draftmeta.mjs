import { makeSandbox, settle, allByClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const writes = [];
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects/demo/drafts/types") return { types: [
    { name: "task", title: "Задача" }, { name: "milestone", title: "Веха" },
  ] };
  if (path === "/api/projects/demo/drafts/XR-9/meta" && init && init.method === "PUT") {
    writes.push(JSON.parse(init.body));
    return { message: "поля сохранены" };
  }
  if (path === "/api/projects/demo/drafts/XR-9") return {
    id: "XR-9", file: "docs/tasks/drafts/XR-9.md", text: "# XR-9: встреча\n", hash: "base",
    type: "task", source: "thought", lock: "unlocked", order: "Разобрать",
  };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
sandbox.location.hash = "demo/draft/XR-9";
await sandbox.refresh();
await settle();
const groups = byId.get("groups");
const box = allByClass(groups, "draft-meta")[0];
if (!box) fail("поля записи не показаны");
const picks = allByClass(box, "pick");
if (picks.length !== 3) fail("жду три поля записи");
const selects = picks.map((p) => p.children.find((n) => n.tagName === "SELECT"));
if (selects.some((s) => !s)) fail("поле выбора не найдено");
selects[0].value = "milestone";
selects[1].value = "meeting";
selects[2].value = "locked";
const save = deepBtn(box, "Сохранить поля");
if (!save) fail("кнопки сохранения полей нет");
save.handlers.click({});
await settle();
if (writes.length !== 1 || writes[0].type !== "milestone" || writes[0].source !== "meeting" || writes[0].lock !== "locked") fail("поля не доехали до сервера: " + JSON.stringify(writes));
console.log("поля записи сохранены");

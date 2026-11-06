import { makeSandbox, settle, deepBtn, dump, allByClass, appPathArg } from "./poc_dom.mjs";

const writes = [];
let failConfirm = true;
const source = "demo/draft/XR-9";
const start = source + "/prepared/chat-delivery/chat/XR-9";
const state = { version: 1, state: "active", proposals: [
  { id: "chat-delivery", board_id: "XR-099", revision: 1, outcome: "add", title: "Доставка", text: "# Доставка\n\nОписание",
    board_type: "bug", rank: "50+8+3+5+3", size: "L", accept: "mixed", barrier: "глаза: проверить вид" },
] };
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/projects/demo/drafts/XR-9/prepared") return { id: "XR-9", snapshot: "s1", basis: "b1", state };
  if (path === "/api/projects/demo/drafts/XR-9") return { id: "XR-9", text: "# Исходник", type: "task", source: "thought", lock: "unlocked" };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.endsWith("/drafts/prepared/confirm") && init && init.method === "POST") {
    writes.push(JSON.parse(init.body));
    if (failConfirm) return { raw: { status: 400, text: JSON.stringify({ error: "барьер требует правки" }) } };
    state.proposals[0].status = "accepted";
    state.proposals[0].result = { ids: ["XR-099"], outcome: "add" };
    return { message: "подтверждённые задачи применены", results: [{ ids: ["XR-099"] }] };
  }
  return {};
});
function check(ok, text) { if (!ok) throw new Error(text); }
sandbox.location.hash = start;
await sandbox.refresh(); await settle();
const groups = byId.get("groups");
const accept = deepBtn(groups, "Подтвердить");
check(accept, "вложенный адрес не открыл подготовленную задачу");
await accept.handlers.click({}); await settle();
check(writes.length === 1 && writes[0].decisions[0].proposal === "chat-delivery" && writes[0].decisions[0].revision === 1, "отправлена другая задача");
check(sandbox.location.hash === start, "после HTTP400 экран ушёл к исходнику");
check(dump(byId.get("flashes")).includes("барьер требует правки"), "ошибка HTTP400 не видна");
await sandbox.refresh(); await settle();
check(dump(byId.get("flashes")).includes("барьер требует правки"), "обновление скрыло причину отказа");
// Правка старой карточки предлагает ключ списком, пояснение остаётся в описании.
const pen = deepBtn(groups, "tpen"); check(pen, "нет правки"); pen.handlers.click({});
const picks = allByClass(groups, "pick");
const barrier = picks.find((p) => dump(p.children[0]) === "Барьер приёмки");
check(barrier, "нет закрытого выбора барьера");
const select = barrier.querySelector("select");
check(select && select.children.some((o) => o.selected && o.value === "глаза"), "старый барьер не разобран на ключ");
check(select.children.every((o) => ["", "глаза", "доступ", "необратимость", "секрет", "согласие", "событие"].includes(o.value)), "выбор допускает произвольный барьер");
const textarea = allByClass(groups, "fbody")[0].querySelector("textarea");
check(textarea && textarea.value.includes("Барьер приёмки: глаза: проверить вид"), "правка потеряла пояснение барьера");
pen.handlers.click({});
failConfirm = false;
await deepBtn(groups, "Подтвердить").handlers.click({}); await settle();
check(sandbox.location.hash.replace(/^#/, "") === source + "/chat/XR-9", "успех не вернул к исходнику с чатом");
sandbox.window.fire("hashchange"); await settle();
check(dump(byId.get("flashes")).includes("XR-099"), "результат успеха потерял закреплённый ID при переходе");
const noSelection = await sandbox.confirmDraftPrepared("/api/projects/demo", [], null);
check(noSelection === false, "пустой выбор не вернул false");
const failed = await sandbox.confirmDraftPrepared("/api/projects/demo", [{ id: "XR-9", basis: "b1", state: { proposals: [{ id: "other", revision: 1 }] } }]);
check(failed === true, "успех массового вызова не вернул true");
console.log("ошибка подтверждения остаётся на задаче, успех возвращает к исходнику с результатом");

import { makeSandbox, settle, allByClass, byClass, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

// Экран подготовленной задачи после провала приёмки 2026-11-05: открывается
// чтением по стандарту форм DK-1438, описание собрано разметкой, короткие
// свойства чипами, длинные тексты блоками, правка включается карандашом и
// несёт рамки с подписями в грани. Согласование стоит кнопками в командной
// панели и уезжает на время правки.
const writes = [];
const state = { version: 1, state: "active", analysis: "ok", proposals: [
  { id: "chat-delivery", revision: 1, outcome: "add", title: "Доставка чата записи",
    board_type: "bug", rank: "2", size: "L", accept: "mixed",
    barrier: "прогон стенда и живой экран", rationale: "дефект виден на приёмке",
    text: "# Доставка чата записи\n\nРеплика уезжает в чат записи.\n\n- пункт один\n- пункт два\n" },
] };
let snapshot = "s1";
const { sandbox, byId } = makeSandbox(appPathArg(), (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path === "/api/projects/demo/drafts/prepared/confirm" && init && init.method === "POST") {
    const body = JSON.parse(init.body); writes.push({ confirm: body });
    state.proposals[0].status = "accepted";
    state.proposals[0].result = { ids: ["XR-1"], outcome: "add", reason: "задача на доске" };
    return { results: [{ ids: ["XR-1"] }] };
  }
  if (path === "/api/projects/demo/drafts/XR-9/prepared" && init && init.method === "PUT") {
    const body = JSON.parse(init.body); writes.push(body); state.proposals[0] = body.proposal; snapshot = "s2";
    return { message: "задача сохранена" };
  }
  if (path === "/api/projects/demo/drafts/XR-9/prepared") return { id: "XR-9", snapshot, basis: "d1", state };
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
function chipText(node) {
  return allByClass(node, "chip").map(dump).join(" | ");
}
function legendField(root, label) {
  return allByClass(root, "dfield").find((n) => n.children[0] && n.children[0].textContent === label);
}
await openScreen("demo/draft/XR-9/prepared/chat-delivery");
const groups = byId.get("groups");
// Чтение по умолчанию: описание собрано разметкой, поля правки спрятаны.
const view = byClass(groups, "fview");
if (!view || view.hidden) fail("описание не показано разметкой в чтении");
if (!dump(view).includes("Реплика уезжает в чат записи") || !dump(view).includes("пункт один")) fail("текст описания не собран в чтении");
const editBox = allByClass(groups, "pedit")[0];
if (!editBox || !editBox.hidden) fail("поля правки не спрятаны в чтении");
const chipsLine = chipText(groups);
for (const part of ["Подготовлена", "тип bug", "цена L", "приёмка mixed"]) {
  if (!chipsLine.includes(part)) fail("в чтении нет чипа " + part);
}
const notes = byClass(groups, "pnotes");
if (!notes || notes.hidden || !dump(notes).includes("Барьер приёмки") || !dump(notes).includes("прогон стенда и живой экран")) fail("длинные тексты не стоят блоками в чтении");
if (!dump(notes).includes("Довод разбора")) fail("довод разбора не показан в чтении");
const accept = deepBtn(groups, "Подтвердить"); if (!accept || accept.hidden) fail("нет подтверждения в чтении");
const reject = deepBtn(groups, "Отклонить"); if (!reject || reject.hidden) fail("нет отказа в чтении");
// Выход «Не заводить» живёт только на форме заведения: у сохранённой задачи
// его роль играет отмена правки, и кнопка не держит пустую полосу в чтении.
if (deepBtn(groups, "Не заводить")) fail("в чтении сохранённой задачи есть кнопка выхода");
// Правка включается карандашом и несёт рамки с подписями в грани.
const pen = deepBtn(groups, "tpen"); if (!pen) fail("нет карандаша правки");
pen.handlers.click({});
if (editBox.hidden) fail("правка не открылась карандашом");
if (!legendField(groups, "Свойства задачи")) fail("нет рамки свойств задачи");
if (!legendField(groups, "Довод разбора")) fail("нет рамки довода разбора");
const text = tag(byClass(groups, "fbody"), "TEXTAREA");
if (!text || text.hidden) fail("описание не правится большим полем");
if (accept.hidden !== true) fail("согласование не уехало на время правки");
// Повторный карандаш возвращает чтение, и согласование встаёт на место.
pen.handlers.click({});
if (!editBox.hidden) fail("правка не закрылась повторным карандашом");
const backView = byClass(groups, "fview");
if (!backView || backView.hidden) fail("после выхода из правки описание не вернулось в чтение");
if (accept.hidden) fail("после выхода из правки согласование не вернулось");
pen.handlers.click({});
if (editBox.hidden) fail("правка не открылась повторно");
text.value = "Реплика уезжает в чат записи. Поправка разбора."; text.handlers.input({});
const save = deepBtn(groups, "Сохранить"); if (!save) fail("нет сохранения правки");
save.handlers.click({}); await settle();
const put = writes.find((w) => !w.confirm);
if (!put || put.base !== "s1" || put.proposal.revision !== 2 || put.proposal.text !== "Реплика уезжает в чат записи. Поправка разбора.") fail("правка не доехала");
// После сохранения экран возвращается в чтение.
await openScreen("demo/draft/XR-9/prepared/chat-delivery");
const back = allByClass(byId.get("groups"), "pedit")[0];
if (!back || !back.hidden) fail("после сохранения экран не вернулся в чтение");
// Подтверждение уезжает на сервер решением по этой задаче.
const again = deepBtn(byId.get("groups"), "Подтвердить"); if (!again) fail("нет подтверждения после правки");
again.handlers.click({}); await settle();
const confirm = writes.find((w) => w.confirm);
if (!confirm || confirm.confirm.decisions.length !== 1 || confirm.confirm.decisions[0].proposal !== "chat-delivery" || confirm.confirm.decisions[0].action !== "accept") fail("подтверждение не доехало");
console.log("экран подготовленной задачи читается и правится по стандарту форм");

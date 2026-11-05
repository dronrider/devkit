import { makeSandbox, settle, allByClass, byClass, tag, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const writes = [];
const state = { version: 1, state: "active", analysis: "ok", proposals: [
  { id: "p1", board_id: "XR-010", revision: 1, outcome: "attach", target: "XR-1", title: "первая приписка", text: "первая приписка" },
  { id: "p2", board_id: "XR-011", revision: 1, outcome: "drop", title: "старый фрагмент", text: "старый фрагмент", reason: "нет нужды" },
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
async function openScreen(hash) {
  sandbox.location.hash = hash;
  await sandbox.refresh(); await settle();
}
await openScreen("demo/draft/XR-9");
const groups = byId.get("groups");
// Состав задач разбора стоит над описанием карточкой со строками (DK-1564,
// утверждённый макет): номер, название и состояние тем же срезом строки, что
// задачи цели. Человек читает номер доски, ссылки остаются по ключу.
const comp = allByClass(groups, "comp").find((n) => cardText(n).includes("Задачи разбора"));
if (!comp) fail("на экране записи нет карточки состава задач разбора");
let rows = allByClass(comp, "srow");
if (rows.length !== 2) fail("две подготовленные задачи не показаны строками состава");
if (!cardText(rows[0]).includes("XR-010") || !cardText(rows[0]).includes("первая приписка")) fail("строка приписки не названа номером и названием");
// Нерешённая задача названа «На согласовании» тем же чипом ожидания, что
// таблица и шапка экрана задачи (DK-1564, замер QA 2026-11-06).
if (!cardText(rows[0]).includes("На согласовании")) fail("приписка до решения не названа ожидающей согласования");
if (!cardText(rows[1]).includes("XR-011") || !cardText(rows[1]).includes("На согласовании")) fail("удаление до решения не названо номером и ожидающим согласования");
if (!deepBtn(comp, "Подготовить вручную")) fail("в составе нет ручной подготовки");
// Правка и отказ живут на экране самой задачи, куда ведёт строка списка
// (DK-1469, решение 4). Экран открывается чтением, правка включается
// карандашом, поэтому до правки поля недоступны.
await openScreen("demo/draft/XR-9/prepared/p1");
const pen = deepBtn(groups, "tpen"); if (!pen) fail("нет карандаша правки");
pen.handlers.click({});
const text = tag(byClass(groups, "fbody"), "TEXTAREA"); if (!text) fail("нет поля описания");
text.value = "поправленная приписка"; text.handlers.input({});
const save = deepBtn(groups, "Сохранить"); if (!save) fail("нет сохранения");
save.handlers.click({}); await settle();
if (writes.length !== 1 || writes[0].base !== "s1" || writes[0].proposal.text !== "поправленная приписка" || writes[0].proposal.revision !== 2) fail("правка не доехала");
await openScreen("demo/draft/XR-9/prepared/p2");
// Отказ спрашивает причину на месте: карточка причины встаёт по кнопке отказа.
const reject = deepBtn(groups, "Отклонить"); if (!reject) fail("нет отказа");
reject.handlers.click({});
const reason = byAria(groups, "причина отказа"); if (!reason) fail("нет поля причины отказа");
reason.value = "не относится";
const rejectGo = deepBtn(groups, "Отклонить задачу"); if (!rejectGo) fail("нет кнопки отказа");
rejectGo.handlers.click({}); await settle();
if (writes.length !== 2 || writes[1].base !== "s2" || writes[1].proposal !== "p2" || writes[1].reason !== "не относится") fail("отказ не доехал");
await openScreen("demo/draft/XR-9");
// Решённая задача сворачивается из состава: актуальные остаются на виду, а
// отклонённое читается за разворотом строки «Ещё N решённых».
const after = allByClass(byId.get("groups"), "comp").find((n) => cardText(n).includes("Задачи разбора"));
// Решённые стоят в DOM и за свёрнутым ящиком: видимость строки решает её
// родитель, а не присутствие в дереве.
const live = allByClass(after, "srow").filter((n) => !(n.parentNode && n.parentNode.hidden));
if (live.length !== 1 || !cardText(live[0]).includes("XR-010")) fail("решённая задача не ушла из вида состава");
const fold = allByClass(after, "more")[0];
if (!fold || !cardText(fold).includes("решённая задача")) fail("нет разворота решённых задач");
fold.handlers.click({});
rows = allByClass(after, "srow");
if (rows.length !== 2 || !cardText(rows[1]).includes("Отклонено")) fail("отклонённое удаление не названо отклонённым за разворотом");
console.log("подготовленные задачи правятся и отклоняются на своём экране");

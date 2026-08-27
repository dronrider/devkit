// Стенд порядка секций доски (POC DK-397, ветка poc-chat).
//
// Blocked стоял ниже Backlog, и припаркованная задача пряталась под очередью в
// сотню строк: «Blocked стоит после Backlog, а задачи там требуют внимания»
// (замечание пользователя). Порядок секций задаёт сам экран (SECTION_ORDER), а
// не доска, и сторожится тут именно он: секция, требующая человека, стоит
// раньше той, которую разбирают по очереди.
//
// Зовётся: node testdata/poc_secorder.mjs static/app.js

import { makeSandbox, settle, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const board = () => ({ prefix: "XR", sections: [
  // Порядок с сервера нарочно перепутан: экран раскладывает секции своим
  // порядком, и зависеть от того, чем их отдала доска, он не должен.
  { key: "backlog", title: "Backlog", rows: [
    { id: "XR-300", title: "очередь", sect: "backlog", r: 40, r_parts: [20, 5, 5, 5, 5] }] },
  { key: "blocked", title: "Blocked", rows: [
    { id: "XR-200", title: "парковка", sect: "blocked", block: "ждёт ответа смежника" }] },
  { key: "check", title: "Check", rows: [
    { id: "XR-100", title: "на приёмке", sect: "check" }] },
  { key: "in-progress", title: "In progress", rows: [
    { id: "XR-400", title: "идёт", sect: "in-progress", run: "tmux" }] },
] });

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR" }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: board(), works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

const titles = allByClass(groups, "shead").map((n) => (n.textContent || "").trim());
if (!titles.length) fail("секции не собрались вовсе");

// Счётчик секции приезжает тем же узлом, что и заголовок: имя берётся первым
// словом, дальше идёт число строк.
const names = titles.map((t) => t.split(/\s+/)[0]);
const want = ["In", "Check", "Blocked", "Backlog"];
if (names.join(",") !== want.join(",")) {
  fail("порядок секций " + names.join(",") + ", ждали " + want.join(","));
}

const at = (name) => names.indexOf(name);
if (at("Blocked") > at("Backlog")) {
  fail("Blocked стоит ниже Backlog: припаркованная задача прячется под очередью");
}

console.log("порядок секций доски: " + names.join(" -> "));

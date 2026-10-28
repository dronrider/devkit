import fs from "node:fs";
import { makeSandbox, settle, byClass, allByClass, deepBtn, fail } from "./poc_dom.mjs";

const wire = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
const sid = "aa144600-1111-4111-8111-111111111111";
let ask = wire;
const sent = [];
const { sandbox, timers, store } = makeSandbox(process.argv[2], (path, init) => {
  if (path.includes("/say") && init?.method === "POST") {
    sent.push(JSON.parse(init.body));
    return { way: "ask" };
  }
  if (path.includes("/ask")) return { ask };
  if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});
const scenario = wire.scenario || wire.steps.find((s) => s.scenario).scenario;
const panel = sandbox.chatPanel("demo", { project: "demo", addr: sid, sid, task: "XR-4",
  chats: [{ id: sid, project: "demo", state: "live", idle: true }], models: [] });
await settle();
const ta = byClass(panel, "csay");
const marks = () => allByClass(byClass(panel, "chatfeed"), "caskpick");
const click = (n) => n.handlers.click({ stopPropagation() {} });
if (marks().length < 3) fail("нет галочек нового списка: " + marks().length);
if (sent.length || ta.value) fail("показ рекомендации отправил или подставил ответ");
if (marks().length > 3) {
  click(marks()[3]);
  if (ta.value !== "срок 1") fail("обычная отметка ответила за сценарии: " + ta.value);
  click(marks()[3]);
  if (ta.value) fail("обычная снятая отметка оставила ответ: " + ta.value);
}
const own = String.raw`плюс "свой, с \"кавычкой\" и \\ путём"`;
ta.value = own;
ta.handlers.input();
click(marks()[0]);
click(marks()[2]);
click(marks()[0]);
if (ta.value !== "сценарии СЦ-3; " + own) fail("свой текст/ID потерян: " + ta.value);
ta.value = "  " + ta.value;
ta.handlers.input();
click(marks()[0]);
click(marks()[0]);
if (ta.value !== "  сценарии СЦ-3; " + own) fail("правка перед машинной частью потеряла текст: " + ta.value);
if (sent.length) fail("галочка отправила ответ");
if (marks().length > 3) click(marks()[3]);
const draft = ta.value;
ask = structuredClone(wire);
const next = ask.scenario || ask.steps.find((s) => s.scenario).scenario;
next.snapshot = "sc1-" + "0".repeat(64);
const poll = timers.find((timer) => timer.ms === 3000);
if (!poll) fail("нет опроса");
poll.fn();
await settle();
if (ta.value !== draft) fail("опрос изменил черновик");
const saved = JSON.parse(store.get("devkit.askdraft." + sid));
if (saved.answer.snapshot !== scenario.snapshot) fail("черновик получил новый снимок");
const originalFetch = sandbox.fetch;
let lost = true;
sandbox.fetch = (path, init) => {
  if (lost && init?.method === "POST" && path.endsWith("/say")) {
    lost = false;
    sent.push(JSON.parse(init.body));
    return Promise.reject(new Error("обрыв сети стенда"));
  }
  return originalFetch(path, init);
};
click(deepBtn(panel, "Отправить"));
await settle();
if (sent.length !== 1 || sent[0].text !== draft || sent[0].scenario.snapshot !== scenario.snapshot || sent[0].scenario.tree !== wire.tree) fail("отправка потеряла raw/снимок/маршрут: " + JSON.stringify(sent));
const loaded = sandbox.echoRead("demo", sid);
if (loaded.length !== 1 || loaded[0].scenario.snapshot !== scenario.snapshot || loaded[0].text !== draft) fail("очередь потеряла снимок/raw");
const resent = [];
const again = makeSandbox(process.argv[2], (path, init) => {
  if (path.endsWith("/say") && init?.method === "POST") { resent.push(JSON.parse(init.body)); return { way: "ask" }; }
  if (path.endsWith("/ask")) return { ask };
  if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
}, { store: Object.fromEntries(store) });
again.sandbox.chatPanel("demo", { project: "demo", addr: sid, sid, task: "XR-4",
  chats: [{ id: sid, project: "demo", state: "live", idle: true }], models: [] });
await settle();
const retry = again.timers.find((timer) => timer.ms === 2000);
if (!retry) fail("после перезагрузки нет повтора");
retry.fn();
await settle();
if (resent.length !== 1 || resent[0].scenario.snapshot !== scenario.snapshot || resent[0].scenario.tree !== wire.tree || resent[0].text !== draft || resent[0].msg_id !== sent[0].msg_id) fail("повтор получил новый снимок/raw/маршрут/ключ: " + JSON.stringify(resent));
const fake = { classList: { contains: () => false } };
if (sandbox.askPickSaid([{ mark: fake, pick: { fork: "сценарии", id: "СЦ-1", scenario } }]) !== "ничего") fail("снятие всех не стало отказом");
process.stdout.write(JSON.stringify(sent[0]));

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const sid = "aaaa1111-1111";
const text = "проверь вложения";
const wire = '<fragment file="/tmp/clip.txt">\nвставлен фрагмент\n</fragment>\n' +
  '<screenshot file="/tmp/clip.png">\nвставлен снимок\n</screenshot>\n' + text;
const id = "clip-retry-original";
const sent = [];
const { sandbox, timers } = makeSandbox(appPathArg(), (path, init) => {
  if (init?.method === "POST") {
    sent.push({ path, body: JSON.parse(init.body) });
    return { way: "socket" };
  }
  if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
  if (path.endsWith("/ask")) return { ask: null };
  return {};
}, { store: { ["devkit.chat.pend.demo/" + sid]: JSON.stringify([
  { text, wire, id, born: Date.now(), state: "bad" },
]) } });
sandbox.chatPanel("demo", { project: "demo", addr: sid, sid, task: "XR-4",
  chats: [{ id: sid, project: "demo", state: "live", idle: true }], models: [] });
await settle();
if (sent.length) fail("восстановление отправило запись раньше повтора");
const retry = timers.find((timer) => timer.ms === 2000);
if (!retry) fail("нет автоповтора восстановленной записи");
retry.fn();
await settle();
if (sent.length !== 1 || !sent[0].path.endsWith("/" + sid + "/say")) {
  fail("повтор пошёл другой дорогой или повторно загрузил вложения: " + JSON.stringify(sent));
}
if (sent[0].body.text !== wire || sent[0].body.msg_id !== id) {
  fail("повтор потерял исходные вложения, их порядок или ключ: " + JSON.stringify(sent[0]));
}

// Стенд вида записей входа (ветка poc-chat, DK-577).
//
// Живой случай: пользователь на приёмке сказал, что вход в чате читается
// гостем в ленте. «Я просил сделать блоки прямо в чате, но они отличаются от
// стандартных блоков чата по размеру и оформлению. Должно быть как чат, всё
// органично».
//
// Предмет стенда: раскладка записи входа снимается числом с настоящей
// разметки и настоящего style.css и сверяется с раскладкой обычной записи
// ленты. Разница допускается только там, где несёт смысл: внутри записи входа
// живут кнопки, ссылка и поле кода, у обычной реплики их нет. Меряется на
// узком экране (390) и на широком (1440).
//
// Зовётся: node testdata/poc_loginfit.mjs static/app.js [--show]

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

const app = appPathArg();
const show = process.argv.includes("--show");
const BYE = "Login expired. Please run /login";

// --- разбор style.css с памятью о медиазапросе ---
const css = readFileSync(join(dirname(app), "style.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");
const rules = [];
{
  let i = 0;
  let media = "";
  while (i < css.length) {
    const open = css.indexOf("{", i);
    if (open < 0) break;
    const head = css.slice(i, open).replace(/^[\s};]+/, "").trim();
    if (head.startsWith("@media")) {
      media = head.slice(6).trim();
      i = open + 1;
      continue;
    }
    if (head.startsWith("@")) {
      // Прочие at-правила пропускаются вместе с телом.
      let depth = 1;
      let j = open + 1;
      while (j < css.length && depth > 0) {
        if (css[j] === "{") depth++;
        if (css[j] === "}") depth--;
        j++;
      }
      i = j;
      continue;
    }
    const close = css.indexOf("}", open);
    if (close < 0) break;
    rules.push({ sel: head, decl: css.slice(open + 1, close), media });
    i = close + 1;
    // Закрылся ли заодно медиаблок: считаем по следующей непустой скобке.
    const rest = css.slice(i);
    const nextOpen = rest.indexOf("{");
    const nextClose = rest.indexOf("}");
    if (media && nextClose >= 0 && (nextOpen < 0 || nextClose < nextOpen)) {
      media = "";
      i += nextClose + 1;
    }
  }
}

// Медиазапрос считается по ширине экрана: в стенде важны только потолки и полы.
const mediaFits = (cond, width) => {
  if (!cond) return true;
  let ok = true;
  for (const m of cond.matchAll(/\((max|min)-width:\s*([0-9.]+)px\)/g)) {
    ok = ok && (m[1] === "max" ? width <= Number(m[2]) : width >= Number(m[2]));
  }
  return ok;
};

// Цепочка предков узла по классам и тегам.
const chainOf = (node) => {
  const out = [];
  for (let n = node; n; n = n.parentNode) {
    out.unshift({
      tag: String(n.tagName || "").toLowerCase(),
      cls: String(n.className || "").split(" ").filter(Boolean),
    });
  }
  return out;
};

// Одна ступень селектора вида «div.a.b» или «.a.b». Псевдоклассы и атрибуты
// стенд не считает: их правила в сверку не идут.
const stepOf = (part) => {
  if (/[:\[\]>~+*#]/.test(part)) return null;
  const m = /^([a-zA-Z]*)((?:\.[A-Za-z0-9_-]+)*)$/.exec(part);
  if (!m || (!m[1] && !m[2])) return null;
  return { tag: m[1].toLowerCase(), cls: m[2].split(".").filter(Boolean) };
};

const stepHits = (step, node) =>
  (!step.tag || step.tag === node.tag) && step.cls.every((c) => node.cls.includes(c));

// Потомковый разбор справа налево.
const selHits = (sel, chain) => {
  const parts = sel.trim().split(/\s+/);
  const steps = parts.map(stepOf);
  if (steps.some((s) => !s)) return false;
  let at = chain.length - 1;
  if (!stepHits(steps[steps.length - 1], chain[at])) return false;
  at--;
  for (let k = steps.length - 2; k >= 0; k--) {
    let hit = false;
    while (at >= 0) {
      if (stepHits(steps[k], chain[at])) { hit = true; at--; break; }
      at--;
    }
    if (!hit) return false;
  }
  return true;
};

// Раскладка узла числом: свойства из списка, последнее правило побеждает.
const WANT = ["max-width", "display", "gap", "margin-top", "margin", "padding",
  "padding-bottom", "border-radius", "font", "font-size", "color", "background",
  "border", "border-color", "overflow-wrap", "opacity", "align-self", "text-align"];

const layoutOf = (node, width) => {
  const chain = chainOf(node);
  const got = {};
  for (const rule of rules) {
    if (!mediaFits(rule.media, width)) continue;
    if (!rule.sel.split(",").some((part) => selHits(part, chain))) continue;
    for (const piece of rule.decl.split(";")) {
      const at = piece.indexOf(":");
      if (at < 0) continue;
      const name = piece.slice(0, at).trim();
      if (!WANT.includes(name)) continue;
      got[name] = piece.slice(at + 1).trim();
    }
  }
  return got;
};

const deepFind = (node, hit, out = []) => {
  if (!node || typeof node !== "object") return out;
  if (hit(node)) out.push(node);
  for (const kid of node.children || []) deepFind(kid, hit, out);
  return out;
};
const hasClass = (cls) => (n) => String(n.className || "").split(" ").includes(cls);

let items = [];
const { sandbox } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const out = (sid) => ({
  addr: sid, sid, task: "DK-397", chats: [], models: [], project: "demo",
  entry: { tmux: "chat-9", state: "live", login: true },
});

items = [
  { key: "m-1", role: "user", text: "посчитай остаток бюджета",
    time: "2026-08-29T19:00:00+03:00" },
  { key: "m-2", role: "assistant", text: BYE, logout: true,
    time: "2026-08-29T19:00:02+03:00" },
];

const panel = sandbox.chatPanel("demo", out("aaaa5779-1111"));
await settle();

const feed = byClass(panel, "chatfeed");
const talk = byClass(panel, "cbyetalk");
if (!feed) fail("ленты разговора нет: " + dump(panel));
if (!talk || talk.hidden) fail("записей входа нет: " + dump(panel));

// Сверяется с ответом агента, а не со своей репликой: своя красится отдельно
// («.msg.me»), а запись входа своей не считается.
const feedMsg = deepFind(feed, hasClass("msg")).find((n) => !hasClass("me")(n));
const loginMsg = deepFind(talk, hasClass("msg"))[0];
if (!feedMsg) fail("в ленте нет ни одной записи: " + dump(feed));
if (!loginMsg) fail("вход не собран записью ленты: " + dump(talk));
const kidOf = (node, cls) => deepFind(node, hasClass(cls))[0];
// Ритм текста живёт на абзаце внутри разметки, а не на её корне.
const paraOf = (node) => deepFind(kidOf(node, "md"),
  (n) => String(n.tagName || "").toLowerCase() === "p")[0];

const pairs = [
  ["запись", feedMsg, loginMsg],
  ["пузырь", kidOf(feedMsg, "bb"), kidOf(loginMsg, "bb")],
  ["текст", kidOf(feedMsg, "md"), kidOf(loginMsg, "md")],
  ["подпись", kidOf(feedMsg, "mm"), kidOf(loginMsg, "mm")],
  ["абзац", paraOf(feedMsg), paraOf(loginMsg)],
];

for (const width of [390, 1440]) {
  for (const [name, a, b] of pairs) {
    if (!a) fail("у записи ленты нет узла «" + name + "»");
    if (!b) fail("у записи входа нет узла «" + name + "»: " + dump(loginMsg));
    const one = layoutOf(a, width);
    const two = layoutOf(b, width);
    if (show) {
      console.log("[" + width + "] " + name);
      for (const k of WANT) {
        if (one[k] === undefined && two[k] === undefined) continue;
        const mark = one[k] === two[k] ? "  " : "!!";
        console.log("   " + mark + " " + k + ": лента " + (one[k] ?? "-") +
          " | вход " + (two[k] ?? "-"));
      }
    }
    for (const k of WANT) {
      if (one[k] !== two[k]) {
        fail("на " + width + " точках у узла «" + name + "» свойство " + k +
          " разошлось: лента «" + (one[k] ?? "нет") + "», вход «" +
          (two[k] ?? "нет") + "»");
      }
    }
  }
}

// Разница, которая несёт смысл, остаётся: внутри записи входа живут кнопки,
// ссылка и поле кода, и они стоят в том же пузыре, а не рядом с ним.
const bb = kidOf(loginMsg, "bb");
const own = deepFind(talk, (n) => String(n.className || "").includes("loginbtns"))[0];
if (!own) fail("кнопок входа в записи нет: " + dump(talk));
for (let n = own; n; n = n.parentNode) {
  if (n === bb) break;
  if (!n.parentNode) fail("кнопки входа стоят вне пузыря записи: " + dump(talk));
}

console.log("ок: записи входа сняты числом с настоящего style.css и сошлись с " +
  "записью ленты на 390 и 1440 точках, а кнопки живут внутри пузыря");

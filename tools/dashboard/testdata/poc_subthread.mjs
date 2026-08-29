// Стенд нити бокового журнала субагента (ветка poc-chat, DK-577).
//
// Живой случай с экрана: «синяя нить начинается не с сообщения субагента, а со
// следующего сообщения и завершается, не доходя до блока про конец фоновой
// работы. Банально видно даже по тому, что линия начинается не с
// кружка, а непонятно с какого места». Нить рисовалась по каждой записи
// журнала своим отрезком, а работа субагента идёт шире: открывает её вызов
// Agent, закрывает весть о том, что фоновый агент закончил, и между ними
// вперемешку идут ходы диспетчера. Отсюда и обрубок не с того кружка, и разрыв
// на каждом чужом ходе.
//
// Предмет стенда две вещи. Первая: отрезок считается по всему списку записей и
// накрывает работу целиком, от вызова до вести о конце. Вторая: числами
// style.css нить непрерывна на всём отрезке (щели между записями она
// перекрывает) и стоит концами ровно на кружках.
//
// Зовётся: node testdata/poc_subthread.mjs static/app.js

import { makeSandbox, fail, appPathArg } from "./poc_dom.mjs";
import { cssRules } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

// --- отрезок работы считается по списку записей ---

const { sandbox } = makeSandbox(app, () => ({}));
if (typeof sandbox.feedSubs !== "function") {
  fail("функции feedSubs нет: отрезок работы субагента считать нечем");
}

// Список снят с живого разговора devkit 8257b5e0: вызов Agent, боковой журнал
// вперемешку с ходами диспетчера (человек успевает написать посреди работы) и
// весть о конце.
const talk = [
  { role: "assistant", text: "перед работой" },
  { role: "tool", tool: "Agent", text: "Нить бокового журнала" },
  { role: "assistant", text: "заказ субагенту", sub: "exec-medium" },
  { role: "tool", tool: "Bash", text: "git log", sub: "exec-medium" },
  { role: "user", text: "правь заодно панель квоты" },
  { role: "tool", tool: "Bash", text: "grep", sub: "exec-medium" },
  { role: "note", mark: "agent", text: "Фоновый агент завершил работу" },
  { role: "assistant", text: "после работы" },
];
const got = sandbox.feedSubs(talk);
const has = (i, cls) => (" " + got[i] + " ").includes(" " + cls + " ");

if (has(0, "sub") || has(7, "sub")) {
  fail("нить вылезла за работу: " + JSON.stringify(got));
}
if (!has(1, "subtop")) {
  fail("нить начинается не с вызова Agent, а позже: " + JSON.stringify(got));
}
if (!has(6, "subend")) {
  fail("нить не доходит до вести о конце работы: " + JSON.stringify(got));
}
for (let i = 1; i <= 6; i++) {
  if (!has(i, "sub")) {
    fail("нить рвётся на записи " + i + " (" + (talk[i].text || "") + "): " +
      JSON.stringify(got));
  }
}

// --- нить непрерывна и стоит концами на кружках ---

// Разбор объявлений в карту свойств.
const declOf = (text) => {
  const out = {};
  for (const part of String(text).split(";")) {
    const [k, v] = part.split(":");
    if (!k || v === undefined) continue;
    out[k.trim()] = v.trim();
  }
  return out;
};

// Селектор вида «.frow.sub.f-bub», может кончаться псевдоэлементом или именем
// потомка. Правила ленты все такие, и разбирать общий случай тут незачем.
const parse = (sel) => {
  const m = /^([.\w-]+)(::before)?(?:\s+\.(fdot))?$/.exec(sel.trim());
  if (!m) return null;
  return {
    cls: m[1].split(".").filter(Boolean),
    before: Boolean(m[2]),
    kid: m[3] || "",
  };
};

// Свойства узла: правила идут по порядку файла, более цепкое (больше классов)
// побеждает. Вид «dot» это кружок внутри строки, и правила ему достаются как
// свои (.fdot), так и через классы самой строки (.frow.f-bub .fdot).
const propsOf = (classes, want) => {
  const set = new Set(classes);
  const hits = [];
  for (let i = 0; i < rules.length; i++) {
    const r = rules[i];
    if (r.media) continue;
    for (const one of r.sel.split(",")) {
      const p = parse(one);
      if (!p) continue;
      if (want === "dot") {
        if (p.before) continue;
        if (p.kid === "fdot") {
          if (!p.cls.every((c) => set.has(c))) continue;
          hits.push({ spec: p.cls.length + 1, at: i, decl: declOf(r.decl) });
          continue;
        }
        if (p.cls.length !== 1 || p.cls[0] !== "fdot") continue;
        hits.push({ spec: 1, at: i, decl: declOf(r.decl) });
        continue;
      }
      if (p.kid) continue;
      if (p.before !== (want === "before")) continue;
      if (!p.cls.every((c) => set.has(c))) continue;
      hits.push({ spec: p.cls.length, at: i, decl: declOf(r.decl) });
    }
  }
  hits.sort((a, b) => (a.spec - b.spec) || (a.at - b.at));
  const out = {};
  for (const h of hits) Object.assign(out, h.decl);
  return out;
};

const px = (v, dflt) => {
  if (v === undefined || v === "auto") return dflt;
  const m = /^(-?[0-9.]+)px$/.exec(v);
  return m ? Number(m[1]) : dflt;
};

// Кружок ленты рисуется у самой строки, и его середина это край работы.
const dotMid = (classes) => {
  const dot = propsOf(classes, "dot");
  const size = px(dot.height, 11);
  return px(dot.top, 0) + size / 2;
};

// Нить строки: где начинается от верха коробки и где кончается. Отрицательное
// «снизу» это выход за коробку, им и перекрывается щель между записями.
const thread = (classes) => {
  const box = propsOf(classes, "box");
  const line = propsOf(classes, "before");
  const top = px(line.top, 0);
  const height = px(line.height, null);
  return {
    hidden: line.display === "none",
    top,
    // Низ считается от нижнего края коробки: положительное это подъём внутрь.
    up: height === null ? px(line.bottom, 0) : null,
    height,
    mTop: px(box["margin-top"], 0),
    mBot: px(box["margin-bottom"], 0),
    colored: /--deleg/.test(String(line.background || "")),
    width: px(line.width, px(propsOf(["frow"], "before").width, 1)),
  };
};

// Строки отрезка: те же виды, что и в живом разговоре. Высота коробки тут не
// нужна: непрерывность считается по краям, а не по середине.
const span = [
  ["frow", "r-tool", "sub", "subtop", "f-head"],
  ["frow", "r-assistant", "sub", "f-bub"],
  ["frow", "r-user", "sub", "gtop", "f-bub"],
  ["frow", "r-assistant", "sub", "gend", "f-bub"],
  ["frow", "r-tool", "sub", "f-head"],
  ["frow", "r-note", "sub", "subend", "f-fold"],
];

const head = thread(span[0]);
if (Math.abs(head.top - dotMid(span[0])) > 0.1) {
  fail("нить начинается не с кружка первой записи: верх " + head.top +
    ", кружок " + dotMid(span[0]));
}
const tail = thread(span[span.length - 1]);
if (tail.height === null) {
  fail("низ нити у последней записи работы задан не высотой, и конец её плавает");
}
const tailEnd = tail.top + tail.height;
if (Math.abs(tailEnd - dotMid(span[span.length - 1])) > 0.1) {
  fail("нить кончается не на кружке последней записи: низ " + tailEnd +
    ", кружок " + dotMid(span[span.length - 1]));
}

for (let i = 1; i < span.length; i++) {
  const up = thread(span[i - 1]);
  const low = thread(span[i]);
  // Щель между записями: сколько нить верхней не дошла до низа своей коробки,
  // плюс поля обеих, плюс отступ нити нижней от верха своей коробки.
  const short = up.height === null ? up.up : null;
  if (short === null && i > 1) {
    fail("нить записи " + (i - 1) + " задана высотой посреди работы: конец её плавает");
  }
  const gap = (short === null ? 0 : short) + up.mBot + low.mTop + low.top;
  if (gap > 0.1) {
    fail("нить рвётся между записями " + (i - 1) + " и " + i + ": щель " +
      gap + " точек (" + span[i].join(".") + ")");
  }
}

// Цвет отличает боковой журнал, а толщина у нити одна: двойная читалась
// жирной чертой («не надо делать линию такой жирной, это чрезмерно»).
const plain = thread(["frow", "r-tool", "f-head"]);
if (!head.colored) fail("нить работы субагента не отличена цветом передачи");
if (head.width !== plain.width) {
  fail("нить работы субагента толще общей: " + head.width + " против " +
    plain.width);
}

console.log("poc_subthread: ok");

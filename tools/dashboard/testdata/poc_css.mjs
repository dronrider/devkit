// Раскладка узла числом: разбор настоящего style.css и разрешение свойств по
// цепочке предков. Стенду вида нужен не глаз, а число. «Выглядит иначе» это не
// находка, пока не сказано, какое свойство и на сколько разошлось.
//
// Разбираются потомковые селекторы из классов и тегов, медиазапросы по ширине
// и одно состояние на выбор (:focus и подобные). Правила с прочими
// псевдоклассами и атрибутами в сверку не идут: стенд считает то, что умеет
// посчитать, и молчать об остальном честнее, чем угадывать.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

// cssRules читает style.css рядом с app.js и отдаёт правила по порядку, каждое
// со своим медиазапросом.
export function cssRules(appPath) {
  const css = readFileSync(join(dirname(appPath), "style.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const rules = [];
  let i = 0;
  let media = "";
  let depth = 0;
  while (i < css.length) {
    const open = css.indexOf("{", i);
    if (open < 0) break;
    const head = css.slice(i, open).replace(/^[\s};]+/, "").trim();
    const closes = (css.slice(i, open).match(/}/g) || []).length;
    if (depth > 0 && closes > 0) {
      depth -= closes;
      if (depth <= 0) { depth = 0; media = ""; }
    }
    if (head.startsWith("@media")) {
      media = head.slice(6).trim();
      depth += 1;
      i = open + 1;
      continue;
    }
    if (head.startsWith("@")) {
      let d = 1;
      let j = open + 1;
      while (j < css.length && d > 0) {
        if (css[j] === "{") d++;
        if (css[j] === "}") d--;
        j++;
      }
      i = j;
      continue;
    }
    const close = css.indexOf("}", open);
    if (close < 0) break;
    rules.push({ sel: head, decl: css.slice(open + 1, close), media });
    i = close + 1;
  }
  return rules;
}

// Медиазапрос считается по ширине экрана: стенду важны потолки и полы.
const mediaFits = (cond, width) => {
  if (!cond) return true;
  let ok = true;
  for (const m of cond.matchAll(/\((max|min)-width:\s*([0-9.]+)px\)/g)) {
    ok = ok && (m[1] === "max" ? width <= Number(m[2]) : width >= Number(m[2]));
  }
  return ok;
};

// Цепочка предков узла по классам и тегам.
export function chainOf(node) {
  const out = [];
  for (let n = node; n; n = n.parentNode) {
    out.unshift({
      tag: String(n.tagName || "").toLowerCase(),
      cls: String(n.className || "").split(" ").filter(Boolean),
    });
  }
  return out;
};

// Одна ступень селектора вида «div.a.b» или «.a.b», с необязательным
// состоянием в хвосте.
const stepOf = (part, state) => {
  let body = part;
  let want = "";
  if (state && body.endsWith(state)) {
    body = body.slice(0, -state.length);
    want = state;
  }
  if (/[:\[\]~+*#]/.test(body)) return null;
  const m = /^([a-zA-Z]*)((?:\.[A-Za-z0-9_-]+)*)$/.exec(body);
  if (!m || (!m[1] && !m[2])) return null;
  return { tag: m[1].toLowerCase(), cls: m[2].split(".").filter(Boolean), want };
};

const stepHits = (step, node) =>
  (!step.tag || step.tag === node.tag) && step.cls.every((c) => node.cls.includes(c));

// Разбор справа налево: потомок через пробел, прямой ребёнок через «>».
const selHits = (sel, chain, state) => {
  const parts = sel.trim().replace(/>/g, " > ").split(/\s+/).filter(Boolean);
  const steps = [];
  let kin = " ";
  for (const part of parts) {
    if (part === ">") { kin = ">"; continue; }
    const step = stepOf(part, state);
    if (!step) return false;
    steps.push({ ...step, kin });
    kin = " ";
  }
  if (!steps.length) return false;
  // Состояние спрашивается только у самой записи, а не у её предков.
  if (steps.slice(0, -1).some((s) => s.want)) return false;
  let at = chain.length - 1;
  if (!stepHits(steps[steps.length - 1], chain[at])) return false;
  at--;
  for (let k = steps.length - 1; k >= 1; k--) {
    const step = steps[k - 1];
    // Родство берётся у той ступени, что стоит справа: это её связь с левой.
    if (steps[k].kin === ">") {
      if (at < 0 || !stepHits(step, chain[at])) return false;
      at--;
      continue;
    }
    let hit = false;
    while (at >= 0) {
      if (stepHits(step, chain[at])) { hit = true; at--; break; }
      at--;
    }
    if (!hit) return false;
  }
  return true;
};

// layoutOf сводит свойства узла: последнее правило побеждает. state («:focus»)
// добавляет правила этого состояния к обычным.
export function layoutOf(node, opts) {
  const { rules, width, want, state } = opts;
  const chain = chainOf(node);
  const got = {};
  for (const rule of rules) {
    if (!mediaFits(rule.media, width)) continue;
    const hit = rule.sel.split(",").some((part) =>
      selHits(part, chain, "") || (state && selHits(part, chain, state)));
    if (!hit) continue;
    for (const piece of rule.decl.split(";")) {
      const at = piece.indexOf(":");
      if (at < 0) continue;
      const name = piece.slice(0, at).trim();
      if (want && !want.includes(name)) continue;
      got[name] = piece.slice(at + 1).trim();
    }
  }
  return got;
}

// Поиск узлов вглубь по признаку.
export function deepFind(node, hit, out = []) {
  if (!node || typeof node !== "object") return out;
  if (hit(node)) out.push(node);
  for (const kid of node.children || []) deepFind(kid, hit, out);
  return out;
}

export const hasClass = (cls) => (n) =>
  String(n.className || "").split(" ").includes(cls);

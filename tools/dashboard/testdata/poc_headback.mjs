// Стенд дороги на доску в шапке страницы (ветка poc-chat).
//
// Шапка внутри проекта звалась «Доска demo» и была ссылкой на доску. Слова
// называли место, а не переход, и человек попросил заменить их на «Назад на
// доску» со стрелкой слева. Имя проекта из слов при этом уходит, и место,
// откуда видно текущий проект, остаётся одно: список проектов рядом, где он
// подсвечен, а на телефоне выбор проекта в той же шапке.
//
// Предмет стенда: на экране под доской (задача, запись накопителя) шапка
// читается «Назад на доску», слева от слов стоит значок, кегль остался
// прежним, черта под словами никуда не делась, и нажатие уводит на доску
// проекта. Сама доска дороги на себя не носит ни в одном из трёх табов: все
// они её разделы, и «назад» вело бы с доски на неё же. Дыры на месте слов при
// этом не остаётся, шапка там называет своё место, а проект остаётся виден и
// без имени в словах: выбор проекта шапки знает текущий.
//
// Зовётся: node testdata/poc_headback.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Кегль, значок и черта живут в стилях, мок разметки о них не знает вовсе.
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "..", "static", "style.css"), "utf8");
const html = readFileSync(join(here, "..", "static", "index.html"), "utf8");
const cssRule = (sel) => {
  const at = css.indexOf(sel + "{");
  if (at < 0) return "";
  return css.slice(at + sel.length + 1, css.indexOf("}", at));
};
const px = (rule, prop) =>
  Number((new RegExp("(?:^|;)\\s*" + prop + ":\\s*(\\d+(?:\\.\\d+)?)px").exec(rule) || [])[1]);

const app = appPathArg();

const row = {
  id: "XR-6", title: "дорога на доску", type: "task", cost: "M",
  p: "P2", r: 30, r_parts: [25, 3, 1, 0, 1], sect: "backlog", section: "Backlog",
  order: "Выполни XR-6",
};

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: [] }, { name: "сосед", prefix: "NB", works: [] }] };
  }
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: row.id, row, after: [], blocks: [],
      file: "docs/tasks/XR-6.md", text: "# XR-6\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

// Узлы шапки берутся у самого дерева: карта заполняется по первому запросу из
// app.js, и до первой отрисовки их там ещё нет.
const pname = () => sandbox.document.getElementById("pname");
const kids = (node) => Array.from(node.children || []);

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

await settle();
await go("#demo/XR-6");

// --- шапка экрана внутри проекта называет переход, а не место ---
{
  const said = dump(pname()).replace(/\s+/g, " ").trim();
  if (said !== "Назад на доску") {
    fail("шапка экрана задачи читается не «Назад на доску»: " + JSON.stringify(said));
  }
  if (!String(pname().className).split(" ").includes("hgo")) {
    fail("шапка не помечена как ведущая: " + pname().className);
  }
  // Значок стоит слева от слов и приезжает из значков разметки, а не рисуется
  // рамками стилей.
  const parts = kids(pname());
  const at = parts.findIndex((n) => String(n.className || "").includes("hgoi"));
  if (at !== 0) {
    fail("стрелки слева от слов нет: " + JSON.stringify(parts.map((n) => n.className)));
  }
  if (!html.includes('data-ico="i-out"')) fail("значка стрелки нет среди значков разметки");
}

// --- кегль прежний, а дорога по-прежнему видна ---
{
  const link = cssRule(".bhead h2.hgo");
  if (!link) fail("правила дороги на доску в стилях нет");
  const size = px(link, "font(?:-size)?") ||
    Number((/font:\s*\d+\s+(\d+(?:\.\d+)?)px/.exec(link) || [])[1]);
  if (size !== 12.5) fail("кегль дороги на доску съехал с 12.5: " + size);
  if (!/font:\s*400\s/.test(link)) fail("вес дороги на доску не обычный: " + link);
  // Черта под словами, а не под всей ссылкой: под стрелкой она читалась бы
  // зачёркнутым значком.
  const words = cssRule(".bhead h2.hgo .hgot");
  if (!words.includes("text-decoration:underline")) {
    fail("дорога ничем не показывает, что она ведёт: " + JSON.stringify(words));
  }
  // Значок под кегль слов: крупнее строки он забирал бы шапку себе.
  const ico = cssRule(".bhead h2.hgo .hgoi");
  const wide = px(ico, "width");
  if (!(wide >= 10 && wide <= 16)) fail("стрелка набрана не под кегль слов: " + JSON.stringify(ico));
}

// --- нажатие уводит на доску проекта ---
{
  pname().handlers.click({});
  await settle();
  if (String(sandbox.location.hash).replace("#", "") !== "demo") {
    fail("нажатие на шапку увело не на доску проекта: " + sandbox.location.hash);
  }
}

// --- на самой доске дороги на себя нет, а проект по-прежнему виден ---
{
  await go("#demo");
  const said = dump(pname()).replace(/\s+/g, " ").trim();
  if (said.includes("Назад")) {
    fail("доска зовёт назад на саму себя: " + JSON.stringify(said));
  }
  if (!said.includes("demo")) {
    fail("шапка доски не называет своё место: " + JSON.stringify(said));
  }
  if (String(pname().className).split(" ").includes("hgo")) {
    fail("шапка доски осталась ссылкой на саму себя: " + pname().className);
  }
  if (!cssRule(".bhead h2.hhere")) fail("правила шапки доски в стилях нет");
  // Табы это разделы той же доски, и дороги назад нет ни в одном: человек уже
  // на доске (замечание пользователя). Пустого места при этом не остаётся,
  // шапка там называет своё место теми же словами.
  for (const tab of ["#demo/sess", "#demo/drafts", "#demo/find"]) {
    await go(tab);
    const tsaid = dump(pname()).replace(/\s+/g, " ").trim();
    if (tsaid.includes("Назад")) fail("таб " + tab + " зовёт назад на доску, будучи её разделом: " + tsaid);
    if (!tsaid.includes("demo")) fail("в шапке таба " + tab + " дыра вместо места: " + JSON.stringify(tsaid));
  }
  // А запись накопителя лежит под доской, и дорога назад с неё есть.
  await go("#demo/draft/XR-6");
  if (!dump(pname()).includes("Назад на доску")) {
    fail("с записи накопителя дороги назад нет: " + JSON.stringify(dump(pname())));
  }
  // Проект виден и там, где слов о нём в шапке нет вовсе: выбор проекта знает
  // текущий, и на телефоне шапка отвечает на вопрос «где я» именно им.
  await go("#demo/XR-6");
  const sel = byId.get("pselect");
  const on = Array.from(sel.children || []).filter((o) => o.selected);
  if (on.length !== 1 || on[0].value !== "demo") {
    fail("текущий проект не выбран в списке проектов шапки: " +
      JSON.stringify(Array.from(sel.children || []).map((o) => [o.value, o.selected])));
  }
}

console.log("poc_headback: ok");

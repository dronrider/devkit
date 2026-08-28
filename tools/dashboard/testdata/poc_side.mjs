// Стенд сворачивания боковой колонки (POC DK-397, ветка poc-chat).
//
// Замечание пользователя: колонка с меню нужна редко, а место отнимает всегда.
// Сворачивается она кнопкой в своём верхнем уголке, возвращается кнопкой на том
// же месте экрана, а состояние переживает перезагрузку страницы.
//
// Предмет стенда: обе кнопки правят одно состояние, память о нём ложится в
// хранилище браузера и читается на следующем заходе, а освободившееся место
// достаётся содержимому (колонки таблицы пересчитываются тут же).
//
// Зовётся: node testdata/poc_side.mjs static/app.js

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const app = appPathArg();

const rows = [{ id: "XR-1", title: "строка доски", sect: "backlog", type: "task",
  cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24" }];

const answer = (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works: [] };
  }
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.endsWith("/works")) return { works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
};

const SIDE_KEY = "devkit.side.off";
const folded = (sandbox) => String(sandbox.document.getElementById("screen").className || "")
  .split(" ").includes("sideoff");
const press = (sandbox, id) => {
  const btn = sandbox.document.getElementById(id);
  if (!btn.handlers.click) fail("кнопка " + id + " не слушает нажатие: сворачивать колонку нечем");
  btn.handlers.click({ stopPropagation: () => {} });
};

// --- первый заход: колонка на месте, обе кнопки живые ---
{
  const { sandbox, store } = makeSandbox(app, answer);
  sandbox.location.hash = "#demo";
  await sandbox.refresh();
  await settle();
  if (folded(sandbox)) fail("колонка встретила свёрнутой без единого нажатия");

  // Свёртка отдаёт место содержимому: колонки таблицы пересчитываются тем же
  // ходом, а не следующей перерисовкой. Видно это по переменным ширин.
  const props = sandbox.document.documentElement.style.props;
  for (const key of Object.keys(props)) {
    if (key.startsWith("--tc-")) delete props[key];
  }
  press(sandbox, "side-fold");
  if (!folded(sandbox)) fail("нажатие не свернуло колонку");
  if (store.get(SIDE_KEY) !== "1") {
    fail("свёрнутая колонка не запомнилась: " + JSON.stringify(store.get(SIDE_KEY)));
  }
  if (!Object.keys(props).some((k) => k.startsWith("--tc-"))) {
    fail("после свёртки колонки таблица не пересчитала ширины: место останется пустым");
  }
  const say = sandbox.document.getElementById("side-fold").attrs["aria-expanded"];
  if (say !== "false") {
    fail("свёрнутая колонка не сказала об этом читалке экрана: " + JSON.stringify(say));
  }

  press(sandbox, "side-show");
  if (folded(sandbox)) fail("колонка не вернулась тем же движением");
  if (store.get(SIDE_KEY) !== "0") {
    fail("возврат колонки не запомнился: " + JSON.stringify(store.get(SIDE_KEY)));
  }
}

// --- следующий заход: свёрнутая колонка такой и встречает ---
{
  const { sandbox } = makeSandbox(app, answer, { store: { [SIDE_KEY]: "1" } });
  sandbox.location.hash = "#demo";
  await sandbox.refresh();
  await settle();
  if (!folded(sandbox)) fail("состояние колонки не пережило перезагрузку страницы");
  press(sandbox, "side-show");
  if (folded(sandbox)) fail("свёрнутая с прошлого захода колонка не разворачивается");
}

// --- стили: свёрнутой колонки нет вовсе, а место её достаётся содержимому ---
{
  const css = await readFile(join(dirname(fileURLToPath(import.meta.url)), "..",
    "static", "style.css"), "utf8");
  const rule = (sel) => {
    const m = new RegExp("(^|[\\n}])" + sel.replace(/[.]/g, "\\.") + "\\{([^}]*)\\}").exec(css);
    return m ? m[2] : "";
  };
  if (!rule(".screen.sideoff .side").includes("display:none")) {
    fail("свёрнутая колонка остаётся на экране: правила .screen.sideoff .side нет");
  }
  if (!rule(".bmain").includes("flex:1")) {
    fail("главная часть экрана не забирает остаток строки: место колонки останется пустым");
  }
  if (!rule(".sfold-back").includes("display:none") ||
    !rule(".screen.sideoff .sfold-back").includes("display:flex")) {
    fail("кнопка возврата колонки стоит не по состоянию: её видно всегда или никогда");
  }
  // На узком экране колонки нет и без сворачивания, и кнопкам там делать
  // нечего: лишний значок в шапке телефона отнимает место у заголовка.
  const narrow = /@media \(max-width:900px\)\{([\s\S]*?)\n\}/.exec(css);
  if (!narrow || !/\.sfold[^{]*\{[^}]*display:none/.test(narrow[1])) {
    fail("на телефоне кнопки сворачивания остались в шапке");
  }
}

console.log("poc_side: ok");

// Стенд блока срока и контроля формы задачи (DK-1503, находки 4-6). Прежде
// срок прятался за карандашом и появлялся после нажатия на карточку ранга,
// контроль стоял отдельной карточкой без стиля, а пикера дат не было видно.
//
// Предмет проверки: блок один, открыт сразу на просмотре, дата выбирается
// полем даты, контроль стоит в том же блоке тем же стилем, правка полем
// без карандаша делает форму грязной и поднимает кнопку сохранения, а снятие
// срока и снятие контроля остаются разными действиями.
//
// Зовётся: node testdata/poc_taskdue.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, "..", "static", "style.css"), "utf8");
const cssRule = (sel) => {
  const at = css.indexOf(sel + "{");
  if (at < 0) return "";
  return css.slice(at + sel.length + 1, css.indexOf("}", at));
};

const app = appPathArg();

const row = {
  id: "XR-7", title: "срок на форме", type: "task", cost: "M",
  p: "P2", r: 30, r_parts: [25, 3, 1, 0, 1], sect: "backlog", section: "Backlog",
  due: "2026-11-05", control: 3,
};

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: row.id, row, after: [], blocks: [],
      file: "docs/tasks/XR-7.md", text: "# XR-7\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
await settle();
await sandbox.loadHarnesses();
sandbox.location.hash = "demo/XR-7";
await sandbox.refresh();
await settle();

// --- блок один и открыт сразу, карандаш не нажат ---
const dueBox = byClass(groups, "duebox");
if (!dueBox) {
  fail("блока срока нет на просмотре формы: " + dump(groups).slice(0, 300));
}
if (allByClass(groups, "duebox").length !== 1) {
  fail("блоков срока и контроля больше одного: " + allByClass(groups, "duebox").length);
}
if (byClass(groups, "controlbox")) fail("контроль остался отдельной карточкой");
{
  const legend = dump(dueBox.querySelector("legend"));
  if (!legend.includes("срок")) fail("блок не назван сроком: " + legend);
  if (!legend.includes("контроль")) fail("блок не назван контролем: " + legend);
}

// --- дата пикером поля, контроль числом рядом, стиль общий ---
{
  const dueIn = allByClass(dueBox, "duein").find((n) => n.type === "date");
  if (!dueIn) fail("поля даты в блоке нет");
  if (dueIn.value !== row.due) fail("поле даты не встало на срок строки: " + dueIn.value);
  const ctrl = byClass(dueBox, "ctrl");
  if (!ctrl) fail("поля контроля в блоке нет");
  if (ctrl.type !== "number") fail("контроль правится не числом: " + ctrl.type);
  if (ctrl.value !== "3") fail("поле контроля не встало на контроль строки: " + ctrl.value);
  if (!String(ctrl.className).includes("duein")) {
    fail("поле контроля не несёт стиль поля даты: " + ctrl.className);
  }
  if (!byClass(dueBox, "ctrlword")) fail("слова «контроль» у числа нет");
}

// --- снятие срока и снятие контроля остаются разными действиями ---
{
  const clears = allByClass(dueBox, "duereset");
  if (clears.length !== 2) fail("кнопок снятия в блоке не две: " + clears.length);
  const words = clears.map((b) => dump(b));
  if (!words.some((w) => w.includes("срок")) || !words.some((w) => w.includes("контроль"))) {
    fail("кнопки снятия не различают срок и контроль: " + JSON.stringify(words));
  }
}

// --- правка полем без карандаша поднимает сохранение ---
{
  const dueIn = allByClass(dueBox, "duein").find((n) => n.type === "date");
  dueIn.value = "2026-11-09";
  dueIn.handlers.change();
  await settle();
  const save = deepBtn(groups, "Сохранить");
  if (!save || save.hidden) {
    fail("правка срока на просмотре не подняла кнопку сохранения");
  }
  // То же поле контроля: число дней меняется там же, без второго блока.
  const ctrl = byClass(dueBox, "ctrl");
  ctrl.value = "5";
  ctrl.handlers.input();
  await settle();
  if (!deepBtn(groups, "Сохранить") || deepBtn(groups, "Сохранить").hidden) {
    fail("правка контроля не подняла кнопку сохранения");
  }
}

// --- карандаш больше не прячет блок: класс включения не возвращается ---
{
  const pen = byClass(groups, "tpen");
  if (!pen) fail("карандаша правки на форме нет");
  pen.handlers.click();
  await settle();
  if (String(dueBox.className).split(" ").includes("on")) {
    fail("блок срока снова гасится классом правки: " + dueBox.className);
  }
}

// --- стили держат блок открытым и красят оба поля ---
{
  const box = cssRule(".duebox");
  if (!box.includes("display:flex")) fail("блок срока не открыт правилом стиля: " + box);
  if (css.includes(".duebox.on")) fail("правило скрытого блока вернулось в стили");
  if (!cssRule(".duebox .ctrl")) fail("поле контроля осталось без правила стиля");
  if (!cssRule(".duebox .duein::-webkit-calendar-picker-indicator")) {
    fail("значок календаря пикера не удержан правилом стиля");
  }
}

console.log("poc_taskdue: ok");

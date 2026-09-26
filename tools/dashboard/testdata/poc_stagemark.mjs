// Стенд этапа задачи в строке списка и на форме (DK-1119, макет «14 Этап
// задачи, ход 2», варианты 2a и 2b).
//
// Предмет: колонка хода строки списка и шапка со степпером формы получают от
// taskctl список одних и тех же полей (stage, stage_since, stage_round,
// stage_session, stage_at) и обязаны различать по ним ровно четыре состояния
// (живая сессия, молчащая, брошенная, ожидание) цветом словаря и подсказкой,
// не приписывая слова о сессии видимым текстом. Строка без записи этапа
// колонку не ломает: ячейка остаётся пустой.
//
// Зовётся: node testdata/poc_stagemark.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const now = Math.floor(Date.now() / 1000);

const row = (id, extra) => Object.assign({
  id, title: "строка " + id, type: "task", p: "P2", r: 30,
  r_parts: [25, 2, 1, 0, 2], cost: "S", link: "-", sect: "in-progress",
}, extra || {});

const board = {
  prefix: "XR",
  sections: [{
    key: "in-progress",
    title: "In progress",
    rows: [
      // живая сессия
      row("XR-1", { stage: "разработка", stage_since: now - 12 * 60, stage_round: 1,
        stage_session: "сессия жива" }),
      // сессия молчит дольше рубежа
      row("XR-2", { stage: "ревью", stage_since: now - 45 * 60, stage_round: 2,
        stage_session: "сессия молчит 25 минут" }),
      // сессии нет, брошена
      row("XR-3", { stage: "разработка", stage_since: now - 5 * 3600, stage_round: 1,
        stage_session: "сессии нет, брошена" }),
      // ожидание человека, встала на этапе «проверка»
      row("XR-4", { stage: "ждёт человека", stage_since: now - 3 * 3600, stage_at: "проверка" }),
      // без записи этапа вовсе
      row("XR-5", {}),
    ],
  }],
};

const { sandbox, byId, timers } = makeSandbox(app, (path) => {
  if (path === "/api/harnesses") return { harnesses: [{ name: "подписка-раз", default: true }] };
  if (path === "/api/quota") return { harnesses: [] };
  if (String(path).includes("/tasks/XR-3")) {
    return { row: board.sections[0].rows[2], text: "" };
  }
  return {};
});

const groups = byId.get("groups");
await settle();
sandbox.renderBoard("demo", board);

const rows = allByClass(groups, "trow");
const stageCell = (id) => {
  const tr = rows.find((r) => dump(byClass(r, "id")).trim() === id);
  if (!tr) fail("строки " + id + " нет на экране");
  return byClass(tr, "stage");
};

// --- живая сессия: слово этапа, круг и возраст, лента без приписок ---
{
  const cell = stageCell("XR-1");
  const box = byClass(cell, "act2");
  if (!box) fail("живая строка без колонки хода: " + dump(cell));
  const cls = String(box.className).split(" ");
  if (!cls.includes("k-dev") || !cls.includes("live")) {
    fail("живая строка без цвета группы или пульса: " + box.className);
  }
  if (box.title !== "сессия жива") {
    fail("подсказка живой строки не та: " + JSON.stringify(box.title));
  }
  const said = dump(box);
  if (!said.includes("разработка") || !said.includes("12 мин")) {
    fail("слово этапа или возраст не читаются: " + said);
  }
  if (said.includes("сессия") || said.includes("жива")) {
    fail("слова о сессии стоят видимым текстом, а не подсказкой: " + said);
  }
}

// --- молчащая сессия: тот же цвет группы, без пульса, круг у возраста ---
{
  const cell = stageCell("XR-2");
  const box = byClass(cell, "act2");
  const cls = String(box.className).split(" ");
  if (!cls.includes("k-rev") || cls.includes("live")) {
    fail("молчащая строка красится не той группой или несёт пульс: " + box.className);
  }
  if (box.title !== "сессия молчит 25 минут") {
    fail("подсказка молчащей строки не та: " + JSON.stringify(box.title));
  }
  const said = dump(box);
  if (!said.includes("ревью") || !said.includes("круг 2") || !said.includes("45 мин")) {
    fail("слово, круг или возраст молчащей строки не читаются: " + said);
  }
  if (said.includes("молчит")) fail("слово «молчит» стоит видимым текстом: " + said);
}

// --- брошенная: цвет брошенной, деление ленты сплошным красным ---
{
  const cell = stageCell("XR-3");
  const box = byClass(cell, "act2");
  const cls = String(box.className).split(" ");
  if (!cls.includes("k-gone")) fail("брошенная строка не красная: " + box.className);
  if (box.title !== "сессии нет, брошена") {
    fail("подсказка брошенной строки не та: " + JSON.stringify(box.title));
  }
  const said = dump(box);
  if (said.includes("брошена") || said.includes("сессии нет")) {
    fail("слова о брошенной сессии видимым текстом: " + said);
  }
  const now2 = byClass(box, "seg");
  const marks = now2.children.map((i) => String(i.className || ""));
  if (!marks.some((m) => m.split(" ").includes("now") && m.includes("gone"))) {
    fail("деление ленты у брошенной не сплошным красным: " + JSON.stringify(marks));
  }
}

// --- ожидание: слово оранжевой группы, деление ленты на этапе stage_at ---
{
  const cell = stageCell("XR-4");
  const box = byClass(cell, "act2");
  const cls = String(box.className).split(" ");
  if (!cls.includes("k-wait")) fail("строка ожидания не оранжевая: " + box.className);
  if (box.title !== "ожидание на этапе «проверка»") {
    fail("подсказка ожидания не та: " + JSON.stringify(box.title));
  }
  const said = dump(box);
  if (!said.includes("ждёт человека") || !said.includes("3 ч")) {
    fail("слово или возраст ожидания не читаются: " + said);
  }
  const seg = byClass(box, "seg");
  const marks = seg.children.map((i) => String(i.className || ""));
  // «проверка» это восьмое, последнее деление ленты (индекс 7).
  if (marks[7] !== "now" || marks.slice(0, 7).some((m) => m !== "done")) {
    fail("лента ожидания подсвечивает не деление «проверка»: " + JSON.stringify(marks));
  }
}

// --- без записи этапа: колонка пустая, сетку не ломает ---
{
  const cell = stageCell("XR-5");
  if (cell.children.length) fail("пустая колонка хода получила содержимое: " + dump(cell));
}

// --- телефон: копия хода живёт внутри заголовка, а не третьей строкой ---
{
  const tr = rows.find((r) => dump(byClass(r, "id")).trim() === "XR-1");
  const tt = byClass(tr, "tt");
  const narrow = byClass(tt, "stage-narrow");
  if (!narrow) fail("в заголовке строки XR-1 нет копии колонки хода для телефона");
  const box = byClass(narrow, "act2");
  if (!box || !String(box.className).split(" ").includes("k-dev")) {
    fail("копия колонки хода в заголовке не та: " + dump(narrow));
  }
  if (!dump(narrow).includes("разработка")) {
    fail("копия колонки хода в заголовке не называет этап: " + dump(narrow));
  }
}

// --- возраст тикает на клиенте: минутный опрос страницы пересчитывает текст ---
{
  const one = timers.find((t) => t.ms === 60000 && t.fn);
  if (!one) fail("минутный опрос возраста этапа не завёлся: " + JSON.stringify(timers.map((t) => t.ms)));
  const ageNode = byClass(stageCell("XR-1"), "stage-age");
  if (!ageNode) fail("у живой строки нет узла возраста с data-stage-since");
  const before = dump(ageNode);
  // Время начала этапа отодвигается на час назад: тик обязан пересчитать
  // текст без нового опроса доски, а не оставить прежнее значение.
  ageNode.dataset.stageSince = String(Math.floor(Date.now() / 1000) - 3600);
  one.fn();
  await settle();
  const after = dump(ageNode);
  if (after === before) fail("минутный тик не тронул текст возраста: " + after);
  if (!after.includes("1 ч")) fail("минутный тик пересчитал возраст не туда: " + after);
}

// --- форма задачи брошенной строки: шапка, степпер и строка-подсказка ---
{
  await sandbox.renderTask("demo", [], "XR-3", null);
  await settle();
  const page = groups;
  const now2 = byClass(page, "now2");
  if (!now2) fail("на форме брошенной задачи нет шапки этапа");
  if (!String(now2.className).split(" ").includes("k-gone")) {
    fail("шапка формы не красная у брошенной: " + now2.className);
  }
  const said = dump(now2);
  if (!said.includes("разработка") || !said.includes("5 ч")) {
    fail("шапка формы не называет этап или возраст: " + said);
  }
  if (said.includes("брошена") || said.includes("сессии нет")) {
    fail("шапка формы приписывает слова о сессии: " + said);
  }
  const step2 = byClass(page, "step2");
  if (!step2) fail("на форме брошенной задачи нет степпера");
  const on = step2.children.filter((s) => String(s.className).split(" ").includes("on"));
  if (on.length !== 1 || !dump(on[0]).includes("разработка")) {
    fail("степпер не подсвечивает «разработка» одним делением: " +
      JSON.stringify(step2.children.map(dump)));
  }
  if (!String(on[0].className).split(" ").includes("gone")) {
    fail("текущее деление степпера у брошенной не красное: " + on[0].className);
  }
  const hint = byClass(page, "hint");
  if (!hint) fail("под степпером брошенной задачи нет строки-подсказки");
  const hintSaid = dump(hint);
  if (!hintSaid.includes("Сессии нет") || !hintSaid.includes("Взять в работу")) {
    fail("строка-подсказка брошенной задачи не та: " + hintSaid);
  }
}

console.log("poc_stagemark: ok");

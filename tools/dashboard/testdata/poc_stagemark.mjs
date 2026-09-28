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
  moved: "2026-09-26",
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
        stage_session: "сессии нет, брошена", accept: "mixed", barrier: "глаза" }),
      // ожидание человека, встала на этапе «проверка»
      row("XR-4", { stage: "ждёт человека", stage_since: now - 3 * 3600, stage_at: "проверка" }),
      // без записи этапа вовсе
      row("XR-5", {}),
      // машинное ожидание диспетчера на этапе «ревью»: слово называет работу,
      // ожидание идёт пометкой
      row("XR-6", { stage: "ждёт события", stage_since: now - 20 * 60, stage_round: 1,
        stage_at: "ревью" }),
      // запись ожидания старого формата, без этапа работы (чинит DK-1193)
      row("XR-7", { stage: "ждёт события", stage_since: now - 2 * 3600, stage_round: 2 }),
      // строка в Check с приёмкой за человеком: чипа «ждёт вашей приёмки»
      // больше нет, о том же говорит слово «проверка» с меткой «вы»
      row("XR-8", { sect: "check", accept: "mixed", barrier: "глаза",
        stage: "проверка", stage_since: now - 4 * 3600, stage_round: 1,
        stage_session: "сессия жива" }),
      // цель: чипа «цель» в строке нет, слово стоит первым в заголовке
      row("XR-9", { title: "Цель: конвейер не встаёт на ожидании" }),
      // строка Check без записи этапа: сказать про приёмку и слитый код
      // колонке хода нечем, и это уходит в подсказку номера
      row("XR-10", { sect: "check", accept: "mixed", barrier: "глаза",
        notes: ["код слит, выката не было"] }),
    ],
  }],
};

const { sandbox, byId, timers } = makeSandbox(app, (path) => {
  if (path === "/api/harnesses") return { harnesses: [{ name: "подписка-раз", default: true }] };
  if (path === "/api/quota") return { harnesses: [] };
  if (String(path).includes("/tasks/XR-6")) {
    return { row: board.sections[0].rows[5], file: "docs/tasks/XR-6.md",
      text: "# XR-6: заголовок из файла\n\n## Что происходит\n\nтело постановки\n" };
  }
  if (String(path).includes("/tasks/XR-3")) {
    // Первая строка файла это «# XR-3: ...», и на экране она не печатается:
    // заголовок один, в шапке формы (замечание 7 приёмки).
    return { row: board.sections[0].rows[2], file: "docs/tasks/XR-3.md",
      text: "# XR-3: заголовок из файла\n\n## Что происходит\n\nтело постановки\n" };
  }
  return {};
});

const groups = byId.get("groups");
await settle();
sandbox.renderBoard("demo", board);

const rows = allByClass(groups, "trow");
const rowOf = (id) => {
  const tr = rows.find((r) => dump(byClass(r, "id")).trim() === id);
  if (!tr) fail("строки " + id + " нет на экране");
  return tr;
};
const stageCell = (id) => byClass(rowOf(id), "stage");

// Видимый текст узла: слова подсказки, открываемой нажатием, в него не входят.
// Коробка подсказки лежит в разметке всегда, а показывает её наведение или
// класс `on` (DK-1119, замечание 3 приёмки второго круга), и обход дерева
// читал бы её словами колонки.
const saidOf = (node) => {
  const tip = byClass(node, "mtipbox");
  const all = dump(node);
  return tip ? all.split(dump(tip)).join(" ") : all;
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
  // Круг с возрастом на ноутбуке ушёл в подсказку, и слова о сессии стоят
  // только там же (замечание 4 приёмки).
  if (box.title !== "разработка, 12 мин, сессия жива") {
    fail("подсказка живой строки не та: " + JSON.stringify(box.title));
  }
  const said = saidOf(box);
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
  if (box.title !== "ревью, круг 2, 45 мин, сессия молчит 25 минут") {
    fail("подсказка молчащей строки не та: " + JSON.stringify(box.title));
  }
  const said = saidOf(box);
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
  if (box.title !== "разработка, 5 ч 0 мин, сессии нет, брошена") {
    fail("подсказка брошенной строки не та: " + JSON.stringify(box.title));
  }
  const said = saidOf(box);
  if (said.includes("брошена") || said.includes("сессии нет")) {
    fail("слова о брошенной сессии видимым текстом: " + said);
  }
  const now2 = byClass(box, "seg");
  const marks = now2.children.map((i) => String(i.className || ""));
  if (!marks.some((m) => m.split(" ").includes("now") && m.includes("gone"))) {
    fail("деление ленты у брошенной не сплошным красным: " + JSON.stringify(marks));
  }
}

// --- ожидание человека: слово этапа работы, метка «вы», деление на stage_at ---
{
  const cell = stageCell("XR-4");
  const box = byClass(cell, "act2");
  const cls = String(box.className).split(" ");
  if (!cls.includes("k-you")) fail("ожидание человека не оранжевое: " + box.className);
  if (box.title !== "проверка, 3 ч 0 мин; ждёт человека") {
    fail("подсказка ожидания не та: " + JSON.stringify(box.title));
  }
  const said = saidOf(box);
  // Слово называет работу, а не ожидание: «ждёт человека» это остановка на
  // этапе «проверка», и в строке стоит этап (замечание 1 приёмки).
  if (!said.includes("проверка")) fail("слово ожидания не назвало этап работы: " + said);
  if (said.includes("ждёт человека")) {
    fail("слово ожидания стоит вместо слова этапа: " + said);
  }
  if (!byClass(box, "you")) fail("у ожидания человека нет метки «вы»: " + said);
  const seg = byClass(box, "seg");
  const marks = seg.children.map((i) => String(i.className || ""));
  // «проверка» это восьмое, последнее деление ленты (индекс 7).
  if (marks[7] !== "now" || marks.slice(0, 7).some((m) => m !== "done")) {
    fail("лента ожидания подсвечивает не деление «проверка»: " + JSON.stringify(marks));
  }
}

// --- машинное ожидание: слово этапа работы и песочные часы при нём ---
{
  const cell = stageCell("XR-6");
  const box = byClass(cell, "act2");
  const cls = String(box.className).split(" ");
  // Цвет остаётся цветом этапа работы: ожидание диспетчера это норма, а не
  // отдельный этап (замечание 1 приёмки).
  if (!cls.includes("k-rev")) fail("ожидание события красится не этапом работы: " + box.className);
  if (box.title !== "ревью, 20 мин; ждёт события") {
    fail("подсказка машинного ожидания не та: " + JSON.stringify(box.title));
  }
  const said = saidOf(box);
  if (!said.includes("ревью")) fail("машинное ожидание не назвало этап работы: " + said);
  if (said.includes("ждёт события")) fail("в строке стоит слово ожидания: " + said);
  if (!byClass(box, "hg")) fail("у машинного ожидания нет песочных часов: " + said);
  if (byClass(box, "you")) fail("машинное ожидание помечено как ожидание человека: " + said);
  const marks = byClass(box, "seg").children.map((i) => String(i.className || ""));
  // «ревью» это четвёртое деление ленты (индекс 3).
  if (marks[3] !== "now") {
    fail("лента машинного ожидания подсвечивает не «ревью»: " + JSON.stringify(marks));
  }
}

// --- запись ожидания без этапа работы: слово остаётся словом ожидания ---
{
  const box = byClass(stageCell("XR-7"), "act2");
  if (!String(box.className).split(" ").includes("k-wait")) {
    fail("запись без этапа работы не оранжевая: " + box.className);
  }
  const said = saidOf(box);
  if (!said.includes("ждёт события")) {
    fail("записи без этапа работы нечего сказать, кроме слова ожидания: " + said);
  }
  // Часы стоят у всякого машинного ожидания, и запись без этапа работы не
  // исключение (вариант 3a макета, замечание 8 ревью).
  if (!byClass(box, "hg")) fail("у записи без этапа работы нет песочных часов: " + said);
  if (box.title.indexOf("DK-1193") < 0) {
    fail("подсказка не называет причину, по которой этапа работы нет: " +
      JSON.stringify(box.title));
  }
}

// --- проверка за человеком: слово «проверка» с меткой «вы», чипов приёмки нет ---
{
  const tr = rowOf("XR-8");
  const box = byClass(byClass(tr, "stage"), "act2");
  if (!String(box.className).split(" ").includes("k-you")) {
    fail("проверка за человеком не оранжевая: " + box.className);
  }
  if (!byClass(box, "you")) fail("у проверки за человеком нет метки «вы»: " + saidOf(box));
  if (box.title.indexOf("приёмка за вами") < 0 || box.title.indexOf("глаза") < 0) {
    fail("подсказка не называет приёмку и барьер: " + JSON.stringify(box.title));
  }
  const said = saidOf(tr);
  if (said.includes("ждёт вашей приёмки") || said.includes("агент проверит сам")) {
    fail("чип приёмки остался в строке: " + said);
  }
}

// --- строка Check без записи этапа: приёмка и пометки в подсказке номера ---
{
  const tr = rowOf("XR-10");
  if (byClass(byClass(tr, "stage"), "act2")) {
    fail("у строки без записи этапа появилась колонка хода");
  }
  const num = byClass(tr, "id").children.find((n) => dump(n).trim() === "XR-10");
  if (!num) fail("в ячейке номера нет самого номера: " + dump(byClass(tr, "id")));
  const tip = String(num.title || "");
  if (tip.indexOf("приёмка за вами") < 0 || tip.indexOf("код слит") < 0) {
    fail("след слитого кода пропал из строки без записи этапа: " + JSON.stringify(tip));
  }
}

// --- цель: чипа «цель» в строке нет, слово стоит первым в заголовке ---
{
  const tt = byClass(rowOf("XR-9"), "tt");
  const chips = byClass(tt, "rchips");
  const said = chips ? dump(chips) : "";
  if (said.includes("цель")) fail("чип «цель» остался в строке: " + said);
  if (!dump(byClass(tt, "ttl")).includes("Цель:")) {
    fail("слово «Цель:» пропало из заголовка строки: " + dump(tt));
  }
}

// --- дата строки идёт без века: «26-09-26», полная приходит подсказкой ---
{
  const when = byClass(rowOf("XR-1"), "twhen");
  const mark = byClass(when, "stale");
  if (!mark) fail("у строки нет даты правки: " + dump(when));
  if (!/^\d\d-\d\d-\d\d$/.test(dump(mark).trim())) {
    fail("дата в колонке печатается не «ГГ-ММ-ДД»: " + dump(mark));
  }
  if (String(mark.title || "").indexOf("2026") < 0) {
    fail("полная дата не ушла в подсказку: " + JSON.stringify(mark.title));
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
  // Ход стоит раньше чипов: на телефоне они идут одной строчкой, чипы за
  // лентой (вариант 3b макета, замечание 7 ревью).
  const at = tt.children.indexOf(narrow);
  const chipsAt = tt.children.indexOf(byClass(tt, "rchips"));
  if (chipsAt >= 0 && at > chipsAt) {
    fail("копия колонки хода встала после чипов: " + JSON.stringify(tt.children.map(dump)));
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

// --- подсказка колонки тикает тем же обходом: на ноутбуке возраст виден там ---
{
  const one = timers.find((t) => t.ms === 60000 && t.fn);
  const box = byClass(stageCell("XR-1"), "act2");
  const was = String(box.title);
  box.dataset.stageSince = String(Math.floor(Date.now() / 1000) - 7200);
  one.fn();
  await settle();
  const now2 = String(box.title);
  if (now2 === was) fail("минутный тик не тронул подсказку колонки: " + now2);
  if (!now2.includes("2 ч") || !now2.includes("разработка") || !now2.includes("сессия жива")) {
    fail("подсказка после тика собралась не та: " + JSON.stringify(now2));
  }
}

// --- пометка ожидания: значок часов и подсказка, открываемая нажатием ---
{
  const box = byClass(stageCell("XR-6"), "act2");
  const wrap = byClass(box, "mtip");
  if (!wrap) fail("пометка ожидания стоит без коробки подсказки: " + dump(box));
  const btn = byClass(wrap, "hg");
  if (!btn || btn.tagName !== "BUTTON") {
    fail("песочные часы это не кнопка, и нажать их нечем: " + JSON.stringify(btn && btn.tagName));
  }
  // Часы рисует значок разметки, а не рамка в стилях (замечание 5 приёмки
  // второго круга): узел значка стоит внутри кнопки своим классом.
  if (!byClass(btn, "gico")) fail("внутри пометки нет значка часов: " + dump(btn));
  const said = String(btn.attrs["aria-label"] || "");
  if (!said.includes("ждёт события")) {
    fail("подсказка пометки не называет причину остановки: " + JSON.stringify(said));
  }
  // Родной подсказки у кнопки нет: она задваивала бы свою коробку и перебивала
  // бы подсказку колонки с кругом, возрастом и состоянием сессии (замечание 10
  // ревью).
  if (btn.title) {
    fail("у пометки осталась родная подсказка браузера: " + JSON.stringify(btn.title));
  }
  const tipBox = byClass(wrap, "mtipbox");
  if (!tipBox || !dump(tipBox).includes("ждёт события")) {
    fail("коробка подсказки пуста: " + dump(wrap));
  }
  // Нажатие открывает подсказку и не уводит внутрь задачи: строка списка
  // кликабельна целиком.
  let stopped = false;
  btn.handlers.click({ stopPropagation: () => { stopped = true; } });
  if (!stopped) fail("нажатие на пометку уходит в строку и открывает задачу");
  if (!String(wrap.className).split(" ").includes("on")) {
    fail("нажатие не открыло подсказку: " + wrap.className);
  }
  if (String(btn.attrs["aria-expanded"]) !== "true") {
    fail("состояние подсказки не доехало до чтения с экрана: " + JSON.stringify(btn.attrs));
  }
  btn.handlers.click({ stopPropagation: () => {} });
  if (String(wrap.className).split(" ").includes("on")) {
    fail("второе нажатие не закрыло подсказку: " + wrap.className);
  }
}

// --- та же пометка в копии хода для телефона: она там и нужнее всего ---
{
  const narrow = byClass(byClass(rowOf("XR-6"), "tt"), "stage-narrow");
  const btn = byClass(narrow, "hg");
  if (!btn || btn.tagName !== "BUTTON" || !byClass(btn, "gico")) {
    fail("в копии хода для телефона пометка ожидания без значка или не нажимается: " +
      dump(narrow));
  }
}

// --- метка «вы» открывает подсказку тем же нажатием ---
{
  const box = byClass(stageCell("XR-4"), "act2");
  const btn = byClass(box, "you");
  if (!btn || btn.tagName !== "BUTTON") {
    fail("метка «вы» не нажимается: " + JSON.stringify(btn && btn.tagName));
  }
  if (!String(btn.attrs["aria-label"] || "").includes("ждёт человека")) {
    fail("подсказка метки «вы» не называет ожидание: " + JSON.stringify(btn.attrs));
  }
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
  const said = saidOf(now2);
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

  // Вид приёмки чипом «mixed, глаза», а чипов «ждёт вашей приёмки» и «агент
  // проверит сам» нет и на форме (замечание 9 приёмки).
  const bar = byClass(page, "tchips");
  if (!bar) fail("на форме задачи нет полосы чипов");
  const barSaid = dump(bar);
  if (!barSaid.includes("mixed, глаза")) {
    fail("на форме нет чипа вида приёмки с барьером: " + barSaid);
  }
  if (barSaid.includes("ждёт вашей приёмки") || barSaid.includes("агент проверит сам")) {
    fail("чип приёмки остался на форме: " + barSaid);
  }

  // Описание идёт без первой строки файла: заголовок стоит один раз, в шапке.
  const view = byClass(page, "fview");
  if (!view) fail("на форме задачи нет просмотра файла");
  const text = dump(view);
  if (text.includes("заголовок из файла")) {
    fail("первая строка файла напечатана второй копией заголовка: " + text);
  }
  if (!text.includes("Что происходит") || !text.includes("тело постановки")) {
    fail("вместе с заголовком пропало тело постановки: " + text);
  }
}

// --- форма машинного ожидания: значок часов в шапке, степпер на этапе работы ---
{
  await sandbox.renderTask("demo", [], "XR-6", null);
  await settle();
  const now2 = byClass(groups, "now2");
  if (!now2) fail("на форме ожидания нет шапки этапа");
  if (!String(now2.className).split(" ").includes("k-rev")) {
    fail("шапка формы у машинного ожидания красится не этапом работы: " + now2.className);
  }
  const btn = byClass(now2, "hgf");
  if (!btn || btn.tagName !== "BUTTON" || !byClass(btn, "gico")) {
    fail("в шапке формы пометка ожидания без значка часов или не нажимается: " + dump(now2));
  }
  if (!String(btn.attrs["aria-label"] || "").includes("ждёт события")) {
    fail("подсказка пометки на форме не называет причину: " + JSON.stringify(btn.attrs));
  }
  const step2 = byClass(groups, "step2");
  const on = step2.children.filter((x) => String(x.className).split(" ").includes("on"));
  if (on.length !== 1 || !dump(on[0]).includes("ревью")) {
    fail("степпер ожидания подсвечивает не этап работы: " +
      JSON.stringify(step2.children.map(dump)));
  }
}

console.log("poc_stagemark: ok");

// Стенд контроля строки (DK-1470): чип контроля это подсказка человеку, куда
// смотреть у срока, красным он стоит ровно у просрочки, а правило «дожать»
// поднимается только у просроченного контроля строки работы. Проверяется
// собранная разметка доски, а не текст исходника: у просроченной строки чип
// красный и подсказка зовёт на экран задачи, у будущей строки чип тихий и
// согласует дни, у строки без контроля чипа нет. Края правила (Check и Blocked
// живут своей механикой, будущий срок, строка без контроля) меряются прямыми
// вызовами, как коридор перетаскивания в стенде частичной перерисовки.
//
// Зовётся: node testdata/board_control.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const tomorrow = new Date(Date.now() + 86400000).toISOString().slice(0, 10);

const row = (id, extra) => Object.assign({
  id, title: "строка доски " + id, type: "task", p: "P2", r: 30,
  r_parts: [25, 2, 1, 0, 2], cost: "S", link: "-",
}, extra || {});

const board = {
  prefix: "XR",
  sections: [
    {
      key: "in-progress", title: "In progress", rows: [
        row("XR-1", { sect: "in-progress", due: "2020-01-01", control: 2 }),
        row("XR-3", { sect: "in-progress" }),
      ],
    },
    { key: "check", title: "Check", rows: [] },
    {
      key: "backlog", title: "Backlog", rows: [
        row("XR-2", { sect: "backlog", due: tomorrow, control: 3 }),
      ],
    },
  ],
};

const app = appPathArg();
const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/harnesses") return { harnesses: [{ name: "подписка-раз", default: true }] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
// app.js на загрузке сам зовёт refresh: даём ему дорисовать своё, иначе его
// отрисовка ляжет поверх нашей и стенд будет судить о чужом экране.
await settle();
sandbox.renderBoard("demo", board);

const rows = allByClass(groups, "trow");
const named = (id) => {
  const tr = rows.find((r) => dump(byClass(r, "id")).trim() === id);
  if (!tr) fail("строки " + id + " нет на доске: " + rows.map((r) => dump(byClass(r, "id"))).join(" | "));
  return tr;
};
const ctlChip = (tr) => allByClass(tr, "chip").find((c) => dump(c).startsWith("контроль: "));

// Просроченный контроль красный, и подсказка зовёт к кнопке дожима.
{
  const chip = ctlChip(named("XR-1"));
  if (!chip) fail("у просроченного контроля нет чипа: " + dump(named("XR-1")));
  if (!String(chip.className).includes("c-block")) fail("чип просрочки не красный: " + chip.className);
  if (!String(chip.title).includes("кнопка «дожать»")) {
    fail("подсказка просрочки не зовёт дожать: " + chip.title);
  }
}

// Контроль до окна тихий, а дни в подсказке согласованы с числом.
{
  const chip = ctlChip(named("XR-2"));
  if (!chip) fail("у будущего контроля нет чипа: " + dump(named("XR-2")));
  if (String(chip.className).includes("c-block")) fail("будущий контроль подан красным: " + chip.className);
  if (!String(chip.title).includes("за 3 дня до срока")) {
    fail("подсказка не согласовала дни: " + chip.title);
  }
}

// Без контроля чипа нет вовсе, тишина не мимикрирует под ноль дней.
if (ctlChip(named("XR-3"))) fail("у строки без контроля встал чип: " + dump(named("XR-3")));

// Края правила дожима прямыми вызовами: сектор, окно и наличие контроля.
{
  const hot = { control: 2, due: "2020-01-01" };
  if (!sandbox.controlOverdue(Object.assign({ sect: "in-progress" }, hot))) {
    fail("просроченный контроль строки работы не горит");
  }
  if (!sandbox.controlOverdue(Object.assign({ sect: "backlog" }, hot))) {
    fail("просроченный контроль очереди не горит");
  }
  if (sandbox.controlOverdue(Object.assign({ sect: "check" }, hot))) {
    fail("Check живёт своей механикой, дожим ему не положен");
  }
  if (sandbox.controlOverdue(Object.assign({ sect: "blocked" }, hot))) {
    fail("Blocked живёт своей механикой, дожим ему не положен");
  }
  if (sandbox.controlOverdue({ sect: "in-progress", control: 2, due: tomorrow })) {
    fail("будущий срок горит дожимом");
  }
  if (sandbox.controlOverdue({ sect: "in-progress", due: "2020-01-01" })) {
    fail("строка без контроля горит дожимом");
  }
  if (sandbox.controlChip({ due: "2020-01-01" }) !== null) {
    fail("у строки без контроля нарисовался чип контроля");
  }
}

console.log("доска: контроль красный у просрочки, тихий до окна, дожим только строкам работы");

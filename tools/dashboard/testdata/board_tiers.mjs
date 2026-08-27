// Стенд ярусов доски (ветка poc-chat): строка Backlog с незакрытым маркером
// «после DK-NNN» показывается в Blocked нижним ярусом, очередь остаётся про
// то, что можно запустить прямо сейчас, а с чипа держащей задачи есть ход на
// неё саму. Группировка живёт в клиенте: доска в git та же, статус строки
// прежний, и проверять тут надо собранную разметку, а не текст исходника.
//
// Зовётся: node testdata/board_tiers.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const row = (id, extra) => Object.assign({
  id, title: "строка доски " + id, type: "task", p: "P2", r: 30,
  r_parts: [25, 2, 1, 0, 2], cost: "S", link: "-",
}, extra || {});

// Доска стенда: две свободные строки очереди, две ждущие чужих задач (одна
// сразу двух), одна с маркером на закрытую задачу (её на доске нет, значит
// держать некому) и одна настоящая парковка с причиной.
const board = {
  prefix: "XR",
  sections: [
    { key: "in-progress", title: "In progress", rows: [row("XR-1", { sect: "in-progress" })] },
    {
      key: "backlog",
      title: "Backlog",
      rows: [
        row("XR-10", { sect: "backlog" }),
        row("XR-11", { sect: "backlog", after: ["XR-1"] }),
        row("XR-12", { sect: "backlog" }),
        row("XR-13", { sect: "backlog", after: ["XR-1", "XR-10"] }),
        row("XR-14", { sect: "backlog", after: ["XR-99"] }),
      ],
    },
    {
      key: "blocked",
      title: "Blocked",
      rows: [
        row("XR-20", {
          sect: "blocked", block: "ждём ответа смежников",
          waiting: { state: "ждёт человека", note: "вопрос агента", questions: ["чинить ли роутер"] },
        }),
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

const cards = allByClass(groups, "tsec");
const heads = allByClass(groups, "shead");
if (heads.length !== 3 || cards.length !== 3) {
  fail("секций на экране " + heads.length + ", тел таблицы " + cards.length + ", ждал по три");
}
// Секции берутся по имени, а не по месту в списке: порядок секций на экране
// правит человек (Blocked стоит выше Backlog), и стенд про ярусы от этого
// порядка зависеть не должен.
const at = (title) => heads.findIndex((h) => dump(h).trim().startsWith(title));
const seat = (title) => {
  const i = at(title);
  if (i < 0) fail("секции «" + title + "» нет на экране: " + heads.map(dump).join(" | "));
  return { head: heads[i], card: cards[i] };
};
const runHead = seat("In progress").head;
const { head: backHead, card: backCard } = seat("Backlog");
const { head: blockHead, card: blockCard } = seat("Blocked");
if (at("Blocked") > at("Backlog")) {
  fail("Blocked встал ниже Backlog: парковки прячутся под очередью");
}
const ids = (card) => allByClass(card, "trow").map((tr) => dump(byClass(tr, "id")).trim());

// --- очередь: только то, что можно запустить ---
if (ids(backCard).join(",") !== "XR-10,XR-12,XR-14") {
  fail("в очереди остались ждущие чужих задач строки: " + ids(backCard).join(","));
}
// Маркер на задачу, которой на доске нет, никого не держит: она закрыта и
// уехала в архив, а строка доступна к запуску.
if (!ids(backCard).includes("XR-14")) {
  fail("строка с маркером на закрытую задачу пропала из очереди: " + ids(backCard).join(","));
}
if (!dump(backHead).includes("3")) {
  fail("счётчик очереди врёт: " + dump(backHead));
}
if (!dump(backHead).includes("по рангу")) fail("очередь перестала называть свой порядок");
if (!dump(runHead).includes("1")) fail("счётчик работы съехал: " + dump(runHead));

// --- Blocked: два яруса, парковки сверху ---
if (ids(blockCard).join(",") !== "XR-20,XR-11,XR-13") {
  fail("ярусы Blocked собраны не тем порядком: " + ids(blockCard).join(","));
}
if (!dump(blockHead).includes("3")) fail("счётчик Blocked врёт: " + dump(blockHead));
const tiers = allByClass(blockCard, "btier");
if (tiers.length !== 2) fail("ярусы не подписаны: " + tiers.length);
if (!dump(tiers[0]).includes("ждут человека") || !dump(tiers[1]).includes("ждут задач")) {
  fail("подписи ярусов не те: " + tiers.map(dump).join(" | "));
}
if (!dump(tiers[0]).includes("1") || !dump(tiers[1]).includes("2")) {
  fail("счётчики ярусов врут: " + tiers.map(dump).join(" | "));
}
// Ярус человека тихим не бывает: там и правда ждут, и подача у него прежняя.
if (String(tiers[0].className).includes("quiet")) fail("ярус человека подан тихо");
if (!String(tiers[1].className).includes("quiet")) fail("ярус задач подан наравне с парковками");

const rows = allByClass(blockCard, "trow");
const parked = rows[0];
const held = rows[1];
if (String(parked.className).includes("rwait")) fail("настоящая парковка подана тихой строкой");
if (!String(held.className).includes("rwait")) fail("ждущая задач строка подана наравне с парковкой");
// Парковка остаётся с чипом причины и ожидания: подача яруса человека прежняя.
if (!dump(parked).includes("ждём ответа смежников")) {
  fail("парковка потеряла причину: " + dump(parked));
}

// Ярус один на всю секцию, и подпись у него всё равно есть: иначе тихие
// строки под словом Blocked не говорят, чего ждут (живая доска сейчас ровно
// такая, парковок на ней нет).
{
  const only = { prefix: "XR", sections: board.sections.map((sec) => (sec.key === "blocked"
    ? { key: "blocked", title: "Blocked", rows: [] } : sec)) };
  sandbox.renderBoard("demo", only);
  const card = allByClass(groups, "tsec")[
    allByClass(groups, "shead").findIndex((h) => dump(h).trim().startsWith("Blocked"))];
  const one = allByClass(card, "btier");
  if (one.length !== 1 || !dump(one[0]).includes("ждут задач")) {
    fail("одинокий ярус остался без подписи: " + one.map(dump).join(" | "));
  }
  sandbox.renderBoard("demo", board);
}

// --- чип держащей задачи ведёт на неё ---
const chips = allByClass(held, "chip").filter((c) => dump(c).startsWith("после "));
if (chips.length !== 1 || dump(chips[0]) !== "после XR-1") {
  fail("чипа держащей задачи нет: " + dump(held));
}
if (chips[0].tagName !== "BUTTON") fail("чип держащей задачи не кнопка: " + chips[0].tagName);
chips[0].handlers.click({ stopPropagation: () => {} });
await settle();
if (!sandbox.location.hash.includes("demo/XR-1")) {
  fail("чип не увёл на держащую задачу: " + sandbox.location.hash);
}

// Держащих несколько, значит и чипов столько же: каждая своя дорога.
const many = allByClass(rows[2], "chip").filter((c) => dump(c).startsWith("после "));
if (many.length !== 2 || dump(many[1]) !== "после XR-10") {
  fail("на две держащие задачи чип один: " + many.map(dump).join(", "));
}

// --- запускать ждущую задач строку нечем ---
// Главной кнопкой такой строки стоит запуск, и он же несёт причину: подписи у
// него нет вовсе, значок с подсказкой, а имя приезжает подписью для чтения с
// экрана.
const btn = allByClass(held, "btn").find((b) => b.attrs["aria-label"] === "Выполнить") || null;
if (!btn) fail("у ждущей строки пропала кнопка с причиной: " + dump(held));
if (!btn.disabled) fail("ждущую чужой задачи строку предлагают запустить");

// --- очередь пустеет честно ---
// Ждущие задач строки ушли все: в очереди без них не остаётся ни одной с
// маркером на живую задачу.
for (const tr of allByClass(backCard, "trow")) {
  const chip = allByClass(tr, "chip").find((c) => dump(c).startsWith("после "));
  if (chip && dump(chip) !== "после XR-99") {
    fail("в очереди осталась строка с маркером на живую задачу: " + dump(tr));
  }
}

console.log("доска: очередь без ждущих, два яруса Blocked, чип держащей задачи ведёт на неё");

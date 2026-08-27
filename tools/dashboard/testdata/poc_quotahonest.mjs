// Стенд честного показа остатка подписок (ветка poc-chat).
//
// Цифры двух подписок стоят рядом и читаются как одна картина на один момент, а
// сняты бывают в разное время: claude-code снимает тик демона, а у второй
// подписки съёмщика не было вовсе, и её рукописный снимок трёхчасовой давности
// выглядел таким же свежим (замечание пользователя). Предмет стенда: имя окна в
// показе, подписка без снимка названа словами, разъезд снимков назван словами и
// в блоке квоты, и в списке выбора подписки при запуске.
//
// Зовётся: node testdata/poc_quotahonest.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: [{ tier: "pro", model: "opus" }] },
  { name: "glm-code", bin: "glm", models: [{ tier: "pro", model: "glm-5.3" }] },
];

// Снимки: свежий у первой подписки, трёхчасовой у второй.
const quota = {
  dir: "/дом/.devkit/quota",
  harnesses: [
    { name: "claude-code", age: "2 мин", age_sec: 120, buckets: [
      { name: "week_all", used_pct: 6, reset: "2026-08-31T15:00" },
      { name: "week_max", used_pct: 0, reset: "2026-08-31T15:00" },
    ] },
    { name: "glm-code", age: "3 ч", age_sec: 3 * 3600, buckets: [
      { name: "window5h_all", used_pct: 7, reset: "2026-08-24T21:01" },
      { name: "week_all", used_pct: 31, reset: "2026-08-29T20:52" },
    ] },
  ],
  // Поле разъезда сервер больше не шлёт вовсе, но стенд кладёт его нарочно:
  // клиент обязан его не рисовать, даже когда оно приехало от старой сборки.
  spread: "снимки сняты в разное время: claude-code 2 мин назад, glm-code 3 ч назад",
};

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/quota") return quota;
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

await settle();
await sandbox.loadHarnesses();
await sandbox.refreshQuota();
await settle();

const box = byId.get("quota");
const said = () => dump(box).replace(/\s+/g, " ");

// --- имя окна режется в показе, ключ данных не трогается ---
{
  const names = allByClass(box, "qrow").map((r) => (byTag(r) || {}).textContent);
  if (!names.includes("5h_all")) {
    fail("имя окна не сократилось в показе: " + JSON.stringify(names));
  }
  if (names.includes("window5h_all")) {
    fail("в показе осталось длинное имя окна: " + JSON.stringify(names));
  }
  // Полное имя остаётся подсказкой: ключ снимка человек читает, когда лезет в
  // сам файл.
  const row = allByClass(box, "qrow").find((r) => dump(r).includes("5h_all"));
  if (!String(row.children[0].title || "").includes("window5h_all")) {
    fail("полное имя окна пропало вовсе: " + JSON.stringify(row.children[0].title));
  }
  // Прочие имена не режутся: приставка снимается только у окна.
  if (!names.includes("week_all")) fail("недельный бакет переименовался: " + JSON.stringify(names));
}

function byTag(node) {
  for (const kid of node.children || []) {
    if (kid.tagName === "EM") return kid;
  }
  return null;
}

// --- давность подписана у каждой подписки, слов о разъезде нет ---
//
// Сперва разъезд стоял красным абзацем на весь блок и вытеснял из колонки сами
// цифры, потом припиской «раньше остальных» у старшего снимка. Ни того ни
// другого больше нет: давность у каждой подписки подписана цифрой, сравнение
// человек делает сам, а лишнее слово путало (решение пользователя).
{
  const shown = said();
  // Цифры обеих подписок на месте.
  for (const want of ["claude-code", "6%", "0%", "glm-code", "7%", "31%"]) {
    if (!shown.includes(want)) fail("из блока пропало «" + want + "»: " + shown);
  }
  if (allByClass(box, "qrow").length !== 4) {
    fail("полос остатка не четыре: " + allByClass(box, "qrow").length + ", " + shown);
  }
  // Давность подписана у каждой подписки своя.
  if (!shown.includes("снимок 2 мин назад") || !shown.includes("снимок 3 ч назад")) {
    fail("давность снимков подписана не у каждой подписки: " + shown);
  }
  // Слов о разъезде нет ни в каком виде: ни плашкой, ни припиской.
  if (shown.includes("раньше остальных") || shown.includes("разное время")) {
    fail("слова о разъезде снимков вернулись на экран: " + shown);
  }
  if (allByClass(box, "qfail").length) {
    fail("разъезд снимков снова стоит плашкой отказа: " + shown);
  }
  // Давность у каждой подписки своя и стоит цифрой.
  const ages = allByClass(box, "qage").map((n) => dump(n).trim());
  if (ages.length !== 2 || !ages.join(" ").includes("2 мин") || !ages.join(" ").includes("3 ч")) {
    fail("давность подписана не у каждой подписки: " + JSON.stringify(ages));
  }
}

// --- подписка машины без снимка названа словами, а не пропущена ---
//
// Список снимков собирается по файлам каталога: подписка, у которой съёмщика
// нет, не попадала в него ни строкой, и экран о ней молчал.
{
  quota.harnesses = [quota.harnesses[0]];
  await sandbox.refreshQuota();
  await settle();
  if (!said().includes("glm-code")) fail("подписка без снимка пропала из блока: " + said());
  if (!said().includes("снимка нет")) {
    fail("подписка без снимка молчит вместо слов: " + said());
  }
  // Ноль тут не рисуется: остаток неизвестен, а нарисованный ноль читался бы
  // как «квота цела».
  const rows = allByClass(box, "qrow").map((r) => dump(r));
  if (rows.some((r) => r.includes("glm"))) fail("у подписки без снимка нарисован остаток");
}

// --- выбор подписки при запуске опирается на ту же картину ---
{
  const row = sandbox.harnessRow({ name: "glm-code" });
  const text = dump(row).replace(/\s+/g, " ");
  if (!text.includes("снимка")) {
    fail("строка выбора подписки не говорит про снимок: " + text);
  }
  if (!byClass(row, "stale")) fail("строка выбора не помечена как неизвестный остаток: " + text);

  // В списке выбора та же мера: давность цифрой, слов о разъезде нет.
  quota.harnesses = [quota.harnesses[0], { name: "glm-code", age: "3 ч", age_sec: 3 * 3600,
    buckets: [{ name: "window5h_all", used_pct: 7 }] }];
  await sandbox.refreshQuota();
  await settle();
  const node = sandbox.harnessRow({ name: "glm-code" });
  const second = dump(node).replace(/\s+/g, " ");
  if (second.includes("раньше остальных") || second.includes("разное время")) {
    fail("список выбора подписки заговорил о разъезде: " + second);
  }
  // Возраст снимка со строки ушёл в подсказку: строка держит одну полосу, и
  // место на ней занимают имя с остатком (замечание пользователя про жирный
  // блок выбора). Пропасть возраст при этом не имеет права, подписку выбирают
  // по свежему остатку.
  if (!String(node.title || "").includes("снимок 3 ч назад")) {
    fail("возраст снимка в списке выбора пропал: " + node.title);
  }
  if (!second.includes("7%")) fail("выбор подписки потерял остаток: " + second);
}

console.log("poc_quotahonest: ok");

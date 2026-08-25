// Стенд давности строки сессии (ветка poc-chat), таб сессий доски.
//
// Живой случай: разговор «Распространить паттерн диспетчера-агента на все чаты»
// стоял на экране простаивающим одну минуту, хотя работы в нём не было больше
// трёх суток. Давность бралась по времени правки файла транскрипта, а файл
// трогают и при мёртвом содержимом. Сервер теперь считает её по последней
// содержательной реплике и отдельным признаком говорит, что реплик не нашлось
// вовсе; предмет стенда в том, как строка это произносит.
//
// Зовётся: node testdata/poc_saidage.mjs static/app.js

import { readFileSync } from "node:fs";
import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const hour = 3600;
const ago = (sec) => Math.floor(Date.now() / 1000) - sec;

const works = [
  // Разговор с репликами: последняя сказана трое суток назад, а файл трогали
  // минуту назад. Сервер отдаёт время реплики, и строка обязана говорить о нём.
  { kind: "session", via: "session", session: "aaaa1111-1111", note: "брошенный разговор",
    own: true, live: "idle", moved: ago(75 * hour) },
  // Разговор, где содержательных реплик нет вовсе: время тут не ход, а начало
  // разговора, и выдавать его за ход нельзя.
  { kind: "session", via: "session", session: "bbbb2222-2222", note: "разговор без реплик",
    own: true, live: "idle", moved: ago(75 * hour), silent: true },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
sandbox.location.hash = "#/agents";
await sandbox.refresh();
await settle();

const rows = () => allByClass(groups, "arow");
const rowOf = (what) => rows().find((r) => dump(r).includes(what));

// --- разговор с репликами: давность по реплике, а не по касанию файла ---
{
  const row = rowOf("брошенный разговор");
  if (!row) fail("строки брошенного разговора на экране нет: " + dump(groups).slice(0, 300));
  const said = dump(row);
  if (!/простаивает\s+7[45] ч/.test(said)) {
    fail("давность строки не по реплике: " + said);
  }
  if (/простаивает\s+\d+ (с|мин)\b/.test(said)) {
    fail("строка меряет простой касанием файла: " + said);
  }
}

// --- разговор без реплик: строка говорит об этом словами ---
{
  const row = rowOf("разговор без реплик");
  if (!row) fail("строки разговора без реплик на экране нет: " + dump(groups).slice(0, 300));
  const said = dump(row);
  if (!said.includes("реплик нет")) {
    fail("разговор без реплик не назван таковым: " + said);
  }
  // Время начала разговора в строку не идёт: за ход его читать нельзя, место
  // ему в подсказке.
  if (/простаивает\s+\d+ ч/.test(said)) {
    fail("начало разговора без реплик выдано за давность хода: " + said);
  }
  const chip = allByClass(row, "chip").find((c) => String(c.textContent || "").includes("реплик нет"));
  if (!chip) fail("слова про реплики стоят мимо чипа состояния: " + said);
  const tip = String(chip.title || "");
  if (!tip.includes("реплик") || !/7[45] ч/.test(tip)) {
    fail("подсказка не сказала, когда разговор заведён: " + JSON.stringify(tip));
  }
  const dot = byClass(row, "dot");
  if (!dot || !String(dot.title || "").includes("реплик")) {
    fail("подсказка кружка молчит о том, что реплик нет: " + JSON.stringify(dot && dot.title));
  }
}

console.log("poc_saidage: строка сессии мерит простой репликами, а не касанием файла");

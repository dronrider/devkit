// Стенд яруса разбора черновика (ветка poc-chat).
//
// Разбор поднимался клиентом без модели вовсе, то есть шёл дефолтом самого
// клиента: у пользователя это верхний ярус, самая дорогая подписка, которую он
// не выбирал (замечание пользователя). Ярус теперь назван: по умолчанию pro, а
// другой берут осознанно тем же списком, в котором выбирают подписку. Про саму
// команду отвечает сервер (TestDraftGroomTier), а предмет этого стенда кнопка:
// что уезжает в заказ и откуда берётся список ярусов.
//
// Зовётся: node testdata/poc_groomtier.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku" }, { tier: "base", model: "sonnet" },
  { tier: "pro", model: "opus" }, { tier: "max", model: "fable" }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm", models: [{ tier: "base", model: "glm-5.3" }, { tier: "pro", model: "glm-5.3" }] },
];

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-D1", session: "task-XR-D1", message: "груминг поднят", tier: "pro" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

const draft = { id: "XR-D1", title: "мысль с телефона", age_words: "вчера" };
const rowOf = () => sandbox.draftRow("demo", draft);

// --- ярусы берутся из раскладки машины, а не из головы ---
{
  const names = sandbox.harnessTiers();
  if (JSON.stringify(names) !== JSON.stringify(["mini", "base", "pro", "max"])) {
    fail("список ярусов собран не по раскладке машины: " + JSON.stringify(names));
  }
}

// --- в списке кнопки разбора есть выбор яруса, и pro в нём выбран ---
{
  const row = rowOf();
  const pop = byClass(row, "hpop");
  if (!pop) fail("у кнопки разбора нет списка выбора: " + dump(row));
  const picks = allByClass(pop, "tpick");
  if (picks.length !== 4) fail("ярусов в списке " + picks.length + ", ждал четыре: " + dump(pop));
  const on = picks.filter((p) => String(p.className).includes("on")).map((p) => dump(p).trim());
  if (JSON.stringify(on) !== JSON.stringify(["pro"])) {
    fail("по умолчанию выбран не pro: " + JSON.stringify(on));
  }
  if (!dump(pop).includes("Ярус: pro")) {
    fail("подвал списка не говорит, каким ярусом поедет разбор: " + dump(pop));
  }
}

// --- широкая половина поднимает разбор ярусом pro ---
{
  const row = rowOf();
  const wide = deepBtn(row, "Провести груминг");
  wide.handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.includes("/groom")) fail("разбор не поднялся: " + JSON.stringify(posted));
  if (!last.body || last.body.tier !== "pro") {
    fail("в заказ разбора не уехал ярус по умолчанию: " + JSON.stringify(last.body));
  }
}

// --- выбранный ярус уезжает в заказ вместе с подпиской ---
{
  const row = rowOf();
  const pop = byClass(row, "hpop");
  const base = allByClass(pop, "tpick").find((p) => dump(p).trim() === "base");
  base.handlers.click({ stopPropagation: () => {} });
  await settle();
  // Выбор яруса сам работы не поднимает: он отвечает на другой вопрос.
  const was = posted.length;
  if (posted.length !== was) fail("выбор яруса сам поднял разбор");
  const pick = allByClass(pop, "hrow").find((h) => dump(h).includes("glm-code"));
  if (!pick) fail("в списке нет второй подписки: " + dump(pop));
  pick.handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last.body || last.body.tier !== "base" || last.body.harness !== "glm-code") {
    fail("выбранные ярус с подпиской не доехали до заказа: " + JSON.stringify(last.body));
  }
}

// --- выбор яруса остаётся и с одной подпиской на машине ---
{
  harnesses.length = 1;
  await sandbox.loadHarnesses();
  const row = rowOf();
  const pop = byClass(row, "hpop");
  if (!pop) fail("с одной подпиской список выбора пропал вместе с ярусами: " + dump(row));
  if (dump(pop).includes("На какой подписке")) {
    fail("с одной подпиской список всё равно спрашивает про подписку: " + dump(pop));
  }
  if (allByClass(pop, "tpick").length !== 4) {
    fail("ярусов с одной подпиской не осталось: " + dump(pop));
  }
}

console.log("poc_groomtier: ok");

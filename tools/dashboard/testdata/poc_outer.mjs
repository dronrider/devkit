// Стенд слов про отказ внешнего входа (ветка poc-chat).
//
// Внешний вход стоит на сервере, дашборд на машине человека, и своим 502 вход
// говорит, что связи с этой машиной нет. Человеку доставался абзац с перебором
// причин («ноутбук мог уснуть, сеть моргнуть, а сам дашборд перезапускаться»),
// и причины эти он назвал лишними (замечание пользователя). Предмет стенда:
// сказан один факт про отсутствие связи, догадок нет вовсе, 502 и 504 разведены
// одним словом, а про судьбу набранного сказано короткой фразой и только там,
// где она правда.
//
// Зовётся: node testdata/poc_outer.mjs static/app.js

import { readFileSync } from "node:fs";
import { makeSandbox, settle, dump, byClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Страница отказа, какой её пишет внешний вход: html, а не JSON.
const page = (code, what) => "<html><head><title>" + code + " " + what +
  "</title></head><body><center><h1>" + code + " " + what +
  "</h1></center><hr><center>nginx</center></body></html>";

let gate = 0;
const asked = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") asked.push(path);
  if (gate && path.endsWith("/say")) {
    return { raw: { status: gate, statusText: gate === 504 ? "Gateway Time-out" : "Bad Gateway",
      text: page(gate, gate === 504 ? "Gateway Time-out" : "Bad Gateway") } };
  }
  return {};
});

const live = () => ({
  addr: "DK-397", sid: "aaaa1111-1111", task: "DK-397", chats: [],
  entry: { id: "aaaa1111-1111", state: "live", tasks: ["DK-397"], model: "opus" },
  models: [], fresh: false, error: "", note: "",
});

// --- одиночный отказ связи молчит: реплику дожимает очередь ---
// Про первый неудачный заход человеку сообщать нечего: это штатная жизнь
// ноутбука, а пузырь и так помечен и держит кнопку повтора (замечание
// пользователя про уведомление о штатной ситуации).
{
  gate = 502;
  asked.length = 0;
  const panel = sandbox.chatPanel("demo", live());
  tag(panel, "TEXTAREA").value = "посмотри ленту";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const quiet = dump(sandbox.document.getElementById("flashes")).replace(/\s+/g, " ");
  if (quiet.trim()) fail("одиночный отказ связи родил уведомление: " + quiet.slice(0, 200));
  if (!dump(byClass(panel, "mlocal")).includes("посмотри ленту")) {
    fail("пузырь неушедшей реплики пропал: " + dump(byClass(panel, "mlocal")));
  }
}

// --- устойчивый отказ доходит до человека объяснением, а не кодом ---
{
  gate = 502;
  asked.length = 0;
  const tries = Number((readFileSync(app, "utf8").match(/const LOST_TRIES = (\d+)/) || [])[1]);
  if (!tries) fail("порога молчания нет в статике: константы LOST_TRIES не нашлось");
  const panel = sandbox.chatPanel("demo", live());
  for (let i = 0; i < tries; i += 1) {
    tag(panel, "TEXTAREA").value = "посмотри ленту";
    deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
    await settle();
  }
  const said = dump(sandbox.document.getElementById("flashes")).replace(/\s+/g, " ");
  if (!asked.some((p) => p.endsWith("/say"))) fail("реплика не уходила вовсе: " + JSON.stringify(asked));
  // Сказано, с кем нет связи, и сказано это одним утверждением о факте.
  if (!said.includes("дашбордом на вашем компьютере")) {
    fail("отказ не сказал, с кем нет связи: " + said);
  }
  // Догадок о причинах нет ни в строке отказа, ни в подсказке рядом.
  if (/уснуть|уснул|моргнуть|моргнула|перезапуск|занята|еле дышит/.test(said)) {
    fail("в отказе остались догадки о причинах: " + said);
  }
  if (said.includes("не дозвался дашборда")) fail("вернулись прежние слова: " + said);
  if (said.includes("<")) fail("в слова про отказ уехала разметка: " + said);
  // Хвоста про судьбу набранного в отказе нет: он не влезал в строку, а
  // неушедшая реплика видна пузырём в ленте (замечание пользователя).
  if (/Реплика|Набранное|Экран покажет/.test(said)) {
    fail("к отказу вернулся хвост про набранное: " + said);
  }
  // И пузырь с набранным на месте, а не пропал вместе с отказом.
  const pend = byClass(panel, "mlocal");
  if (!dump(pend).includes("посмотри ленту")) {
    fail("пузырь неушедшей реплики пропал: " + dump(pend));
  }
}

// --- 504 говорит своё: достучались, но не дождались ---
{
  const bad = sandbox.outerFail(502, "Bad Gateway", page(502, "Bad Gateway"),
    "/api/projects/demo/chats/aaaa1111-1111/say", "POST");
  const slow = sandbox.outerFail(504, "Gateway Time-out", page(504, "Gateway Time-out"),
    "/api/projects/demo/chats/aaaa1111-1111/say", "POST");
  if (bad === slow) fail("502 и 504 сказаны одними словами: " + bad);
  if (!bad.includes("502") || !slow.includes("504")) {
    fail("код потерялся из слов: " + bad + " / " + slow);
  }
  if (!slow.includes("истёк срок ожидания")) {
    fail("504 не сказал про истёкший срок ожидания: " + slow);
  }
  for (const line of [bad, slow]) {
    if (!line.includes("не удалось установить связь с дашбордом на вашем компьютере")) {
      fail("отказ сказан не одной строкой про отсутствие связи: " + line);
    }
    if (/уснуть|моргнуть|перезапуск|занят|еле дышит|попробуйте ещё раз/.test(line)) {
      fail("в отказе остались догадки или прежнее «попробуйте ещё раз»: " + line);
    }
    // Строка короткая: одно утверждение о факте и ничего сверх него.
    if (line.length > 95) fail("отказ разросся длиннее одной строки: " + line);
  }
}

// --- отказ один на все запросы: обещаний про набранное в нём нет ---
{
  // Реплика, отправка формы и чтение экрана получают одну и ту же строку:
  // обещать дожим и сохранность набранного она не берётся, судьба реплики
  // видна в ленте пузырём (замечание пользователя про лишние слова).
  const say = sandbox.outerFail(502, "Bad Gateway", "");
  const post = sandbox.outerFail(502, "Bad Gateway", "");
  const get = sandbox.outerFail(504, "Gateway Time-out", "");
  if (say !== post) fail("реплике и форме сказано разное: " + say + " / " + post);
  for (const line of [say, post, get]) {
    if (/Реплика|Набранное|Экран|сама|потеряно/.test(line)) {
      fail("в отказе осталось обещание про набранное: " + line);
    }
  }
  if (!/\(502\)\.$/.test(say)) fail("отказ 502 не кончается кодом: " + say);
  if (!/\(504\)\.$/.test(get)) fail("отказ 504 не кончается кодом: " + get);
}

// --- незнакомый код по-прежнему берётся кодом со статусом ---
{
  const said = sandbox.outerFail(500, "Internal Server Error",
    page(500, "Internal Server Error"), "/api/projects", "GET");
  if (!said.includes("500")) fail("незнакомый код потерялся: " + said);
  if (said.includes("<")) fail("в слова уехала разметка: " + said);
}

console.log("poc_outer: отказ внешнего входа сказан одним фактом без догадок, " +
  "502 и 504 разведены одним словом, обещаний про набранное в строке нет");

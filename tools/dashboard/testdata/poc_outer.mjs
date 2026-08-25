// Стенд слов про отказ внешнего входа (ветка poc-chat).
//
// Внешний вход стоит на сервере, дашборд на машине человека, и своим 502 вход
// говорит, что до этой машины не достучался. Человеку доставалось «внешний
// вход не дозвался дашборда (502): попробуйте ещё раз»: ни кто кому не
// дозвался, ни куда делось написанное (замечание пользователя). Предмет
// стенда: причина сказана словами, 502 и 504 разведены, и про судьбу реплики
// сказано там, где реплика была.
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
  // Названы обе стороны: ворота на сервере и дашборд на машине человека.
  if (!said.includes("дашборда на вашей машине")) {
    fail("отказ не сказал, до кого не достучались: " + said);
  }
  // Названа причина житейскими словами, а не одним кодом.
  if (!/уснуть|моргнуть|перезапуск/.test(said)) {
    fail("отказ не назвал ни одной причины: " + said);
  }
  if (said.includes("не дозвался дашборда")) fail("вернулись прежние слова: " + said);
  if (said.includes("<")) fail("в слова про отказ уехала разметка: " + said);
  // Судьба набранного сказана прямо: реплика осталась в панели и уйдёт сама.
  if (!said.includes("не потеряна")) fail("про судьбу реплики не сказано: " + said);
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
  if (!/не дождался|не дождались/.test(slow)) {
    fail("504 не сказал, что ответа не дождались: " + slow);
  }
  if (!/занят|еле дышит|связь/.test(slow)) fail("504 не назвал причины: " + slow);
  for (const line of [bad, slow]) {
    if (line.includes("попробуйте ещё раз") && !line.includes("не потеряна")) {
      fail("отказ отделался прежним «попробуйте ещё раз»: " + line);
    }
  }
}

// --- про повтор сказано то, что правда для этого запроса ---
{
  // Реплику панель дожимает сама, и про неё сказано, что она не потеряна.
  const say = sandbox.outerFail(502, "Bad Gateway", "",
    "/api/projects/demo/chats/aaaa1111-1111/say", "POST");
  if (!say.includes("уйдёт повтором сама")) fail("про дожим реплики не сказано: " + say);
  // Прочая отправка дожима не имеет: там повтор за человеком, и обещать за
  // него нельзя.
  const post = sandbox.outerFail(502, "Bad Gateway", "", "/api/projects/demo/tasks", "POST");
  if (post.includes("сама")) fail("дашборд обещал дожать то, чего не дожимает: " + post);
  if (!post.includes("Набранное не потеряно")) fail("про форму не сказано: " + post);
  // Чтение экрана повторится следующим заходом, набранного там нет вовсе.
  const get = sandbox.outerFail(504, "Gateway Time-out", "", "/api/projects", "GET");
  if (get.includes("Набранное") || get.includes("Реплика")) {
    fail("у чтения экрана нашлось набранное: " + get);
  }
  if (!get.includes("Экран")) fail("про экран не сказано: " + get);
}

// --- незнакомый код по-прежнему берётся кодом со статусом ---
{
  const said = sandbox.outerFail(500, "Internal Server Error",
    page(500, "Internal Server Error"), "/api/projects", "GET");
  if (!said.includes("500")) fail("незнакомый код потерялся: " + said);
  if (said.includes("<")) fail("в слова уехала разметка: " + said);
}

console.log("poc_outer: отказ внешнего входа назван словами, 502 и 504 разведены, " +
  "судьба набранного сказана по месту");

// Стенд панели, открытой по заблокированной задаче без разговоров (ветка
// poc-chat). Разбор снимка пользователя по DK-466, три замечания разом.
//
// Первое: в шапке горел красный моргающий кружок с цифрой 1. Это ожидание самой
// строки, живых сессий за ней нет ни одной, и читалось оно тревогой без
// объяснения. Блокировка это состояние, а не событие: кольцо больше не моргает,
// а причина блока стоит рядом словами.
//
// Второе: панель говорила о пустоте дважды, заголовком «Чатов пока нет» и
// припиской в ленте «чатов тут пока нет, заведите новый кнопкой +». Второе
// сообщение рассчитано на пустой список, а не на открытую панель задачи.
//
// Третье: поле ввода звало «Ответ задаче DK-466...». Плашка казённая, и место
// ей нейтральное, как в обычном чате.
//
// Зовётся: node testdata/poc_taskpanel.mjs static/app.js

import { makeSandbox, settle, tag, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const BLOCK = "нужен ответ пользователя про раскладку правил";
const board = { sections: [{ key: "blocked", rows: [
  { id: "DK-466", title: "паттерн диспетчера-агента", sect: "blocked", block: BLOCK },
] }] };

// Разговоров по задаче нет ни одного, а строка стоит с вопросом: ждёт она сама,
// и живых сессий за ней ноль.
const pulse = { task: "DK-466", state: "waiting", scale: "", flow: false,
  count: 0, working: 0, waiting: 1, parked: true, block: BLOCK };

const { sandbox } = makeSandbox(app, (path) => {
  if (path.includes("/pulse")) return pulse;
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

const st = await sandbox.chatState("demo", "DK-466", board);
// Шапка и лента это два соседних узла панели: кольцо с названием живут в первом,
// поле ввода и сама лента во втором.
const head = sandbox.chatHead("demo", st);
const panel = sandbox.chatPanel("demo", st);
await settle();
const said = dump(head) + " " + dump(panel);

// --- кружок больше не моргает и не носит цифры ---
{
  const ring = byClass(head, "ringwrap");
  if (!ring) fail("кольца в шапке панели нет вовсе: " + said.slice(0, 400));
  const cls = String(ring.className || "").split(" ");
  if (!cls.includes("parked")) {
    fail("ожидание строки помечено как ожидание агента, ореол моргает: " + ring.className);
  }
  if (allByClass(ring, "rnum").length) {
    fail("в середине кольца снова стоит число, хотя ждущих агентов нет ни одного");
  }
}

// --- чем задача заблокирована, сказано словами ---
{
  const chip = allByClass(head, "chip").find((c) => String(c.textContent || "").includes("блок"));
  if (!chip) fail("про блокировку не сказано ни слова: " + said.slice(0, 400));
  const full = String(chip.textContent || "") + " " + String(chip.title || "");
  if (!full.includes(BLOCK)) {
    fail("чип блока не назвал причину: " + JSON.stringify(full));
  }
}

// --- о пустоте говорят один раз, а не два ---
{
  if (said.includes("чатов тут пока нет")) {
    fail("приписка пустого списка стоит внутри открытой панели: " + said);
  }
  if (said.includes("Чатов пока нет")) {
    fail("заголовок панели говорит о пустоте вместо имени задачи: " + said);
  }
  if (!said.includes("DK-466")) {
    fail("панель не назвала задачу, по которой открыта: " + said);
  }
}

// --- плашка поля нейтральная ---
{
  const ta = tag(panel, "TEXTAREA");
  if (!ta) fail("поля ввода в панели нет: " + said.slice(0, 400));
  const hint = String(ta.placeholder || "");
  if (hint.includes("Ответ задаче")) {
    fail("казённая плашка поля осталась: " + JSON.stringify(hint));
  }
  if (hint !== "Написать агенту...") {
    fail("плашка поля не нейтральная: " + JSON.stringify(hint));
  }
}

console.log("poc_taskpanel: кольцо не моргает над блоком, причина сказана словами, " +
  "о пустоте говорится один раз, плашка поля нейтральная");

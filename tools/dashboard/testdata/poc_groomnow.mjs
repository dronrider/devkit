// Стенд запуска разбора без подтверждения (ветка poc-chat).
//
// Прежде «Грумить» открывала карточку «Поднимется 3 сессии разбора...»
// с кнопками «Поднять 3» и «Отмена», а исходная кнопка оставалась доступной:
// нажать её можно было поверх открытого вопроса. Пользователь спросил, нужен ли
// этот шаг вообще, и ответил сам: выбор записей отметками это и есть осознанное
// действие, второй вопрос поверх него лишний.
//
// Предмет стенда обратный прежнему: подтверждения нет вовсе, число выбранных
// стоит в подписи кнопки, на время подъёма кнопка гаснет, выбор после подъёма
// снимается, а про пропущенные записи сказано строкой итога.
//
// Зовётся: node testdata/poc_groomnow.mjs static/app.js

import { makeSandbox, settle, dump, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "pro", model: "opus" }];
const harnesses = [{ name: "claude-code", bin: "claude", default: true, models: tiers }];

const posted = [];
let hold = null;
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    // Ответ ручки разбора можно придержать: гашение кнопки видно только пока
    // подъём идёт.
    if (hold && path.includes("/groom")) return hold;
    return { id: "XR-D1", session: "task-XR-D1", message: "груминг поднят" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

// Узел строк итога заводится самим экраном, и до первой строки его может не
// быть вовсе.
const flashBox = () => sandbox.document.getElementById("flashes");
const flashes = () => (flashBox() ? dump(flashBox()).replace(/\s+/g, " ") : "");
const flashClear = () => { const box = flashBox(); if (box) box.replaceChildren(); };
const clearPicks = () => {
  for (const id of ["XR-D1", "XR-D2", "XR-D3"]) sandbox.draftPickSet(id, false);
};

// --- число выбранных стоит в подписи кнопки ---
{
  clearPicks();
  sandbox.draftPickSet("XR-D1", true);
  sandbox.draftPickSet("XR-D2", true);
  const bar = sandbox.draftRunBar("demo", []);
  const btn = deepBtn(bar, "Грумить");
  if (!btn) fail("кнопки разбора над списком нет вовсе: " + dump(bar));
  if (!dump(btn).includes("(2)")) {
    fail("кнопка не назвала число выбранных записей: " + JSON.stringify(dump(btn)));
  }
}

// --- нажатие поднимает сразу, подтверждения нет ---
{
  clearPicks();
  posted.length = 0;
  flashClear();
  sandbox.draftPickSet("XR-D1", true);
  const bar = sandbox.draftRunBar("demo", []);
  const btn = deepBtn(bar, "Грумить");
  btn.handlers.click({ stopPropagation: () => {} });
  // Гашение ставится тем же ходом, что и нажатие: второе нажатие подняло бы
  // пачку дважды.
  if (!btn.disabled) fail("кнопка осталась доступной на время подъёма");
  await settle();
  const said = dump(bar) + " " + flashes();
  for (const word of ["Поднимется", "Поднять 1", "Отмена", "Понятно"]) {
    if (said.includes(word)) fail("подтверждение вернулось на экран: " + word);
  }
  const groom = posted.filter((p) => p.path.includes("/groom"));
  if (groom.length !== 1) fail("разбор поднялся не разом с нажатия: " + JSON.stringify(posted));
  if (!flashes().includes("поднято 1")) {
    fail("итог подъёма не сказан строкой: " + flashes());
  }
  // Выбор снят: пачка сделана, отметки до следующего захода не висят. Видно
  // это по пересобранной полосе, она при пустом выборе зовёт отметить записи.
  const after = dump(sandbox.draftRunBar("demo", []));
  if (!after.includes("Отметьте записи")) {
    fail("после подъёма отметки выбора остались: " + after);
  }
}

// --- запись с идущим разбором пропускается строкой итога, а не карточкой ---
{
  clearPicks();
  posted.length = 0;
  flashClear();
  sandbox.draftPickSet("XR-D1", true);
  sandbox.draftPickSet("XR-D2", true);
  // Разбор XR-D2 уже идёт: работа задачи живая.
  const works = [{ id: "XR-D2", kind: "task", live: "busy" }];
  const bar = sandbox.draftRunBar("demo", works);
  deepBtn(bar, "Грумить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const groom = posted.filter((p) => p.path.includes("/groom"));
  if (groom.length !== 1 || !groom[0].path.includes("XR-D1")) {
    fail("поднялся не только непочатый разбор: " + JSON.stringify(posted));
  }
  const said = flashes();
  if (!said.includes("пропущено") || !said.includes("XR-D2")) {
    fail("про пропущенную запись не сказано строкой итога: " + said);
  }
}

console.log("poc_groomnow: подтверждения нет, кнопка называет число, на время подъёма гаснет, " +
  "пропущенное сказано строкой итога");

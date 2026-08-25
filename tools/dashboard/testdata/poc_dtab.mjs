// Стенд таба черновиков без своей кнопки заведения (ветка poc-chat).
//
// Заведение живёт меню кнопки «плюс» рядом с полем поиска: нажатие спрашивает,
// черновик заводят или задачу. Своя кнопка «Новая задача» внутри таба
// черновиков после этого лишняя, а поясняющий абзац под ней рассказывал про
// метаданные там, где человек пришёл разбирать накопитель (решение
// пользователя). Предмет стенда это сам экран накопителя: кнопки и абзаца на
// нём нет, а список записей с полосой запуска на месте.
//
// Зовётся: node testdata/poc_dtab.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const drafts = [
  { id: "XR-D1", title: "мысль с телефона", age_words: "вчера", moved: "2026-08-20",
    prio: "mid", order: "Проведи груминг XR-D1" },
  { id: "XR-D2", title: "вторая мысль", age_words: "неделю назад", moved: "2026-08-11" },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") return { message: "груминг поднят" };
  if (path === "/api/harnesses") {
    return { harnesses: [{ name: "claude-code", bin: "claude", default: true,
      models: [{ tier: "pro", model: "opus" }] }] };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.endsWith("/drafts")) return { drafts };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

const groups = byId.get("groups");
await sandbox.renderDrafts("demo", []);
await settle();
const said = dump(groups).replace(/\s+/g, " ");

// --- своей кнопки заведения на накопителе нет ---
if (deepBtn(groups, "Новая задача")) {
  fail("кнопка заведения осталась в табе черновиков: " + said.slice(0, 400));
}

// --- поясняющего абзаца нет тоже ---
if (said.includes("Записанные мимо доски идеи")) {
  fail("поясняющий абзац остался над списком: " + said.slice(0, 400));
}
if (said.includes("ранг и тип выдаст груминг")) {
  fail("остаток поясняющего абзаца висит над списком: " + said.slice(0, 400));
}

// --- сам накопитель на месте: записи и полоса запуска ---
{
  if (!said.includes("XR-D1") || !said.includes("мысль с телефона")) {
    fail("записи накопителя пропали вместе с кнопкой: " + said.slice(0, 400));
  }
  if (!said.includes("Отметьте записи")) {
    fail("полоса запуска разбора пропала вместе с кнопкой: " + said.slice(0, 400));
  }
  if (!byClass(groups, "dpick")) {
    fail("отметок выбора на накопителе нет: " + said.slice(0, 400));
  }
}

console.log("poc_dtab: ok");

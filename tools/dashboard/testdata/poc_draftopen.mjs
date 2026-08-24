// Стенд открытия записи накопителя нажатием на строку (ветка poc-chat).
//
// Строка черновика это дорога внутрь записи, и других входов у неё нет: с
// доски и с телефонного таба «Черновики» жмут ровно её. Дорога встала, когда
// рядом в строке появились кнопка чата и составная кнопка выбора подписки:
// обработчик спрашивал children.includes, а children в браузере это
// HTMLCollection без методов массива, и нажатие падало с TypeError, не доходя
// до перехода. Стенд судит по браузерным правилам (browserKids), иначе мок с
// массивом детей эту поломку прячет.
//
// Зовётся: node testdata/poc_draftopen.mjs static/app.js

import { makeSandbox, browserKids, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const drafts = [
  { id: "XR-D1", title: "мысль с телефона", age_words: "вчера", prio: "mid",
    order: "Проведи груминг XR-D1" },
  { id: "XR-D2", title: "вторая мысль", age_words: "неделю назад" },
];

const harnesses = [
  { name: "claude-code", bin: "claude", default: true },
  { name: "glm-code", bin: "glm" },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") return { message: "груминг поднят" };
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.endsWith("/drafts")) return { drafts };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

// Нажатие по узлу строки: обработчик зовётся так же, как его зовёт всплытие
// события от нажатого узла, и падение обработчика тут находка, а не помеха
// прогону.
function press(row, target, why) {
  try {
    row.handlers.click({ target, stopPropagation: () => {} });
  } catch (err) {
    fail("нажатие " + why + " уронило обработчик строки: " + err.message);
  }
}

// --- строка накопителя ведёт внутрь записи ---
{
  const row = browserKids(sandbox.draftRow("demo", drafts[0]));
  const title = allByClass(row, "st")[0];
  if (!title) fail("в строке накопителя нет заголовка записи: " + dump(row));
  sandbox.location.hash = "#demo/drafts";
  press(row, title, "на заголовок записи");
  await settle();
  if (!String(sandbox.location.hash).includes("draft/XR-D1")) {
    fail("нажатие на строку накопителя не открыло запись: " + sandbox.location.hash);
  }
}

// --- ID записи это та же дорога ---
{
  const row = browserKids(sandbox.draftRow("demo", drafts[0]));
  const id = byClass(row, "id");
  sandbox.location.hash = "#demo/drafts";
  press(row, id, "на ID записи");
  await settle();
  if (!String(sandbox.location.hash).includes("draft/XR-D1")) {
    fail("нажатие на ID записи не открыло её: " + sandbox.location.hash);
  }
}

// --- кнопки строки внутрь не уводят ---
{
  const row = browserKids(sandbox.draftRow("demo", drafts[0]));
  const groomBox = byClass(row, "split");
  if (!groomBox) fail("у разбора нет составной кнопки с выбором подписки: " + dump(row));
  const wide = deepBtn(row, "Провести груминг");
  const talk = deepBtn(row, "btn-ico");
  if (!talk) fail("кнопки чата в строке накопителя нет: " + dump(row));
  const icon = tag(talk, "SVG") || Array.from(talk.children)[0];
  for (const [node, why] of [[wide, "на широкую половину разбора"],
    [groomBox, "на коробку составной кнопки"],
    [talk, "на кнопку чата"],
    [icon, "на значок внутри кнопки чата"]]) {
    sandbox.location.hash = "#demo/drafts";
    press(row, node, why);
    await settle();
    if (String(sandbox.location.hash).includes("draft/XR-D1")) {
      fail("нажатие " + why + " увело внутрь записи: " + sandbox.location.hash);
    }
  }
}

// --- то же на телефоне: таб «Черновики» рисует те же строки ---
{
  sandbox.window.matchMedia = () => ({ matches: true, addEventListener: () => {},
    removeEventListener: () => {} });
  sandbox.location.hash = "#demo/drafts";
  await sandbox.renderDrafts("demo");
  await settle();
  const groups = byId.get("groups");
  const rows = allByClass(groups, "srow");
  if (rows.length !== drafts.length) {
    fail("на телефонном табе «Черновики» строк " + rows.length + ", ждал " + drafts.length);
  }
  const row = browserKids(rows[1]);
  const title = allByClass(row, "st")[0];
  press(row, title, "на строку телефонного таба");
  await settle();
  if (!String(sandbox.location.hash).includes("draft/XR-D2")) {
    fail("на телефоне строка накопителя не открыла запись: " + sandbox.location.hash);
  }
}

console.log("poc_draftopen: ok");

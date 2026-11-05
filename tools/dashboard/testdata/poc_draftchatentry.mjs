import { makeSandbox, settle, allByClass, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

// Вход в чат разбора без запуска новой ходки (DK-1564, утверждённая поправка
// пользователя 2026-11-05). Разбор записи уже шёл, и повторный груминг имеет
// смысл только при неразобранных мыслях: кнопка на форме при любом их числе
// открывает чат этой записи, само открытие разговор не поднимает. Разбор
// неразобранных мыслей стоит явным действием в самом чате и доступен только
// при них. Первый разбор черновика без прошедшего разбора работает как прежде.
//
// Записей несколько, потому что панель разговора переживает закрытие пулом
// слотов и повторный вход показывает собранное без пересборки: каждое состояние
// чата проверяется на своей записи с первым входом.
//
// Зовётся: node testdata/poc_draftchatentry.mjs static/app.js

const app = appPathArg();

const posts = [];
const works = [];
const drafts = {
  // XR-12 ведёт форму: сначала без разбора, потом после него с любым числом мыслей.
  "XR-12": { id: "XR-12", groomed: true, ideas_total: 2, ideas_undecided: 0 },
  // XR-20 после разбора с неразобранными мыслями, XR-21 без них, XR-22 под идущим разбором.
  "XR-20": { id: "XR-20", groomed: true, ideas_total: 3, ideas_undecided: 2 },
  "XR-23": { id: "XR-23", groomed: true, ideas_total: 0, ideas_undecided: 0, groom_error: true },
  "XR-21": { id: "XR-21", groomed: true, ideas_total: 2, ideas_undecided: 0 },
  "XR-22": { id: "XR-22", groomed: true, ideas_total: 3, ideas_undecided: 2 },
};
const form = { title: "запись накопителя", text: "текст записи" };

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posts.push({ path, body: init.body ? JSON.parse(init.body) : null });
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path.includes("/groom")) return { message: "разбор поднят" };
  if (path.includes("/prepared")) {
    return { id: path.split("/")[5], snapshot: "s1", state: { version: 1, state: "active", proposals: [] } };
  }
  // Журнал чата проверяется раньше поиска записи: адрес чата содержит номер
  // той же записи, и поиск съедал бы 404 журнала ответом тела записи.
  if (path.includes("/tasks/") && path.endsWith("/chat")) {
    const id = ["XR-20", "XR-21", "XR-22", "XR-23"].find((id) => path.includes("/" + id + "/"));
    if (id) return {id, chat:"carrier-" + id};
    return { raw: { status: 404, statusText: "Not Found", text: '{"error":"журнала нет"}' } };
  }
  const hit = Object.keys(drafts).find((id) => path.includes("/" + id));
  if (hit) {
    return Object.assign({ file: "docs/tasks/drafts/" + hit + ".md", order: "Проведи груминг " + hit },
      form, drafts[hit]);
  }
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works };
  if (path.includes("/chats")) return { chats: ["XR-20","XR-21","XR-22","XR-23"].map((id) => ({id:"carrier-" + id,tasks:[id],title:"Разбор записи"})), models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const groomPosts = () => posts.filter((p) => p.path.includes("/groom"));
const visibleSlot = () => allByClass(sandbox.document.getElementById("cpin"), "cslot")
  .find((s) => !s.classList.contains("off")) || null;

// --- первый разбор: черновик без прошедшего разбора держит рабочий путь ---
{
  drafts["XR-12"].groomed = false;
  await go("#demo/draft/XR-12");
  const groups = byId.get("groups");
  if (!deepBtn(groups, "Разобрать")) fail("у записи без разбора нет кнопки первого разбора");
  if (deepBtn(groups, "Чат разбора")) fail("до первого разбора на форме стоит чат разбора");
}

// --- разбор шёл, неразобранных мыслей нет: кнопка открывает чат, не ходку ---
{
  drafts["XR-12"].groomed = true;
  posts.length = 0;
  await go("#demo");
  await go("#demo/draft/XR-12");
  const groups = byId.get("groups");
  const talk = deepBtn(groups, "Чат разбора");
  if (!talk) fail("после разбора на форме нет входа в чат записи");
  if (!String(talk.title || "").includes("неразобранных мыслей нет")) {
    fail("подсказка входа молчит об отсутствии мыслей: " + talk.title);
  }
  if (deepBtn(groups, "Разобрать")) fail("после разбора на форме остался запуск новой ходки");
  talk.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).includes("/chat/XR-12")) {
    fail("кнопка не открыла чат записи: " + sandbox.location.hash);
  }
  if (groomPosts().length) fail("открытие чата без мыслей подняло груминг");
}

// --- разбор шёл, мысли неразобраны: тот же вход в чат, ходки с формы нет ---
{
  drafts["XR-12"].ideas_undecided = 2;
  posts.length = 0;
  await go("#demo");
  await go("#demo/draft/XR-12");
  const groups = byId.get("groups");
  const talk = deepBtn(groups, "Чат разбора");
  if (!talk) fail("с неразобранными мыслями вход в чат пропал с формы");
  if (!String(talk.title || "").includes("неразобранные мысли разбираются разговором")) {
    fail("подсказка входа не зовёт разбирать мысли в чате: " + talk.title);
  }
  if (deepBtn(groups, "Разобрать")) fail("с неразобранными мыслями форма сама подняла ходку");
  talk.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).includes("/chat/XR-12")) {
    fail("кнопка с мыслями не открыла чат записи: " + sandbox.location.hash);
  }
  if (groomPosts().length) fail("открытие чата с неразобранными мыслями подняло груминг");
}

// --- явное действие разбора стоит в самом чате и только при мыслях ---
{
  await go("#demo");
  await go("#demo/chat/XR-20");
  const slot = visibleSlot();
  if (!slot) fail("панель чата записи не собралась: " + dump(sandbox.document.getElementById("cpin")).slice(0, 300));
  const more = deepBtn(slot, "Разобрать мысли");
  if (!more) fail("в чате записи нет явного действия разбора мыслей");
  more.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!groomPosts().length) fail("действие разбора мыслей не подняло груминг");
  if (!groomPosts()[0].path.includes("/drafts/XR-20/groom")) {
    fail("разбор мыслей ушёл не в запись: " + groomPosts()[0].path);
  }
}

// --- без неразобранных мыслей действия разбора в чате нет ---
{
  await go("#demo");
  await go("#demo/chat/XR-21");
  const slot = visibleSlot();
  if (!slot) fail("чат записи без мыслей не открылся");
  if (deepBtn(slot, "Разобрать мысли")) fail("без неразобранных мыслей в чате стоит разбор");
}

// --- идущий разбор забирает действие себе: второй запуск не нужен ---
{
  works.push({ id: "XR-22", kind: "task", via: "tmux", own: true, live: "busy" });
  await go("#demo");
  await go("#demo/chat/XR-22");
  const slot = visibleSlot();
  if (!slot) fail("чат записи с идущим разбором не открылся");
  if (deepBtn(slot, "Разобрать мысли")) fail("при идущем разборе чат предлагает второй запуск");
}

{
 await go("#demo");
 await go("#demo/draft/XR-23");
 posts.length = 0;
 const talk = deepBtn(byId.get("groups"), "Чат разбора");
 if (!talk) fail("после ошибки нет входа в прежний чат");
 talk.handlers.click({stopPropagation:()=>{}});
 await go("#demo/chat/XR-23");
 const actual = await sandbox.chatState("demo", "XR-23", {sections:[]}, works);
 if (actual.sid !== "carrier-XR-23" || !actual.draft || !actual.draft.groom_error) fail("повтор потерял назначенный чат или ошибку");
 if (posts.some((p) => /\/(groom|say)$/.test(p.path))) fail("вход в прежний чат после ошибки отправил запрос запуска или реплику");
 const retry = deepBtn(visibleSlot(), "Повторить разбор");
 if (!retry) fail("после ошибки нет явного повтора без новой мысли");
 retry.handlers.click({stopPropagation:()=>{}});
 await settle();
 if (groomPosts().length !== 1 || !groomPosts()[0].path.includes("/XR-23/groom")) fail("повтор после ошибки не поднялся");
}

posts.length = 0;
for (const id of ["XR-20", "XR-21", "XR-22"]) sandbox.draftPickSet(id, true);
await sandbox.draftGroomStart("demo", works, "", "", "");
await settle();
if (groomPosts().length !== 1 || !groomPosts()[0].path.includes("/XR-20/groom")) {
  fail("пакетный запуск повторил разобранную запись или живой разбор: " + JSON.stringify(groomPosts()));
}

console.log("вход в чат разбора не поднимает ходку, мысли разбираются действием в чате");

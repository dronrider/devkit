// Стенд колонок раздела сессий (POC DK-397, ветка poc-chat).
//
// Две находки пользователя об одном списке. Первая: под колонку хода занято
// слишком много места, восемьдесят точек под кружок в девять, и держала их
// собственная подпись со значком порядка. Вторая: колонки «Идёт» и
// «Активность» показывают похоже одно и то же, а полезна из них вторая, потому
// что сессия висит третьи сутки и замолчала час назад.
//
// Отсюда предмет стенда: колонка хода не подписана словом и ширина её меньше
// порога, но сортировка по ней жива; колонки возраста нет вовсе, а сам возраст
// не потерян, он стоит в подсказке даты активности рядом с точным временем
// реплики. Заголовок у колонки при этом есть, он рисованный: снятый заголовок
// человек забраковал отдельно, и сторожит его стенд poc_headico.mjs.
//
// Зовётся: node testdata/poc_sesscol.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Порог ширины колонки состояния: она несёт кружок в девять точек и боковые
// поля ячейки, и восемь десятков под это не нужны никому.
const LIVE_MAX = 48;

const works = [
  { kind: "session", via: "session", session: "s-busy", own: true, live: "busy",
    title: "буквой раньше", started: 3000, moved: 9000 },
  { kind: "session", via: "session", session: "s-idle", own: true, live: "idle",
    title: "буквой позже", started: 1000, moved: 5000 },
];

const { sandbox, byId } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path_ === "/api/harnesses") return { harnesses: [] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [] }, works };
  }
  if (path_.endsWith("/works")) return { works };
  if (path_.endsWith("/drafts")) return { drafts: [] };
  if (path_.includes("/chats")) return { chats: [], models: [] };
  if (path_ === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const dump = (node) => String((node && node.textContent) || "");
const head = () => allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-sess")) || null;
const cells = () => [...((head() || {}).children || [])];
const colCell = (key) => cells().find((c) => (c.attrs || {})["data-col"] === key) || null;
const titles = () => allByClass(groups, "arow")
  .map((row) => dump(byClass(byClass(row, "ab"), "tt")));
const click = async (btn) => {
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
};

await go("#demo/sess");
if (!head()) fail("шапки колонок у сессий нет вовсе");

// --- колонки возраста сессии нет ни в шапке, ни в строке ---
{
  const labels = cells().map((c) => dump(c).trim());
  if (labels.some((said) => said.includes("Идёт"))) {
    fail("колонка «Идёт» осталась в шапке сессий: " + JSON.stringify(labels));
  }
  const row = allByClass(groups, "arow")[0];
  const kids = [...row.children].map((k) => String(k.className || "").split(" ")[0]);
  if (kids.includes("atime")) {
    fail("ячейка возраста осталась в строке сессии: " + JSON.stringify(kids));
  }
  if (!kids.includes("amoved")) {
    fail("колонка активности пропала вместе с возрастом: " + JSON.stringify(kids));
  }
}

// --- колонка хода не подписана словом, и ширина её меньше порога ---
{
  const cell = colCell("live");
  if (!cell) fail("колонки состояния в шапке сессий нет: " + dump(head()));
  if (dump(cell).trim()) {
    fail("колонка хода снова подписана словом «" + dump(cell).trim() +
      "»: место под слово её и раздувало");
  }
  const said = sandbox.document.documentElement.style.props["--tc-sess-live"] || "";
  const wide = Number(String(said).replace("px", ""));
  if (!(wide > 0)) fail("ширина колонки состояния не встала переменной: " + said);
  if (wide > LIVE_MAX) {
    fail("колонка состояния шире порога: " + wide + " точек при пороге " + LIVE_MAX +
      ", а несёт она кружок в девять точек");
  }
  // Кружок при этом на месте: колонка ужата, а состояние видно.
  const row = allByClass(groups, "arow")[0];
  if (!byClass(byClass(row, "live"), "dot")) {
    fail("кружок хода пропал вместе с шириной колонки: " + String(dump(row)).slice(0, 120));
  }
}

// --- сортировка по состоянию жива ---
{
  const btn = byClass(colCell("live"), "tblb");
  if (!btn) fail("шапка колонки состояния перестала быть кнопкой порядка");
  const say = String((btn.attrs || {})["aria-label"] || "");
  if (!say.includes("ходу работы")) {
    fail("подсказка колонки состояния не называет, по чему она сортирует: " + say);
  }
  const was = titles();
  if (JSON.stringify(was) !== JSON.stringify(["буквой раньше", "буквой позже"])) {
    fail("сессии открылись не по состоянию: " + JSON.stringify(was));
  }
  await click(btn);
  if (JSON.stringify(titles()) !== JSON.stringify(["буквой позже", "буквой раньше"])) {
    fail("нажатие на шапку состояния не развернуло порядок: " + JSON.stringify(titles()));
  }
  if (sandbox.localStorage.getItem("devkit.dash.sess.sort") !== "live:desc") {
    fail("порядок по состоянию не записался: " +
      sandbox.localStorage.getItem("devkit.dash.sess.sort"));
  }
  await click(btn);
}

// --- возраст сессии уехал в подсказку активности ---
{
  const row = allByClass(groups, "arow")[0];
  const said = byClass(byClass(row, "amoved"), "stale");
  if (!said) fail("даты активности в строке сессии нет");
  const tip = String((said.attrs || {}).title || said.title || "");
  if (!tip) fail("у даты активности нет подсказки вовсе");
  if (!/\d{1,2}:\d{2}/.test(tip)) {
    fail("подсказка активности не несёт времени последней реплики: " + tip);
  }
  if (!tip.includes("сессия идёт")) {
    fail("возраст сессии потерялся вместе с колонкой «Идёт»: " + tip);
  }
}

// --- сортировка по активности жива ---
{
  const btn = byClass(colCell("moved"), "tblb");
  if (!btn) fail("шапка колонки активности перестала быть кнопкой порядка");
  await click(btn);
  if (JSON.stringify(titles()) !== JSON.stringify(["буквой раньше", "буквой позже"])) {
    fail("сессии не встали по последней активности: " + JSON.stringify(titles()));
  }
  await click(btn);
  if (JSON.stringify(titles()) !== JSON.stringify(["буквой позже", "буквой раньше"])) {
    fail("порядок по активности не развернулся: " + JSON.stringify(titles()));
  }
}

console.log("poc_sesscol: ok");

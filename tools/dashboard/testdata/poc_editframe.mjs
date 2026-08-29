// Стенд рамки поля правки (ветка poc-chat, DK-577).
//
// Живой случай: пользователь выделил на экране задачи карточку панели и поле
// внутри неё. «Нужно убрать вот эту двойную рамку при включении
// редактирования». Рамку рисовала карточка панели и поле ввода внутри неё, и в
// режиме правки они складывались в две рамки одна в другой.
//
// Предмет стенда: у поля правки внутри карточки рамка одна, и это рамка
// карточки. Поле остаётся отличимым от покоя: своя земля и признак фокуса.
// Меряется числом с настоящего style.css на узком экране и на широком, и не по
// одному месту: постановка задачи, форма черновика и поле реплики правятся
// одинаково.
//
// Зовётся: node testdata/poc_editframe.mjs static/app.js [--show]

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf, deepFind } from "./poc_css.mjs";

const app = appPathArg();
const show = process.argv.includes("--show");
const rules = cssRules(app);
const WANT = ["border", "border-color", "border-radius", "padding", "background",
  "outline", "box-shadow"];
const fit = (node, width, state) => layoutOf(node, { rules, width, want: WANT, state });

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/sessions/")) return { session: "s", head: { id: "s" }, items: [], total: 0 };
  if (path.endsWith("/drafts")) return { drafts: [], works: [] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const tagIs = (name) => (n) => String(n.tagName || "").toLowerCase() === name;
const field = (node) => deepFind(node,
  (n) => tagIs("textarea")(n) || tagIs("input")(n)).find((n) => !n.hidden);

// Панель постановки в режиме правки: та самая, что выделил пользователь.
const paper = sandbox.filePanel("demo", "XR-226",
  { file: "docs/tasks/XR-226.md" }, { text: "постановка задачи" }, () => {}, true, false);
// Поле реплики разговора: правится там же, тем же способом.
const chat = sandbox.chatPanel("demo",
  { addr: "s", sid: "aaaa5779-2222", chats: [], models: [], project: "demo",
    entry: { tmux: "chat-9", state: "live" } });
await settle();

// Рамкой считается непрозрачная граница: «none» и отсутствие правила это не
// рамка, а её отсутствие.
function framed(got) {
  const b = String(got.border || "");
  return b !== "" && b !== "none" && !b.startsWith("0");
}

// Проверка идёт по месту сразу после сборки: форма рисует в общий узел экрана,
// и снятая с него разметка теряет предков, а с ними и раскладку.
function check(name, body) {
  for (const width of [390, 1440]) {
    const area = field(body);
    if (!area) fail("у места «" + name + "» нет поля правки: " + dump(body).slice(0, 200));
    // Рамку держит ближайший предок поля, который её рисует: карточка панели
    // или коробка самого поля.
    let card = null;
    for (let n = area.parentNode; n; n = n.parentNode) {
      if (framed(fit(n, width))) { card = n; break; }
    }
    if (!card) card = body;
    const outer = fit(card, width);
    const inner = fit(area, width);
    const live = fit(area, width, ":focus");
    if (show) {
      console.log("[" + width + "] " + name);
      for (const k of WANT) {
        if (outer[k] === undefined && inner[k] === undefined) continue;
        console.log("   " + k + ": рамка держит " + (outer[k] ?? "-") +
          " | поле " + (inner[k] ?? "-"));
      }
      console.log("   фокус: " + JSON.stringify(fit(card, width, ":focus-within")));
    }
    if (framed(outer) && framed(inner) && card !== area) {
      fail("на " + width + " точках у «" + name + "» рамка двойная: снаружи «" +
        outer.border + "», у поля «" + inner.border + "»");
    }
    if (!framed(outer) && !framed(inner)) {
      fail("на " + width + " точках у «" + name + "» рамки нет вовсе: поле правки " +
        "потеряло границу");
    }
    // Поле правки остаётся отличимым от покоя: либо рамка вокруг него самого,
    // либо своя земля под текстом.
    const ownFrame = framed(inner) || card === area.parentNode;
    if (!ownFrame && (!inner.background || inner.background === "none")) {
      fail("на " + width + " точках у «" + name + "» поле правки не отличить от покоя");
    }
    // И фокус назван: снятая рамка не должна забрать с собой понимание, что
    // здесь сейчас пишут.
    const marks = ["outline", "border-color", "box-shadow", "background"];
    const ownLive = fit(card, width, ":focus-within");
    const named = marks.some((k) => live[k] !== undefined && live[k] !== inner[k]) ||
      marks.some((k) => ownLive[k] !== undefined && ownLive[k] !== outer[k]);
    if (!named) {
      fail("на " + width + " точках у «" + name + "» фокус поля ничем не назван: " +
        JSON.stringify(live));
    }
  }
}

check("постановка задачи", byClass(paper, "fbody"));
check("поле реплики", byClass(chat, "cbox"));

// Форма заведения строит поля в теле карточки, и правила ей писаны на семью
// («.nfbody textarea», «.nfbody>input»), а не на узел. Экраны стенда до всех
// её полей не доводят, поэтому семья проверяется собранной цепочкой узлов: это
// те же классы и тот же разбор style.css, только предки поставлены руками.
const madeChain = (steps) => {
  let up = null;
  for (const step of steps) {
    const [tag, cls] = step.split("|");
    const node = { tagName: tag.toUpperCase(), className: cls || "", children: [], parentNode: up };
    if (up) up.children.push(node);
    up = node;
  }
  return up;
};
check("форма заведения, поле текста",
  madeChain(["div|card", "div|nfbody", "textarea|"]).parentNode);
check("форма заведения, поле строки",
  madeChain(["div|card", "div|nfbody", "input|"]).parentNode);

sandbox.location.hash = "#demo/new/draft";
await sandbox.refresh();
await settle();
const draft = byClass(byId.get("groups"), "tpage");
if (!draft) fail("форма черновика не собралась");
check("форма черновика", draft);

console.log("ок: у поля правки рамка одна, поле отличимо от покоя и названо в " +
  "фокусе, и это снято числом с постановки, поля реплики, формы заведения и " +
  "формы черновика на 390 и 1440 точках");

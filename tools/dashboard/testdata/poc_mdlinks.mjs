// Стенд автоссылок в репликах ленты (ветка poc-chat).
//
// Из разговора попадают туда, о чём в нём говорят: ID задачи и путь документа
// стояли в ленте обычным текстом, и дорога от слов до экрана шла через поиск
// руками (замечание пользователя, как в vscode). Предмет стенда это разметка
// пузыря: во что превращается упоминание, куда ведёт адрес и чего разбор не
// трогает. Не трогает он много чего: готовую ссылку markdown, код в обратных
// кавычках, блок кода целиком и незнакомый префикс вроде «UTF-8».
//
// Зовётся: node testdata/poc_mdlinks.mjs static/app.js

import { makeSandbox, settle, dump, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const projects = [
  { name: "devkit", prefix: "DK", works: [] },
  { name: "other", prefix: "XR", works: [] },
];

const { sandbox } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

if (typeof sandbox.rememberPrefixes !== "function" || typeof sandbox.chatFeedIn !== "function") {
  fail("в статике нет ни памяти префиксов досок, ни двери проекта ленты: " +
    "автоссылке в реплике не по чему узнать ID и некуда вести");
}
sandbox.rememberPrefixes(projects);

// Ссылки собранного пузыря: подпись и адрес, куда она ведёт.
const links = (node) => allByClass(node, "mdgo").map((a) => [dump(a).trim(), a.href]);

const bubble = (project, text) => {
  sandbox.chatFeedIn(project);
  return sandbox.chatBubble("агент", text, "");
};

// --- ID задачи в тексте становится ссылкой на форму своего проекта ---
{
  const got = links(bubble("devkit", "Правку везёт DK-397, она рядом с DK-430."));
  if (JSON.stringify(got) !== JSON.stringify([["DK-397", "#devkit/DK-397"], ["DK-430", "#devkit/DK-430"]])) {
    fail("ID задач не стали ссылками своего проекта: " + JSON.stringify(got));
  }
}

// --- в чате чужого проекта ссылка ведёт в его проект ---
{
  const got = links(bubble("other", "Сначала XR-1, потом посмотрим."));
  if (JSON.stringify(got) !== JSON.stringify([["XR-1", "#other/XR-1"]])) {
    fail("в чате чужого проекта ссылка ушла не в его проект: " + JSON.stringify(got));
  }
  // Чужой префикс ведёт в проект своей доски, а не в открытый разговором.
  const alien = links(bubble("other", "Это чинили в DK-397."));
  if (JSON.stringify(alien) !== JSON.stringify([["DK-397", "#devkit/DK-397"]])) {
    fail("ID чужой доски увёл не в её проект: " + JSON.stringify(alien));
  }
}

// --- незнакомый префикс остаётся текстом ---
{
  const box = bubble("devkit", "Файл в UTF-8, а сборка ISO-8601 и COVID-19 тут ни при чём.");
  if (links(box).length) fail("слова с дефисом и числом стали ссылками: " + JSON.stringify(links(box)));
  if (!dump(box).includes("UTF-8")) fail("текст с незнакомым префиксом потерялся: " + dump(box));
}

// --- ID внутри блока кода и обратных кавычек не трогается ---
{
  const box = bubble("devkit", "Смотри тут:\n\n```\ntaskctl show DK-397\n```\n\nи `DK-430` в строке.");
  if (links(box).length) {
    fail("ID из блока кода или обратных кавычек стал ссылкой: " + JSON.stringify(links(box)));
  }
  if (!dump(box).includes("taskctl show DK-397")) fail("блок кода потерялся: " + dump(box));
}

// --- документы: LLD, файл задачи, черновик ---
{
  const got = links(bubble("devkit",
    "Разбор в docs/lld/DK-397.md, постановка в docs/tasks/DK-397.md, мысль в docs/tasks/drafts/DK-D1.md."));
  const want = [["docs/lld/DK-397.md", "#devkit/doc/lld/DK-397.md"],
    ["docs/tasks/DK-397.md", "#devkit/DK-397"],
    ["docs/tasks/drafts/DK-D1.md", "#devkit/draft/DK-D1"]];
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("пути документов повели не туда: " + JSON.stringify(got));
  }
}

// --- ссылка markdown на файл репозитория ведёт на экран, а не в пустоту ---
{
  const got = links(bubble("devkit", "Постановка: [tasks/DK-397.md](../tasks/DK-397.md)."));
  if (JSON.stringify(got) !== JSON.stringify([["tasks/DK-397.md", "#devkit/DK-397"]])) {
    fail("ссылка markdown на файл задачи повела не туда: " + JSON.stringify(got));
  }
}

// --- готовая внешняя ссылка не переписывается ---
{
  const box = bubble("devkit", "Тут [про DK-397](https://example.org/DK-397) и голый https://example.org/x");
  if (links(box).length) fail("внешняя ссылка переписана автоссылкой: " + JSON.stringify(links(box)));
  const hrefs = [];
  const walk = (n) => {
    if (n.tagName === "A") hrefs.push(n.href);
    for (const kid of n.children || []) walk(kid);
  };
  walk(box);
  if (JSON.stringify(hrefs) !== JSON.stringify(["https://example.org/DK-397", "https://example.org/x"])) {
    fail("внешние ссылки собрались не теми адресами: " + JSON.stringify(hrefs));
  }
}

// --- нажатие ведёт на экран, не закрывая разговор ---
{
  const box = bubble("devkit", "Правку везёт DK-397.");
  const a = allByClass(box, "mdgo")[0];
  sandbox.location.hash = "#devkit/chat/aaaa1111-1111";
  a.handlers.click({ preventDefault: () => {}, stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash !== "devkit/DK-397/chat/aaaa1111-1111") {
    fail("нажатие на автоссылку закрыло разговор или ушло не туда: " + sandbox.location.hash);
  }
}

// --- разметка соседних экранов остаётся без ссылок ---
// mdRender зовут и постановка задачи, и документ LLD: там ID стоит в своём
// тексте, и проект ленты к нему отношения не имеет.
{
  const plain = sandbox.mdRender("Правку везёт DK-397.");
  if (allByClass(plain, "mdgo").length) {
    fail("автоссылка вылезла за пределы ленты разговора: " + dump(plain));
  }
}

console.log("poc_mdlinks: ok");

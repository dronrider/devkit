// Страница ленты для снимка (ветка poc-chat).
//
// Вёрстку ленты моком не проверить: связка линией, кружки исхода и попадание
// кружка в первую строку записи это геометрия, и видно её только в браузере.
// Живую страницу дашборда headless-chrome не отпускает (вечные опросы держат
// виртуальное время), поэтому лента собирается тем же кодом статики в
// игрушечном дереве, а наружу отдаётся простой страницей с настоящими стилями.
// Снимок с неё делает testdata/poc_shot_page.py.
//
// Зовётся: node testdata/poc_shot_feed.mjs static/app.js > /tmp/feed.html

import { makeSandbox, makeNode, settle, appPathArg } from "./poc_dom.mjs";

const items0 = [];
const { sandbox } = makeSandbox(appPathArg(), (path) => {
  if (path.includes("/sessions/") && !path.includes("stream=1")) {
    return { session: "shot", head: { id: "shot" }, items: items0, total: items0.length, start: true };
  }
  return {};
});

const at = (s) => "2026-08-20T01:" + String(s).padStart(2, "0") + ":00+03:00";
const items = [
  { seq: 0, key: "m:0", role: "user", text: "проверь сборку и покажи дифф правки", time: at(1) },
  { seq: 1, key: "m:1", role: "thinking", text: "смотрю, что поменялось", spent: 4200, time: at(2) },
  { seq: 2, key: "m:2", role: "assistant", text: "Собираю и смотрю дифф.", time: at(3) },
  { seq: 3, key: "m:3", role: "tool", tool: "Bash", note: "GOWORK=off go build ./...",
    about: "сборка пакета", text: "command: GOWORK=off go build ./...", time: at(4) },
  { seq: 4, key: "m:4", role: "toolout", text: "готово, ошибок нет", time: at(4) },
  { seq: 5, key: "m:5", role: "tool", tool: "Bash", note: "go test ./...", about: "прогон тестов",
    text: "command: go test ./...", time: at(5) },
  { seq: 6, key: "m:6", role: "toolout", text: "FAIL github.com/dronrider/devkit/tools/dashboard", fail: true, time: at(5) },
  { seq: 7, key: "m:7", role: "tool", tool: "Read", note: "/Users/rider/projects/devkit/tools/dashboard/static/app.js",
    args: { file_path: "/Users/rider/projects/devkit/tools/dashboard/static/app.js", offset: "120", limit: "40" },
    time: at(6) },
  { seq: 8, key: "m:8", role: "tool", tool: "Edit", note: "static/style.css",
    args: { file_path: "/Users/rider/projects/devkit/tools/dashboard/static/style.css",
      old_string: ".frow{position:relative}\n.fdot{opacity:.55}\n.fbody{flex:1}",
      new_string: ".frow{position:relative}\n.fdot{z-index:1}\n.fbody{flex:1}" },
    time: at(7) },
  { seq: 9, key: "a:0", role: "assistant", text: "смотрю дерево стендов", sub: "разбор находки", time: at(8) },
  { seq: 10, key: "a:1", role: "tool", tool: "Bash", note: "ls testdata", about: "стенды на месте",
    text: "command: ls testdata", sub: "разбор находки", time: at(8) },
  { seq: 11, key: "a:2", role: "toolout", text: "poc_panel.mjs poc_wake.mjs", sub: "разбор находки", time: at(9) },
  { seq: 12, key: "m:9", role: "assistant", text: "Правка на месте, тесты зелёные.", time: at(10) },
];

items0.push(...items);
const box = makeNode("div");
sandbox.wireChatFeed("demo", box, "shot");
await settle();

// Разметка игрушечного дерева в html: своего сериализатора у мока нет, а для
// снимка нужен именно он.
const ESC = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" };
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ESC[c]);
function html(node) {
  if (!node) return "";
  const tag = String(node.tagName || "div").toLowerCase();
  if (tag === "#text") return esc(node.textContent || "");
  const bits = [];
  if (node.className) bits.push(' class="' + esc(node.className) + '"');
  if (node.title) bits.push(' title="' + esc(node.title) + '"');
  if (node.hidden) bits.push(" hidden");
  for (const [k, v] of Object.entries(node.attrs || {})) {
    if (k !== "class") bits.push(" " + k + '="' + esc(v) + '"');
  }
  const kids = (node.children || []).map(html).join("");
  const own = node.children && node.children.length ? "" : esc(node.textContent || "");
  return "<" + tag + bits.join("") + ">" + own + kids + "</" + tag + ">";
}

// Страница повторяет настоящую панель разговора: та же обёртка и та же
// ширина, иначе вёрстка меряется в чужой раскладке. Ширина приходит вторым
// доводом, замер геометрии третьим («мерка»): он печатает, где стоит кружок и
// где середина текста заголовка записи.
const width = process.argv[3] || "900";
const measure = process.argv[4] === "мерка";
const probe = `<pre id="geom"></pre><script>
function lead(row){
  const head=row.querySelector(".frowb b")||row.querySelector(".frowb");
  const walk=document.createTreeWalker(head,NodeFilter.SHOW_TEXT);
  let n;
  while((n=walk.nextNode())){
    if(!n.textContent.trim()) continue;
    const r=document.createRange(); r.selectNodeContents(n);
    const box=r.getClientRects()[0];
    if(box&&box.height) return box;
  }
  return null;
}
const out=[];
for(const row of document.querySelectorAll(".frow")){
  const dot=row.querySelector(".fdot").getBoundingClientRect();
  const box=lead(row);
  out.push(row.className+" | сдвиг "+(box?((dot.top+dot.height/2)-(box.top+box.bottom)/2).toFixed(1):"-")+
    " | зазор слева "+(row.querySelector(".frowb").getBoundingClientRect().left-
      (dot.left+dot.width/2)).toFixed(1));
}
document.getElementById("geom").textContent=out.join("\\n");
</script>`;

process.stdout.write(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>лента</title>
<link rel="stylesheet" href="style.css"></head>
<body style="margin:0"><div class="cpanel" style="--cw:${width}px"><div class="cpin"><div class="chatwrap">${html(box)}</div></div></div>${measure ? probe : ""}</body></html>
`);

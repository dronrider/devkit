// Мок сети для стендов, которые гоняют настоящий static/app.js в браузере
// (POC DK-397, ветка poc-chat).
//
// Стенды раскладки до сих пор повторяли вёрстку руками: браузеру нужен живой
// DOM, а поднимать сервер ради замера дорого. Копия вёрстки врёт молча, стоит
// экрану поехать в app.js, и замер тогда говорит о разметке, которой на экране
// нет. Тут вместо копии грузится сам app.js, а вместо сервера отвечает этот
// мок: страница стенда живёт файлом, ходить ей некуда.
//
// Скрипт встаёт до app.js: подменять fetch надо раньше первого запроса.
const STAND_HARNESSES = [{ name: "claude-code", default: true, tiers: ["base", "pro", "max"] },
  { name: "glm-code", tiers: ["pro"] }];

// Секция строки приходит параметром адреса: экран задачи держит на строке
// Backlog кнопку «Выполнить», а на строке Check «Проверить», и мерить надо обе.
const STAND_SECT = new URLSearchParams(location.search).get("bar") === "check"
  ? "check" : "backlog";

const STAND_ROW = { id: "XR-1", title: "запуск с выбором подписки", sect: STAND_SECT,
  type: "task", cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24",
  accept: "user" };

function standBody(path) {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: STAND_HARNESSES };
  if (path.includes("/drafts/")) {
    return { project: "demo", id: "d-1", file: "local-docs/drafts/d-1.md",
      said: "Меню подписки в мобильной форме\n\n### Ситуация\n\nТекст записи.\n",
      hash: "h1", running: false, locked: false, order: "" };
  }
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/tasks/")) {
    return { project: "demo", id: "XR-1", row: STAND_ROW, after: [], blocks: [],
      file: "docs/tasks/XR-1.md", text: "# XR-1\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: STAND_SECT, title: "Раздел",
      rows: [STAND_ROW] }] }, works: [] };
  }
  if (path.endsWith("/works")) return { works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
}

window.fetch = (path) => Promise.resolve({
  ok: true,
  status: 200,
  statusText: "OK",
  text: () => Promise.resolve(JSON.stringify(standBody(String(path)))),
});
// Канал событий стенду не нужен: экран собирается ответами, а живой ленты тут
// нет вовсе.
window.EventSource = function EventSourceStub() {
  this.close = () => {};
  this.addEventListener = () => {};
};

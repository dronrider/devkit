// Стенд экрана записи при идущем груминге (два замечания пользователя).
// Первое: карточка «Груминг идёт» дублировала одноимённую пометку в шапке, а
// живых данных не несла, и с экрана снята. Второе: конец груминга экран
// замечает сам: пока разбор идёт, состояние перечитывается по таймеру, и
// пометка уходит вместе с приходом карточки исхода без перезагрузки страницы.
//
// Зовётся: node testdata/poc_groomwatch.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

let grooming = true;
let outcome = { state: "open", file: "docs/tasks/drafts/XR-D2.md",
  note: "груминг записи не касался" };

const { sandbox, byId, timers } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", sections: {},
      works: grooming ? [{ id: "XR-D2", via: "tmux", title: "запись накопителя" }] : [] }] };
  }
  if (path.endsWith("/outcome")) return outcome;
  if (path.includes("/chats")) return { chats: [] };
  if (path.includes("/drafts/")) {
    return { file: "docs/tasks/drafts/XR-D2.md", text: "текст записи" };
  }
  return null;
});

sandbox.location.hash = "#demo/draft/XR-D2";
await sandbox.refresh();
await settle();

const groups = byId.get("groups");
let shown = dump(groups).replace(/\s+/g, " ");

// Пометка в шапке стоит, а карточки-дубля с тем же заголовком и подсказкой
// про будущий исход на экране нет.
if (!shown.includes("груминг идёт")) {
  fail("идущий груминг не помечен в шапке: " + shown.slice(0, 300));
}
if (shown.includes("Груминг идёт")) {
  fail("карточка «Груминг идёт» дублирует пометку шапки: " + shown.slice(0, 400));
}
if (shown.includes("Разбор кончится строкой")) {
  fail("подсказка снятой карточки хода осталась на экране: " + shown.slice(0, 400));
}

// Пока разбор идёт, заведён опрос состояния: без него конец груминга видел бы
// только тот, кто перезагрузил страницу.
const poll = timers.filter((t) => t.ms === 3000);
if (!poll.length) fail("опрос конца груминга не заведён: таймеров с его интервалом нет");

// Груминг кончился строкой: очередной тик опроса сам снимает пометку и ставит
// карточку исхода, руками экран никто не обновляет.
grooming = false;
outcome = { state: "row", note: "груминг завёл строку: XR-D2 стоит на доске demo" };
poll[poll.length - 1].fn();
await settle();
shown = dump(groups).replace(/\s+/g, " ");
if (shown.includes("груминг идёт")) {
  fail("пометка идущего груминга висит после конца разбора: " + shown.slice(0, 400));
}
if (!shown.includes("Черновик оформлен строкой")) {
  fail("исход груминга не показался сам: " + shown.slice(0, 400));
}
if (!shown.includes("груминг завёл строку")) {
  fail("слова сервера про след не показаны: " + shown.slice(0, 400));
}

// Кончившийся разбор круг опроса обрывает: нового таймера после тика нет.
const left = timers.filter((t) => t.ms === 3000);
if (left.length !== poll.length) {
  fail("опрос не остановился после конца груминга: таймеров было " +
    poll.length + ", стало " + left.length);
}

console.log("ok: карточки-дубля у идущего груминга нет, конец разбора экран " +
  "замечает опросом и сам ставит карточку исхода, опрос после конца стоит");

// Стенд экрана записи при идущем груминге (замечания пользователя).
// Карточек исхода на форме нет ни одной: разговор с агентом всегда идёт в
// чате, там же виден и его исход, а на доске он виден по факту, строкой или её
// отсутствием. Осталась пометка идущего разбора в шапке и её опрос: конец
// груминга экран замечает сам, и пометка уходит без перезагрузки страницы, а
// кнопка «Грумить» возвращается на место.
//
// Зовётся: node testdata/poc_groomwatch.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

let grooming = true;
const { sandbox, byId, timers } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", sections: {},
      works: grooming ? [{ id: "XR-D2", via: "tmux", live: "busy",
        title: "запись накопителя" }] : [] }] };
  }
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

// Груминг кончился: очередной тик опроса сам снимает пометку и возвращает
// кнопку разбора, руками экран никто не обновляет. Исхода форма не
// пересказывает: он виден в чате и на доске.
grooming = false;
poll[poll.length - 1].fn();
await settle();
shown = dump(groups).replace(/\s+/g, " ");
if (shown.includes("груминг идёт")) {
  fail("пометка идущего груминга висит после конца разбора: " + shown.slice(0, 400));
}
if (!shown.includes("Грумить")) {
  fail("после конца разбора кнопка груминга не вернулась: " + shown.slice(0, 400));
}
if (shown.includes("Черновик оформлен строкой") || shown.includes("Груминг кончился")) {
  fail("карточка исхода вернулась на форму: " + shown.slice(0, 400));
}

// Кончившийся разбор круг опроса обрывает: нового таймера после тика нет.
const left = timers.filter((t) => t.ms === 3000);
if (left.length !== poll.length) {
  fail("опрос не остановился после конца груминга: таймеров было " +
    poll.length + ", стало " + left.length);
}

console.log("ok: карточек исхода на форме записи нет, конец разбора экран замечает " +
  "опросом и сам возвращает кнопку груминга, опрос после конца стоит");

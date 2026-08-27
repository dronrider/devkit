// Стенд состояния сессий и их закрытия (ветка poc-chat), таб сессий доски.
//
// Раздел красил зелёным всякую живую сессию: на снимке пользователя три
// молчащих часами окна выглядели работающими, а всего на машине их накопилось
// три десятка, и закрыть их было нечем вовсе («я ничего не могу сделать с
// этими сессиями»). Предмет стенда две половины: строка называет состояние
// словом и красит кружок по нему, а снятие идёт той же ручкой, какой сессию
// снимает смена модели, с подтверждением у занятой.
//
// Пачки «Закрыть простаивающие» тут больше нет вовсе: она считала по
// разговорам всей машины, а таб показывает сессии проекта, и предлагала снять
// два с половиной десятка при пяти видимых, назвав их именами tmux, из которых
// не видно, с чем прощаешься (решение пользователя). Массовое снятие вернётся
// другим способом, если понадобится, а стенд держит рубеж: кнопки нет.
//
// Зовётся: node testdata/poc_agentlive.mjs static/app.js

import { readFileSync } from "node:fs";
import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

// Имя tmux-сессии (tmux) едет у каждой работы, которую поднимал дашборд: на
// нём стоит и признак own, и закрытие, и врозь эти два поля не ездят. Работа
// без имени закрывается только там, где открыта, и кнопка у неё погашена.
const works = [
  { id: "XR-1", kind: "task", via: "tmux", title: "идущая работа", own: true,
    tmux: "task-XR-1-1", live: "busy", moved: Math.floor((Date.now() - 30 * 1000) / 1000) },
  { id: "XR-2", kind: "task", via: "session", session: "aaaa1111-1111", title: "спросил и ждёт",
    own: true, tmux: "chat-XR-2-1",
    live: "waiting", moved: Math.floor((Date.now() - 5 * 60 * 1000) / 1000) },
  { kind: "session", via: "session", session: "bbbb2222-2222", note: "молчащее окно",
    own: true, tmux: "chat-bbbb-1", live: "idle", moved: Math.floor((Date.now() - 3 * hour) / 1000) },
  // Разговорная сессия конвейера: её подняла tmux дашборда, ход в ней кончился,
  // своего id сессии у неё в ответе сервера нет. Таких на живой машине
  // большинство, и кнопки снятия у них не было вовсе (замечание пользователя).
  { id: "XR-5", kind: "task", via: "tmux", title: "договорившая сессия задачи",
    own: true, tmux: "task-XR-5-1", talk: true, live: "idle",
    moved: Math.floor((Date.now() - 2 * hour) / 1000) },
  // Две молчащие сессии про уход строки с экрана: одна снимается удачно, вторая
  // отвечает «уже закрыта». Свои они у каждой проверки нарочно: снятая строка
  // держится убранной памятью экрана, и переиспользовать её нельзя.
  { kind: "session", via: "session", session: "eeee5555-5555", note: "молчащая вторая",
    own: true, tmux: "chat-eeee-1", live: "idle", moved: Math.floor((Date.now() - 4 * hour) / 1000) },
  { kind: "session", via: "session", session: "ffff6666-6666", note: "молчащая третья",
    own: true, tmux: "chat-ffff-1", live: "idle", moved: Math.floor((Date.now() - 5 * hour) / 1000) },
  // Окно человека: дашборд его не поднимал, tmux-имени не знает, снимать нечем.
  { kind: "session", via: "session", session: "cccc3333-3333", note: "окно vscode",
    live: "idle", moved: Math.floor((Date.now() - hour) / 1000) },
  { id: "XR-9", kind: "goal", via: "registry", title: "цикл цели мимо дашборда", live: "dead" },
];

// Разговоры машины: список нужен экрану сам по себе, порогов и пачек он больше
// не обслуживает.
const chats = [
  { id: "bbbb2222-2222", state: "live", tmux: "chat-XR-2-1", project: "demo",
    mtime: stamp(3 * hour) },
  { id: "dddd4444-4444", state: "live", tmux: "chat-XR-4-1", project: "demo",
    mtime: stamp(2 * 60 * 1000) },
];

const stopped = [];
// Чем ручка снятия отвечает: удачей, спокойным «уже закрыта» либо отказом.
let stopSaid = { way: "drop", message: "сессия снята" };
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST" && path.includes("/stop")) {
    stopped.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return stopSaid;
  }
  if (init && init.method === "DELETE" && path.includes("/runs/")) {
    stopped.push({ path, body: null });
    return { id: "XR-1", message: "работа снята" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: { sections: [] }, works };
  if (path.includes("/chats")) return { chats, models: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

await go("#/agents");

const rows = () => allByClass(groups, "arow");
// Классы узла и всех его детей одной строкой: карточка сбоя помечена ими, а не
// словами, и искать её текстом значило бы ловить чужую фразу.
const collectClasses = (node) => {
  let out = String(node.className || "");
  for (const kid of node.children || []) {
    if (kid && typeof kid === "object") out += " " + collectClasses(kid);
  }
  return out;
};
const rowOf = (what) => rows().find((r) => dump(r).includes(what));


// --- кружок и слово состояния у каждой строки ---
{
  const cases = [
    // Идущая работа слова в строке не носит: её состояние несут бегущий
    // кружок и время работы справа, а чип «активна» между ними был третьим
    // указателем на одно и то же (разбор пользователя). Слово при этом никуда
    // не делось, оно в подсказке кружка.
    ["идущая работа", "", "pulse"],
    ["спросил и ждёт", "ждёт ответа", "dot-wait"],
    ["молчащее окно", "простаивает", "dot-idle"],
  ];
  for (const [what, word, cls] of cases) {
    const row = rowOf(what);
    if (!row) fail("строки «" + what + "» на экране нет: " + dump(groups).slice(0, 300));
    if (word && !dump(row).includes(word)) {
      fail("строка «" + what + "» не назвала состояние «" + word + "»: " + dump(row));
    }
    const dot = byClass(row, "dot");
    if (!dot || !String(dot.className).split(" ").includes(cls)) {
      fail("кружок строки «" + what + "» не по состоянию: " + (dot ? dot.className : "кружка нет"));
    }
  }
  // Зелёным горит только идущий ход: в этом и была вся жалоба.
  const green = rows().filter((r) => String((byClass(r, "dot") || {}).className || "").includes("pulse"));
  if (green.length !== 1 || !dump(green[0]).includes("идущая работа")) {
    fail("зелёным помечено не только работающее: " + green.map((r) => dump(r)).join(" | "));
  }
  // У идущей работы слово состояния живёт подсказкой кружка: чипа с ним в
  // строке нет, а знание не потеряно.
  {
    const row = rowOf("идущая работа");
    const dot = byClass(row, "dot");
    if (!dot || !String(dot.title || "").includes("активна")) {
      fail("у идущей работы состояние пропало вместе с чипом: " + JSON.stringify(dot && dot.title));
    }
    if (dump(row).includes("активна")) {
      fail("чип «активна» вернулся в строку: " + dump(row));
    }
  }

  // Давность последнего хода видна словами у молчащей строки.
  const said = dump(groups);
  if (/работает|tmux-сессия активна/.test(said)) {
    fail("состояние названо мимо словаря: " + said.slice(0, 300));
  }
  if (!/простаивает\s+3 ч/.test(dump(rowOf("молчащее окно")))) {
    fail("простой без давности хода: " + dump(rowOf("молчащее окно")));
  }
  // Работа, поднятая мимо дашборда, стоит в том же списке и зелёной тоже не
  // бывает: сессии дашборд не видит вовсе.
  const alien = rowOf("цикл цели мимо дашборда");
  if (!alien) fail("строки чужой работы нет: " + dump(groups).slice(0, 300));
  if (!dump(alien).includes("сессии нет")) fail("чужая работа не назвала состояние: " + dump(alien));
  const dot = byClass(alien, "dot");
  if (!String(dot.className).split(" ").includes("dot-other")) {
    fail("кружок чужой работы не по состоянию: " + dot.className);
  }
}

// --- закрытие молчащей сессии: одно нажатие, ручка та же, что у смены модели ---
{
  const row = rowOf("молчащее окно");
  const close = deepBtn(row, "sclose");
  if (!close) fail("у молчащей сессии нет кнопки закрытия: " + dump(row));
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = stopped[stopped.length - 1];
  if (!last || !last.path.includes("/chats/bbbb2222-2222/stop") || !last.body.drop) {
    fail("закрытие пошло не той ручкой: " + JSON.stringify(stopped));
  }
}

// --- занятая сессия снимается только со второго нажатия ---
{
  stopped.length = 0;
  await go("#/agents");
  const row = rowOf("спросил и ждёт");
  const close = deepBtn(row, "sclose");
  if (!close) fail("у ждущей сессии нет кнопки закрытия: " + dump(row));
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (stopped.length) fail("занятая сессия снялась без подтверждения: " + JSON.stringify(stopped));
  // Крестик подписи не держит, и взведённое состояние видно классом с
  // подсказкой: слово «Точно закрыть?» на значок не помещается.
  if (!String(close.className).split(" ").includes("armed")) {
    fail("кнопка не взвелась подтверждением: " + close.className);
  }
  if (!String(close.title || "").includes("Точно закрыть?")) {
    fail("взведённая кнопка не спросила подтверждения: " + close.title);
  }
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!stopped.length || !stopped[0].path.includes("/chats/aaaa1111-1111/stop")) {
    fail("подтверждённое закрытие не ушло: " + JSON.stringify(stopped));
  }
}

// --- пачки на экране нет вовсе ---
//
// Кнопка «Закрыть простаивающие» считала по разговорам всей машины, а таб
// показывает сессии проекта: при пяти видимых строках она предлагала снять два
// с половиной десятка, назвав их именами tmux, по которым не видно, с чем
// прощаешься (решение пользователя).
{
  stopped.length = 0;
  await go("#/agents");
  if (deepBtn(groups, "Закрыть простаивающие")) {
    fail("пачка вернулась на экран: " + dump(groups).slice(0, 300));
  }
  const said = dump(groups).replace(/\s+/g, " ");
  if (said.includes("простаивающие") || said.includes("Молчащих дольше")) {
    fail("на экране остались слова пачки: " + said.slice(0, 300));
  }
  // Механики её тоже нет: пустой ручки экран не зовёт и списка чатов машины
  // ради снятия не читает.
  const src = readFileSync(app, "utf8");
  for (const gone of ["sweepIdle", "sweepPick", "SWEEP_IDLE_HOURS", "sweepSaid"]) {
    if (src.includes(gone)) fail("механика пачки осталась в коде: " + gone);
  }
}

// --- кнопка снятия есть у всякой строки со своей tmux-сессией ---
//
// Разговорная сессия конвейера приезжает без id сессии (его заполняет только
// транскрипт), и снималась она ручкой работы, а кнопки у неё не было вовсе:
// на живой машине такими были почти все строки (замечание пользователя).
{
  stopped.length = 0;
  await go("#/agents");
  const row = rowOf("договорившая сессия задачи");
  if (!row) fail("строки договорившей сессии на экране нет: " + dump(groups).slice(0, 300));
  // «Стоп» ей не адресован: ход кончился, снимают тут окно.
  if (deepBtn(row, "Стоп")) fail("договорившей сессии предложен стоп хода: " + dump(row));
  const close = deepBtn(row, "sclose");
  if (!close) fail("у сессии без id нет кнопки снятия: " + dump(row));
  close.handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = stopped[stopped.length - 1];
  if (!last || !last.path.includes("/runs/XR-5")) {
    fail("снятие сессии без id пошло не той ручкой: " + JSON.stringify(stopped));
  }
  // И эта строка уходит тем же ходом: снятие ручкой работы прежде оставляло её
  // стоять, а список сессий на живой машине из таких строк и состоит.
  if (rowOf("договорившая сессия задачи")) {
    fail("снятая ручкой работы строка осталась стоять: " + dump(groups).slice(0, 300));
  }
  sandbox.renderSessions("demo", works, "");
  await settle();
  if (rowOf("договорившая сессия задачи")) {
    fail("снятая ручкой работы строка вернулась опросом: " + dump(groups).slice(0, 300));
  }
  // Идущая работа остаётся при своём «Стопе»: снимают там ход, а не окно.
  // Стоп в строке стоит значком, и узнают его подписью для чтения с экрана:
  // слово рядом с чатом и крестиком забирало колонку под подпись.
  const busyRow = rowOf("идущая работа");
  if (!deepBtn(busyRow, "rstop")) fail("у идущей работы пропал стоп: " + dump(busyRow));
}

// --- работу, которую закрыть нечем, называет собой погашенная кнопка ---
// Приписок в хвосте строки («поднята вне дашборда», «идёт вне дашборда») тут
// больше нет: они были третьим способом сказать одно и то же после чипа
// «внешняя» и подсказки строки, а трёх разных объяснений одного бессилия
// человек читал как три разных беды. Погашенная кнопка говорит «действия нет»
// короче любых слов и не отнимает у строки колонку (решение пользователя).
{
  await go("#/agents");
  for (const what of ["окно vscode", "цикл цели мимо дашборда"]) {
    const row = rowOf(what);
    if (!row) fail("строки «" + what + "» на экране нет: " + dump(groups).slice(0, 300));
    const shut = byClass(row, "sclose");
    if (!shut) fail("у строки «" + what + "» пропала кнопка закрытия: " + dump(row));
    if (!shut.disabled || deepBtn(row, "Стоп")) {
      fail("строке «" + what + "» предложено снятие, которого нет: " + dump(row));
    }
    // Разбор живёт в подсказке кнопки: слова одни на все случаи чужой работы.
    if (!String(shut.title || "").includes("там же, где открыта")) {
      fail("погашенная кнопка «" + what + "» не сказала, где эту работу закрывают: " + shut.title);
    }
    const said = dump(row);
    for (const gone of ["поднята вне дашборда", "идёт вне дашборда", "снимать нечем",
      "имени сессии нет в реестре"]) {
      if (said.includes(gone)) fail("снятая приписка вернулась в строку «" + what + "»: " + said);
    }
  }
  // У строки, чью сессию поднял дашборд, та же кнопка живая: гасит её
  // отсутствие имени tmux-сессии, а не вид работы и не состояние.
  const still = rowOf("молчащая вторая");
  const mine = still && byClass(still, "sclose");
  if (!mine || mine.disabled) fail("своей работе закрытие погашено: " + dump(still || groups));
}

// --- удачное снятие убирает строку тем же ходом и опрос её не возвращает ---
//
// Сервер узнаёт о снятии не сразу: он смотрит список tmux, а тот успевает
// ответить по-старому. Строка, вернувшаяся следующим опросом, читается как
// несработавшее нажатие (замечание пользователя).
{
  stopped.length = 0;
  stopSaid = { way: "drop", message: "сессия снята" };
  await go("#/agents");
  const row = rowOf("молчащая вторая");
  if (!row) fail("строки молчащей сессии на экране нет: " + dump(groups).slice(0, 300));
  deepBtn(row, "sclose").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (rowOf("молчащая вторая")) {
    fail("снятая строка осталась стоять тем же ходом: " + dump(groups).slice(0, 300));
  }
  // Опрос приходит с прежним списком: сервер ещё не договорил.
  sandbox.renderSessions("demo", works, "");
  await settle();
  if (rowOf("молчащая вторая")) {
    fail("снятая строка вернулась опросом: " + dump(groups).slice(0, 300));
  }
}

// --- сессии уже нет: это не сбой, а сделанное дело ---
//
// Живой случай: tmux-сессии на машине давно не было, ручка отвечала отказом
// «снимать под перезапуск нечего», экран показывал красную карточку, а строка
// оставалась стоять, и второе нажатие упиралось в тот же отказ. Человек решил,
// что кнопка не работает (замечание пользователя).
{
  stopped.length = 0;
  stopSaid = { way: "gone", message: "сессия уже закрыта" };
  await go("#/agents");
  const row = rowOf("молчащая третья");
  if (!row) fail("строки молчащей сессии на экране нет: " + dump(groups).slice(0, 300));
  deepBtn(row, "sclose").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (rowOf("молчащая третья")) {
    fail("строка уже закрытой сессии осталась стоять: " + dump(groups).slice(0, 300));
  }
  const flashes = byId.get("flashes");
  const said = dump(flashes).replace(/\s+/g, " ");
  if (/\berr\b/.test(collectClasses(flashes))) {
    fail("уже закрытая сессия показана карточкой сбоя: " + said);
  }
  if (said.includes("нечего") || said.includes("не живёт")) {
    fail("спокойная новость сказана словами поломки: " + said);
  }
  stopSaid = { way: "drop", message: "сессия снята" };
}

// --- одно состояние одним словом во всех показах ---
//
// Прежде активная сессия звалась «работает» в табе, «tmux-сессия активна» на
// форме задачи и «активна» в снимке tmux: три слова об одном (замечание
// пользователя).
{
  const say = (node) => dump(node).replace(/\s+/g, " ").trim();
  const onForm = sandbox.liveChip({ id: "XR-1", via: "tmux", live: "busy" });
  if (!onForm || !say(onForm).includes("активна")) {
    fail("форма задачи назвала состояние иначе: " + (onForm ? say(onForm) : "чипа нет"));
  }
  if (say(onForm).includes("tmux-сессия")) {
    fail("на форме осталось «tmux-сессия активна»: " + say(onForm));
  }
  // Происхождение работы ушло в подсказку: это про то, кто её ведёт, а не про
  // то, идёт ли она.
  if (!String(onForm.title || "").includes("сессия дашборда")) {
    fail("форма не говорит, чья это сессия: " + JSON.stringify(onForm.title));
  }
  const inList = sandbox.chatOption("demo", { id: "s1", state: "live", idle: false }, "");
  if (!say(inList).includes("активна")) {
    fail("список чатов назвал живую сессию иначе: " + say(inList));
  }
  // И в табе сессий то же слово, из того же словаря.
  const inTab = sandbox.workLiveChip({ live: "busy" }, Date.now());
  if (say(inTab) !== "активна") fail("таб сессий назвал состояние иначе: " + say(inTab));
}

console.log("poc_agentlive: ok");

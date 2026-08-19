// Стенд ленты разговора (DK-371, панель DK-435). Экран агента и чат цели
// держали по своей копии разбора реплик, стрима и пагинации; после выноса лента
// стала одна, а после слияния экранов она и рисуется одна: пузыри разговора и
// строки инструментов идут вперемешку, потому что живой статус и чат это теперь
// один элемент. Предмет проверки тут её механика, а не написанное в исходнике:
// хвост при открытии, отсев повторов потока, подгрузка от прокрутки вверх без
// кнопки, надпись начала разговора и память позиции при уходе и возврате
// (DK-434). Стенд поднимает static/app.js в песочнице node с заглушкой DOM и
// гоняет ленту панели по одному и тому же разговору.
//
// Зовётся из go-теста (sessions_test.go), путь к статике приходит аргументом.

import fs from "node:fs";
import vm from "node:vm";

const appPath = process.argv[2];
if (!appPath) {
  console.error("нужен путь до static/app.js");
  process.exit(2);
}

function makeNode(tag) {
  const node = {
    tagName: String(tag || "div").toUpperCase(),
    className: "",
    textContent: "",
    title: "",
    hidden: false,
    disabled: false,
    value: "",
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    children: [],
    attrs: {},
    style: {},
    dataset: {},
    handlers: {},
  };
  node.classList = {
    add: (...cls) => { node.className = (node.className + " " + cls.join(" ")).trim(); },
    remove: () => {},
    contains: (cls) => node.className.split(" ").includes(cls),
    toggle: () => {},
  };
  node.append = (...kids) => { node.children.push(...kids); };
  node.appendChild = (kid) => { node.children.push(kid); return kid; };
  node.prepend = (...kids) => { node.children.unshift(...kids); };
  node.replaceChildren = (...kids) => { node.children = kids; };
  // Лента перерисовывается по месту (sync), а не собирается заново: узел с
  // прежним ключом остаётся тем же узлом, и порядок правится вставкой.
  node.insertBefore = (kid, ref) => {
    const at = ref ? node.children.indexOf(ref) : -1;
    if (at < 0) node.children.push(kid);
    else node.children.splice(at, 0, kid);
    return kid;
  };
  node.removeChild = (kid) => {
    const at = node.children.indexOf(kid);
    if (at >= 0) node.children.splice(at, 1);
    return kid;
  };
  node.remove = () => {};
  node.setAttribute = (name, value) => { node.attrs[name] = String(value); };
  node.removeAttribute = (name) => { delete node.attrs[name]; };
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => {};
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = () => {};
  node.querySelector = () => null;
  node.querySelectorAll = () => [];
  node.closest = () => null;
  Object.defineProperty(node, "childElementCount", { get: () => node.children.length });
  Object.defineProperty(node, "firstChild", { get: () => node.children[0] || null });
  return node;
}

// Текст поддерева одной строкой: реплика лежит в нескольких узлах.
function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
}

// Сколько раз слова встретились в поддереве: задвоенный хвост виден именно
// вторым вхождением той же реплики.
function times(node, want) {
  return dump(node).split(want).length - 1;
}

const fail = (msg) => { console.error(msg); process.exit(1); };

const streams = [];
const asked = [];
const byId = new Map();

// Разговор стенда: текстовые реплики вперемешку с инструментом и
// размышлениями. Чат берёт из него только текст, транскрипт показывает всё, и
// разница эта задана параметром отбора, а не второй лентой.
const talk = [
  { seq: 5, role: "user", text: "как дела с витком", time: "2026-08-13T10:00:00+03:00" },
  { seq: 6, role: "tool", tool: "Bash", note: "go test" },
  { seq: 7, role: "thinking", text: "сначала посмотрю строку доски" },
  { seq: 8, role: "assistant", text: "виток идёт, задачи режу", time: "2026-08-13T10:01:00+03:00" },
];
// История до хвоста: её тянет прокрутка вверх через ?before=.
const older = [
  { seq: 3, role: "user", text: "подними виток", time: "2026-08-12T09:00:00+03:00" },
  { seq: 4, role: "assistant", text: "поднял", time: "2026-08-12T09:01:00+03:00" },
];

const session = { id: "abc-12345678", mtime: "2026-08-13T10:01:00+03:00", branch: "dk-371",
  task: "XR-100", taskNote: "по дереву задачи", first: "Выполни XR-100" };

// Соседний разговор той же задачи: на него переключаются с переключателя, и на
// нём же проверяется уход с ленты посреди запроса.
const neighbour = { id: "def-87654321", mtime: "2026-08-12T09:01:00+03:00", branch: "dk-371",
  task: "XR-100", taskNote: "по дереву задачи", first: "Верни XR-100 на доработку" };

// Короткий разговор: вся история умещается в хвост, и открытие сразу
// упирается в начало (firstSeq === 0), без единой подгрузки (DK-434).
const shortTalk = [
  { seq: 0, role: "user", text: "быстрый вопрос", time: "2026-08-14T09:00:00+03:00" },
  { seq: 1, role: "assistant", text: "быстрый ответ", time: "2026-08-14T09:00:30+03:00" },
];
const shortSession = { id: "ccc-11112222", mtime: "2026-08-14T09:00:30+03:00", branch: "dk-371",
  task: "XR-100", taskNote: "по дереву задачи", first: "Короткий вопрос" };

// Свой id для сценария памяти позиции: session к этому месту стенда уже
// прошёл подгрузки в сценарии транскрипта и гонки запросов, и его feedPlace
// не пустой, а сценарий позиции проверяет заход именно с чистой памяти.
const deepSession = { id: "eee-99998888", mtime: "2026-08-14T10:00:00+03:00", branch: "dk-371",
  task: "XR-100", taskNote: "по дереву задачи", first: "Глубокий разговор" };

// Задержанный ответ сервера: пока id разговора лежит здесь, запрос его хвоста
// висит без ответа, и стенд отвечает на него сам, явным вызовом. Такой случай
// не берётся мгновенно разрешённым обещанием: щель между запросом хвоста и
// подъёмом потока в нём просто не открывается.
let heldSession = "";
let release = null;

// Пустой разговор: реплик нет вовсе, и сервер называет это словами и в ответе,
// и первым событием потока.
const emptyNote = "в транскрипте пока нет реплик";
let empty = false;

// Рост коробки прокрутки от подгруженной страницы истории: настоящий браузер
// пересчитывает scrollHeight сам, когда в дерево встают старые реплики, а
// заглушка DOM этого не делает, и тест обязан подать рост явно, иначе высота
// после досбора глубины (restorePlace в app.js) осталась бы той же, что и у
// одного хвоста, и не поймала бы клампинг к нулю (замечание ревью DK-434).
let growScroll = null;

function reply(body) {
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

const sandbox = {
  console: { log: () => {}, error: (...args) => { console.error(...args); }, warn: () => {} },
  setTimeout: () => 0,
  clearTimeout: () => {},
  setInterval: () => 0,
  clearInterval: () => {},
  Date,
  JSON,
  document: {
    createElement: makeNode,
    createTextNode: (text) => {
      const n = makeNode("#text");
      n.textContent = String(text);
      return n;
    },
    getElementById: (id) => {
      if (!byId.has(id)) {
        const node = makeNode("div");
        node.content = { querySelector: () => null };
        byId.set(id, node);
      }
      return byId.get(id);
    },
    addEventListener: () => {},
    body: makeNode("body"),
    documentElement: { style: { setProperty: () => {} } },
  },
  window: {
    addEventListener: () => {},
    removeEventListener: () => {},
    innerWidth: 1200,
    matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
  },
  location: { hash: "", href: "", replace: () => {} },
  localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  EventSource: class {
    constructor(url) {
      this.url = url;
      this.closed = false;
      this.listeners = {};
      if (String(url).includes("/sessions/")) streams.push(this);
    }
    addEventListener(name, fn) { this.listeners[name] = fn; }
    close() { this.closed = true; }
  },
  fetch: (path) => {
    asked.push(path);
    if (path.includes("/sessions?task=")) return reply({ sessions: [session, neighbour] });
    if (path.includes("/sessions/")) {
      const sid = path.slice(path.indexOf("/sessions/") + "/sessions/".length).split("?")[0];
      const head = sid === neighbour.id ? neighbour : session;
      if (heldSession && sid === heldSession) {
        return new Promise((res) => {
          release = () => res({ ok: true, status: 200,
            json: () => Promise.resolve({ session: sid, head, total: talk.length, items: talk }) });
        });
      }
      if (empty) return reply({ session: sid, head, total: 0, items: [], note: emptyNote });
      if (sid === shortSession.id) {
        return reply({ session: sid, head: shortSession, total: shortTalk.length, items: shortTalk });
      }
      if (path.includes("before=")) {
        if (growScroll) growScroll();
        return reply({ session: sid, head, items: older });
      }
      return reply({ session: sid, head, total: talk.length + older.length, items: talk });
    }
    return reply({ sessions: [] });
  },
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

const settle = async () => {
  for (let i = 0; i < 100; i += 1) await Promise.resolve();
};

// Хвост потока приходит заново: сервер шлёт последние реплики первым делом
// (streamSession в sessions.go), и лента, открытая ответом ?n=, обязана их
// отсеять по seq.
const restream = (es, items) => {
  for (const item of items) es.onmessage({ data: JSON.stringify(item) });
};

// Надпись начала разговора и коробка реплик: обе лежат в коробке ленты,
// первой надпись (DK-434: кнопки «раньше» больше нет, подгрузка сама встаёт
// от прокрутки вверх).
const startOf = (box) => box.children[0];
const listOf = (box) => box.children[1];

// Подгрузка от прокрутки вверх: коробка ставится у самого верха, и слушатель
// прокрутки на ней получает событие, как от настоящего скролла в браузере.
const scrollUp = (scroll, height) => {
  scroll.scrollHeight = height;
  scroll.clientHeight = 300;
  scroll.scrollTop = 0;
  scroll.handlers.scroll();
};

// Панель разговора: лента открывается хвостом и прокручивается сама, реплики
// человека и агента идут пузырями, а вызов инструмента и размышления строкой
// между ними.
const box = makeNode("div");
sandbox.wireChatFeed("demo", box, session.id);
await settle();

if (!asked.some((p) => p.includes("/sessions/" + session.id + "?n=40"))) {
  fail("панель открылась не хвостом: " + JSON.stringify(asked));
}
if (streams.length !== 1) {
  fail("на один разговор поднято потоков: " + streams.length);
}
if (!dump(box).includes("как дела с витком") || !dump(box).includes("виток идёт")) {
  fail("хвост разговора не встал в ленту панели: " + dump(box));
}
// Размышления теперь едут текстом свёрнутым блоком, а не меткой (POC ветки
// poc-chat): по метке «размышления свёрнуты» о ходе мысли не сказано ничего.
if (!dump(box).includes("сначала посмотрю строку доски") || !dump(box).includes("Bash")) {
  fail("лента потеряла инструмент или размышления: чем занят агент, снова не видно");
}
restream(streams[0], talk);
await settle();
if (times(box, "как дела с витком") !== 1) {
  fail("хвост встал в ленту дважды: поток шлёт свои реплики заново, и отсева по seq нет");
}
restream(streams[0], [{ seq: 9, role: "assistant", text: "нарезал три штуки",
  time: "2026-08-13T10:05:00+03:00" }]);
await settle();
if (!dump(box).includes("нарезал три штуки")) {
  fail("живое дострение не дошло до ленты панели: " + dump(box));
}
if (!startOf(box).hidden) {
  fail("надпись начала разговора видна при непрочитанной истории");
}
if (startOf(box).handlers.click) {
  fail("на надписи начала остался обработчик клика: кнопка «раньше» не убрана");
}
if (dump(box).includes("раньше")) {
  fail("в ленте панели осталось слово «раньше»: кнопка не убрана до конца");
}
// Подгрузка идёт от одной прокрутки, без клика: коробка панели встаёт у
// самого верха, и слушатель тянет историю сам, незаметно для взгляда.
scrollUp(box, 500);
await settle();
if (!asked.some((p) => p.includes("before=5&n=40"))) {
  fail("подгрузка от прокрутки просит историю не от первой показанной реплики: " +
    JSON.stringify(asked));
}
if (!dump(box).includes("подними виток")) {
  fail("история не встала над лентой панели: " + dump(box));
}
if (dump(listOf(box)).indexOf("подними виток") > dump(listOf(box)).indexOf("как дела с витком")) {
  fail("история встала под хвостом, а не над ним: " + dump(listOf(box)));
}

// Гонка запросов: прокрутка у самого верха шлёт событие на каждый пиксель, и
// без защиты вторая подгрузка ушла бы вторым запросом, пока висит первый.
asked.length = 0;
heldSession = session.id;
scrollUp(box, 500);
scrollUp(box, 500);
scrollUp(box, 500);
if (asked.filter((p) => p.includes("before=")).length !== 1) {
  fail("подряд подгрузки ушли не одним запросом: " + JSON.stringify(asked));
}
if (!release) fail("стенд не поймал запрос подгрузки, вставший на паузу");
release();
await settle();
heldSession = "";
release = null;

// Конец истории назван словами, а не бесконечным ожиданием: у короткого
// разговора весь хвост уже вся история, и надпись видна сразу, без единой
// подгрузки (DK-434).
asked.length = 0;
sandbox.closeChatLive();
const shortBox = makeNode("div");
sandbox.wireChatFeed("demo", shortBox, shortSession.id);
await settle();
if (startOf(shortBox).hidden) {
  fail("надпись начала разговора не встала у короткой истории: " + dump(shortBox));
}
if (!dump(shortBox).includes("это начало разговора")) {
  fail("конец истории не назван словами: " + dump(shortBox));
}
scrollUp(shortBox, 300);
await settle();
if (asked.some((p) => p.includes("before="))) {
  fail("у начала разговора всё равно ушёл запрос подгрузки: " + JSON.stringify(asked));
}

// Вторая лента в той же вкладке: своя коробка, свой поток, и открывается она
// тем же куском.
sandbox.closeChatLive();
asked.length = 0;
streams.length = 0;
const feed = makeNode("div");
await sandbox.wireChatFeed("demo", feed, session.id);
await settle();

if (!asked.some((p) => p.includes("/sessions/" + session.id + "?n=40"))) {
  fail("вторая лента открылась не хвостом: " + JSON.stringify(asked));
}
if (streams.length !== 1) {
  fail("на ленту панели поднято потоков: " + streams.length);
}
if (listOf(feed).className !== "mlist") {
  fail("лента панели собрана мимо своей коробки: " + listOf(feed).className);
}
if (!dump(feed).includes("как дела с витком") || !dump(feed).includes("виток идёт")) {
  fail("хвост разговора не встал во вторую ленту: " + dump(feed));
}
restream(streams[0], talk);
await settle();
if (times(feed, "как дела с витком") !== 1) {
  fail("хвост встал дважды: отсев повторов потока пропал");
}
if (dump(feed).includes("раньше")) {
  fail("во второй ленте осталось слово «раньше»: кнопка не убрана до конца");
}
scrollUp(feed, 500);
await settle();
if (!asked.some((p) => p.includes("before=5&n=40"))) {
  fail("подгрузка от прокрутки просит историю не тем адресом: " + JSON.stringify(asked));
}
if (!dump(feed).includes("подними виток")) {
  fail("история не встала над второй лентой: " + dump(feed));
}
// Разделитель дня приезжает вместе с историей: реплики разных дней разведены
// им, и пересчёт ленты ставит его сам.
if (!dump(feed).includes(sandbox.localDay(older[0].time))) {
  fail("день истории не назван разделителем: " + dump(feed));
}

// Уход с ленты, пока висит ответ сервера. Поток поднимается после ответа, и
// между запросом хвоста и подъёмом потока открыта щель: переключение на
// соседний разговор в неё попадает. Запоздавший ответ не должен идти дальше,
// иначе за спиной у открытой ленты поднимется второй поток и станет дописывать
// реплики прежнего разговора в снятую с экрана коробку. До выноса ленты (DK-371)
// не было щели вовсе: поток открывался тем же ходом, что и лента.
asked.length = 0;
streams.length = 0;
sandbox.closeChatLive();
const raceBox = makeNode("div");

heldSession = session.id;
sandbox.wireChatFeed("demo", raceBox, session.id);
await settle();
if (streams.length !== 0) {
  fail("поток поднят до ответа сервера: " + JSON.stringify(streams.map((s) => s.url)));
}
if (!release) fail("стенд не поймал запрос хвоста первого разговора");
const releaseFirst = release;

// Переключение на соседний разговор: прежняя лента снимается, её поток
// закрывается, и открывается лента соседа.
sandbox.closeChatLive();
sandbox.wireChatFeed("demo", raceBox, neighbour.id);
await settle();
if (streams.length !== 1 || !streams[0].url.includes(neighbour.id)) {
  fail("после переключения открыт не разговор соседа: " +
    JSON.stringify(streams.map((s) => s.url)));
}

// Запоздавший ответ первого разговора: поток по нему не поднимается, и лента
// соседа остаётся на экране одна.
releaseFirst();
await settle();
if (streams.length !== 1) {
  fail("запоздавший ответ поднял второй поток: " + JSON.stringify(streams.map((s) => s.url)));
}
if (streams.some((s) => !s.closed && s.url.includes(session.id))) {
  fail("после ухода с ленты остался живой поток прежнего разговора: " +
    JSON.stringify(streams.filter((s) => !s.closed).map((s) => s.url)));
}
heldSession = "";
release = null;

// Позиция ленты держится при уходе с экрана и возврате, у каждого разговора
// своя (DK-434). raceBox это та же коробка прокрутки, что и у соседнего
// разговора, ровно как в настоящей панели: смена разговора меняет ленту внутри
// неё, а не саму коробку. Разговор свой, deepSession: у session к этому месту
// стенда уже накопилась память из сценария транскрипта и гонки запросов, и
// для захода с чистой памятью нужен свежий id. Реалистичный случай сложнее
// простого «запомнил scrollTop»: до ухода была подгрузка вверх, и высота
// коробки на момент записи места (rest) больше, чем у одного свежего хвоста
// после возврата.
sandbox.closeChatLive();
sandbox.wireChatFeed("demo", raceBox, deepSession.id);
await settle();
// Подгрузка вверх до ухода: лента уже не хвост, а хвост плюс страница
// истории, и firstSeq сдвинут с 5 на 3.
scrollUp(raceBox, 1000);
await settle();
if (!asked.some((p) => p.includes("before=5&n=40"))) {
  fail("перед уходом история не подгрузилась: " + JSON.stringify(asked));
}
raceBox.scrollHeight = 1000;
raceBox.clientHeight = 300;
raceBox.scrollTop = 200;
raceBox.handlers.scroll();

// Уход с разговора: переключение на соседа снимает ленту и открывает ленту
// neighbour, у которой памяти о месте ещё нет, и она встаёт вниз, как при
// первом заходе. Высота коробки сброшена к размеру одного хвоста: свежий
// заход соседа его и приносит, старая большая высота ему не принадлежит.
raceBox.scrollHeight = 150;
sandbox.closeChatLive();
sandbox.wireChatFeed("demo", raceBox, neighbour.id);
await settle();
if (raceBox.scrollTop !== raceBox.scrollHeight) {
  fail("разговор без памяти о месте встал не вниз: " + raceBox.scrollTop);
}

// Возврат на прежний разговор: свежий заход снова приносит только хвост (та
// же высота одного хвоста, 150, а не хвоста с подгруженной историей), и
// честное восстановление обязано сначала дособрать историю до прежней
// глубины (firstSeq 5 -> 3, тот же запрос, что и до ухода) и только потом
// встать на прежнее место, а не клампиться к нулю против чужой, куда меньшей
// высоты (замечание ревью DK-434). growScroll подаёт рост коробки от
// подгруженной страницы, который в настоящем браузере посчитал бы сам layout.
raceBox.scrollHeight = 150;
asked.length = 0;
growScroll = () => { raceBox.scrollHeight = 1000; };
sandbox.closeChatLive();
sandbox.wireChatFeed("demo", raceBox, deepSession.id);
await settle();
if (!asked.some((p) => p.includes("before=5&n=40"))) {
  fail("возврат не дособрал историю до прежней глубины: " + JSON.stringify(asked));
}
if (raceBox.scrollTop !== 200) {
  fail("позиция разговора не вернулась на прежнее место: " + raceBox.scrollTop +
    ", ожидал 200 (хвост короче прежней ленты, а восстановление не дособрало историю)");
}
growScroll = null;

// Пустой разговор говорит словами: молчащая коробка неотличима от
// оборвавшегося потока.
sandbox.closeChatLive();
empty = true;
const quiet = makeNode("div");
await sandbox.wireChatFeed("demo", quiet, session.id);
await settle();
if (!dump(quiet).includes(emptyNote)) {
  fail("пустая лента молчит: " + dump(quiet));
}

console.log("лента панели: разговор открывается хвостом, повтор потока отсеивается," +
  " история подгружается от прокрутки вверх без кнопки и без повторных запросов," +
  " начало разговора названо словами, позиция ленты держится при уходе и возврате," +
  " пузыри и строки инструментов стоят одной лентой," +
  " уход с ленты посреди запроса потока не поднимает, пустота названа словами");

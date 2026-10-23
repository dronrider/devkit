// Стенд пары кнопок на форме записи (DK-370, LLD DK-354 решение 5, ветка
// poc-chat), второй круг по макету DK-447.
//
// Прежде запись кончалась одной кнопкой «Записать черновик» и промежуточным
// экраном «Черновик записан» с кнопками «Записать ещё», «В накопитель» и «На
// доску». Кнопок теперь две, и расходятся они дорогой после записи:
// «Сохранить» возвращает в накопитель, «Сохранить и грумить» той же ручкой
// пишет запись, поднимает разбор и открывает экран записи. Предмет стенда это
// обе дороги, отсутствие промежуточного экрана и общий рубеж пары: у пустого
// черновика гаснут обе кнопки.
//
// Второй круг добавляет вид формы по макету: подписи полей стоят в рамках,
// отказ подсвечивает поле на месте (пустота после попытки записи, длина сразу
// со счётчиком), уровень разбора стоит строкой со значком информации, а выход
// это крестик в углу с вопросом о потере текста.
//
// Зовётся: node testdata/poc_savegroom.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const posted = [];
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push(path);
    if (path.endsWith("/drafts")) {
      return { id: "XR-D7", file: "docs/tasks/drafts/XR-D7.md", message: "черновик записан" };
    }
    if (path.includes("/groom")) return { message: "груминг поднят" };
  }
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/drafts")) {
    return { drafts: [{ id: "XR-D7", title: "свежая мысль", written: "2026-08-26",
      age_days: 0, order: "Проведи груминг XR-D7" }] };
  }
  if (path.includes("/drafts/XR-D7")) {
    return { id: "XR-D7", file: "docs/tasks/drafts/XR-D7.md", text: "свежая мысль",
      order: "Проведи груминг XR-D7" };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Поля формы черновика: своего класса у них нет, различаются подписью для
// чтения с экрана. Наименование и описание стоят раздельными полями в рамках
// с подписью на верхней границе (DK-447, макет), и в описании лежит шаблон
// разделов.
const area = (label) => (function find(node) {
  if (node.tagName === "TEXTAREA" &&
    (!label || String(node.attrs["aria-label"] || "") === label)) return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && kid && find(kid);
    if (got) return got;
  }
  return null;
})(groups);

const put = (label, text) => {
  const ta = area(label);
  if (!ta) fail("поля «" + label + "» на форме нет: " + dump(groups).slice(0, 300));
  ta.value = text;
  ta.handlers.input({});
};

// Записи черновика хватает непустого наименования: описание человек правит
// руками, и снятый раздел записи не мешает.
const fill = async (title) => {
  put("наименование черновика", title);
  if (title.trim()) put("описание черновика", "### Ситуация\n\nзамечено на прогоне.\n");
  await settle();
};

// --- на форме записи стоят обе кнопки, старой одиночной нет ---
{
  await go("#demo/new/draft");
  await fill("мысль с телефона");
  if (deepBtn(groups, "Записать черновик")) {
    fail("одиночная кнопка записи осталась на форме: " + dump(groups).slice(0, 300));
  }
  for (const want of ["Сохранить", "Сохранить и грумить"]) {
    if (!deepBtn(groups, want)) {
      fail("кнопки «" + want + "» на форме записи нет: " +
        dump(groups).replace(/\s+/g, " ").slice(0, 400));
    }
  }
  // Подписей под формой нет вовсе: они пересказывали устройство и объясняли
  // кнопки, чьи подписи их и называют (замечание пользователя). Пометка про
  // то, что черновику доступен только груминг, тоже снята: она говорила
  // неправду, у формы есть «Сохранить и выполнить». Приписка уровня разбора со
  // шкалой снята по макету: смысл уровня спрашивают значком информации.
  const said = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["вернёт в накопитель", "поднимет разбор", "Ляжет в docs/tasks/drafts/",
    "Бакет считается", "Черновику доступен только груминг", "в работу его не взять",
    "уровень разбора на глаз", "Заголовок-исход"]) {
    if (said.includes(gone)) fail("на форме осталась подпись «" + gone + "»: " + said.slice(0, 400));
  }
  if (!area("наименование черновика") || !area("описание черновика")) {
    fail("на форме нет раздельных полей наименования и описания: " + said.slice(0, 400));
  }
  // Подсказок внутри полей нет: подпись стоит на верхней границе рамки, и поле
  // молчит до ввода (макет DK-447).
  if (area("наименование черновика").placeholder || area("описание черновика").placeholder) {
    fail("подсказка внутри поля черновика не снята");
  }
}

// --- уровень разбора стоит строкой, подсказка за значком информации ---
{
  const row = byClass(groups, "lvlrow");
  if (!row) fail("строки уровня разбора на форме нет: " + dump(groups).slice(0, 300));
  const said = dump(row);
  if (!said.includes("Уровень разбора:")) fail("подпись уровня не встала в строку: " + said);
  const tip = byClass(groups, "itip");
  const info = deepBtn(groups, "infobtn");
  if (!tip || !info) fail("значка информации с подсказкой нет: " + dump(groups).slice(0, 300));
  if (!tip.hidden) fail("подсказка уровня открыта без нажатия значка");
  info.handlers.click({ stopPropagation: () => {} });
  if (tip.hidden || !tip.textContent.includes("важность черновика")) {
    fail("тап по значку не раскрыл подсказку уровня: " + tip.textContent);
  }
  info.handlers.click({ stopPropagation: () => {} });
  if (!tip.hidden) fail("повторный тап не закрыл подсказку уровня");
}

// --- рубеж пары кнопок: нетронутая форма гасит обе, отказ подсвечивает поле ---
{
  await go("#demo/new/draft");
  put("наименование черновика", "");
  put("описание черновика", "");
  await settle();
  if (!deepBtn(groups, "Сохранить").disabled || !deepBtn(groups, "Сохранить и грумить").disabled) {
    fail("у нетронутой формы кнопки живые");
  }
  // Описание без наименования держит кнопки живыми: пустое обязательное поле
  // молчит, пока сохранить не попробовали (макет DK-447, состояние 2).
  put("описание черновика", "замечено на прогоне.");
  if (deepBtn(groups, "Сохранить").disabled) {
    fail("описание без наименования погасило кнопки до попытки записи");
  }
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const frame = byClass(groups, "dfield");
  const msg = byClass(groups, "dmsg");
  if (!frame || !frame.classList.contains("err")) {
    fail("пустое наименование не подсветилось после попытки записи");
  }
  if (!msg || msg.textContent !== "поле обязательно для заполнения") {
    fail("отказ обязательного поля не встал под рамку: " + (msg && msg.textContent));
  }
  if (!deepBtn(groups, "Сохранить").disabled) {
    fail("после неудачной попытки кнопки записи остались живыми");
  }
  // Годное наименование снимает подсветку и возвращает кнопки.
  put("наименование черновика", "мысль с телефона");
  if (frame.classList.contains("err") || msg.textContent) {
    fail("подсветка не снялась годным наименованием: " + msg.textContent);
  }
  if (deepBtn(groups, "Сохранить").disabled) {
    fail("годное наименование не вернуло кнопки");
  }
  // Порог длины работает сразу и называет живой счётчик (макет DK-447,
  // состояние 4).
  put("наименование черновика", "м".repeat(93));
  if (!frame.classList.contains("err")) fail("порог длины не подсветил поле");
  if (msg.textContent !== "превышена допустимая длина поля (93 из 72)") {
    fail("порог длины молчит или без счётчика: " + msg.textContent);
  }
  if (!deepBtn(groups, "Сохранить").disabled) fail("порог длины не погасил кнопки");
  put("наименование черновика", "мысль с телефона");
  await settle();
}

// --- крестик закрывает пустую форму молча, набранную спрашивает ---
{
  // Пустая форма закрывается сразу: шаблон в описании не в счёт написанного.
  put("наименование черновика", "");
  put("описание черновика", "");
  await settle();
  if (!byClass(groups, "xscrim")) fail("вопроса выхода нет на форме");
  deepBtn(groups, "xbtn").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("пустая форма не закрылась крестиком: " + sandbox.location.hash);
  }

  // Набранная форма спрашивает карточкой под крестиком (макет DK-447,
  // состояние 5). Форма перерисовалась, и вопрос ищется заново.
  await go("#demo/new/draft");
  await fill("мысль про выход");
  let scrim = byClass(groups, "xscrim");
  if (!scrim) fail("вопроса выхода нет на форме");
  if (!scrim.hidden) fail("вопрос выхода открыт без нажатия");
  deepBtn(groups, "xbtn").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (scrim.hidden) fail("крестик не раскрыл вопрос о потере текста");
  const asked = dump(scrim);
  if (!asked.includes("Текст черновика будет утерян. Продолжить?")) {
    fail("вопрос о потере стоит не теми словами: " + asked);
  }
  for (const want of ["Не записывать", "Остаться"]) {
    if (!deepBtn(scrim, want)) fail("в вопросе выхода нет кнопки «" + want + "»");
  }
  // «Остаться» закрывает вопрос, форма и набранный текст стоят.
  deepBtn(scrim, "Остаться").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!scrim.hidden) fail("«Остаться» не закрыла вопрос");
  const kept = area("наименование черновика");
  if (!kept || kept.value !== "мысль про выход") fail("«Остаться» потеряла набранный текст");
  // Клик мимо карточки это тоже «остаться».
  deepBtn(groups, "xbtn").handlers.click({ stopPropagation: () => {} });
  scrim.handlers.click({ target: scrim, stopPropagation: () => {} });
  if (!scrim.hidden) fail("клик мимо карточки не закрыл вопрос");
  // «Не записывать» уходит в накопитель без записи.
  deepBtn(groups, "xbtn").handlers.click({ stopPropagation: () => {} });
  posted.length = 0;
  deepBtn(scrim, "Не записывать").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length) fail("уход с формы записал черновик: " + JSON.stringify(posted));
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("«Не записывать» не увела в накопитель: " + sandbox.location.hash);
  }
}

// --- «Сохранить» пишет запись и возвращает в накопитель ---
{
  await go("#demo/new/draft");
  await fill("мысль с телефона");
  posted.length = 0;
  deepBtn(groups, "Сохранить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length !== 1 || !posted[0].endsWith("/drafts")) {
    fail("запись уехала не в свою ручку: " + JSON.stringify(posted));
  }
  if (!String(sandbox.location.hash).endsWith("demo/drafts")) {
    fail("«Сохранить» вернуло не в накопитель: " + sandbox.location.hash);
  }
  const said = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["Черновик XR-D7 записан", "Записать ещё", "В накопитель", "На доску"]) {
    if (said.includes(gone)) fail("промежуточный экран остался: нашлось «" + gone + "»");
  }
  if (!said.includes("XR-D7")) fail("записи нет в накопителе, куда увела кнопка: " + said.slice(0, 300));
}

// --- «Сохранить и грумить» пишет, поднимает разбор и уводит на экран записи ---
{
  await go("#demo/new/draft");
  await fill("вторая мысль с телефона");
  posted.length = 0;
  deepBtn(groups, "Сохранить и грумить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length !== 2) fail("пара «запись и разбор» ушла не двумя запросами: " +
    JSON.stringify(posted));
  if (!posted[0].endsWith("/drafts")) fail("первой ушла не запись: " + JSON.stringify(posted));
  if (!posted[1].endsWith("/drafts/XR-D7/groom")) {
    fail("разбор поднялся не той ручкой: " + JSON.stringify(posted));
  }
  if (!String(sandbox.location.hash).includes("demo/draft/XR-D7")) {
    fail("«Сохранить и грумить» увело не на экран записи: " + sandbox.location.hash);
  }
}

// --- форма задачи своей кнопкой не задета ---
{
  await go("#demo/new/task");
  if (!deepBtn(groups, "Завести задачу")) {
    fail("кнопка заведения задачи пропала: " + dump(groups).slice(0, 300));
  }
  if (deepBtn(groups, "Сохранить и грумить")) {
    fail("пара кнопок черновика встала на форму задачи");
  }
  // У задачи выход остался кнопкой в полосе действий: крестик и вопрос о
  // потере стоят только у черновика (макет DK-447).
  if (deepBtn(groups, "xbtn")) fail("крестик выхода встал на форму задачи");
  if (!deepBtn(groups, "Не заводить")) fail("у формы задачи пропала кнопка выхода");
}

console.log("poc_savegroom: ok");


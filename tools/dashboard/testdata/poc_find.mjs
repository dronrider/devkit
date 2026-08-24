// Стенд поля поиска в шапке (ветка poc-chat).
//
// Поле одно на все экраны, а отвечает на разные вопросы: на доске оно ведёт в
// выдачу по задачам, в разделе «Агенты» фильтрует сами сессии раздела. Прежде
// раздел уводил в поиск по доске, то есть отвечал не на тот вопрос (замечание
// пользователя). Второй предмет стенда: набор следующей буквы обязан
// перерисовывать выдачу, хотя экран при этом тот же самый.
//
// Зовётся: node testdata/poc_find.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const works = [
  { id: "XR-1", kind: "task", via: "tmux", title: "конвейер задачи", session: "aaaa1111",
    own: true, model: "opus", sect: "in-progress" },
  { id: "XR-2", kind: "goal", via: "registry", title: "цикл цели", sect: "in-progress" },
];
const board = { sections: [{ key: "backlog", rows: [{ id: "XR-7", title: "строка доски", sect: "backlog" }] }] };
const asked = [];

const { sandbox } = makeSandbox(app, (path) => {
  asked.push(path);
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path.endsWith("/board")) return { board, works };
  if (path.includes("/search")) {
    const q = decodeURIComponent(path.split("q=")[1] || "");
    return { groups: [{ key: "board", title: "Доска", rows: [
      { id: "XR-7", title: "нашлось по запросу " + q, sect: "backlog" }] }] };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const groups = sandbox.document.getElementById("groups");
// Запрос в выдаче подсвечен своим узлом, поэтому пробелы в сравнении
// схлопываются: предмет стенда сама выдача, а не её разметка.
const said = () => dump(groups).replace(/\s+/g, " ");
const field = sandbox.document.getElementById("hq");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
// Набор буквы в поле: обработчик тот же, что у живого поля, а срок ожидания
// стенду не нужен, он зовёт отправку сразу.
// Набор буквы. В браузере смену адреса подхватывает обработчик хэша; в стенде
// его будит сам стенд, но решение, перерисовывать экран или одну панель,
// остаётся за статикой (chatOnlyMove). Возвращается принятое решение: набор
// буквы движением панели считаться не должен, иначе выдача останется прежней.
const type = async (text) => {
  field.value = text;
  sandbox.findGo(text);
  await settle();
  const panelOnly = sandbox.chatOnlyMove(sandbox.route());
  if (!panelOnly) {
    await sandbox.refresh();
    await settle();
  }
  return panelOnly;
};

// --- таб сессий: поле фильтрует сессии и не уводит в выдачу по доске ---
await go("#demo/sess");
{
  asked.length = 0;
  await type("цикл");
  if (sandbox.location.hash.includes("/find/")) {
    fail("поиск таба увёл в выдачу по доске: " + sandbox.location.hash);
  }
  if (!decodeURIComponent(sandbox.location.hash).includes("demo/sess/цикл")) {
    fail("запрос таба ушёл не в его адрес: " + sandbox.location.hash);
  }
  if (asked.some((p) => p.includes("/search"))) {
    fail("таб сходил в поиск по задачам: " + JSON.stringify(asked));
  }
  const rows = allByClass(groups, "arow");
  if (rows.length !== 1 || !dump(rows[0]).includes("цикл цели")) {
    fail("таб не отфильтровался: " + dump(groups).slice(0, 200));
  }
  // Запрос живёт в адресе: перерисовка экрана его не теряет.
  await sandbox.refresh();
  await settle();
  if (!dump(groups).includes("цикл цели")) fail("запрос таба не пережил перерисовку");
  if (field.value !== "цикл") fail("поле шапки потеряло набранное: " + field.value);
}

// --- доска: поле по-прежнему ведёт в выдачу, и каждая буква её обновляет ---
await go("#demo");
{
  await type("пер");
  if (!sandbox.location.hash.includes("/find/")) {
    fail("с доски поиск не открыл выдачу: " + sandbox.location.hash);
  }
  if (!said().includes("нашлось по запросу пер")) {
    fail("выдача не собралась: " + said().slice(0, 300));
  }
  // Вторая буква: экран тот же, а выдача обязана перерисоваться. Перерисовка
  // панели вместо экрана оставляла бы на нём выдачу по прежнему запросу.
  if (await type("перв")) {
    fail("набор буквы принят за движение панели: выдача осталась бы прежней");
  }
  if (!said().includes("нашлось по запросу перв")) {
    fail("следующая буква выдачу не обновила: " + said().slice(0, 300));
  }
}

// --- очистка поля возвращает доску без перезагрузки ---
{
  await go("#demo");
  await type("перв");
  if (!said().includes("нашлось по запросу")) fail("выдача не открылась: " + said().slice(0, 200));

  // Стёрли до одного знака: сервер такой запрос не ищет, и вместо пустой
  // выдачи с надписью про два символа обязана вернуться доска.
  await type("п");
  if (sandbox.location.hash.includes("/find/")) {
    fail("на одном знаке экран выдачи остался: " + sandbox.location.hash);
  }
  if (!said().includes("строка доски")) fail("доска не вернулась: " + said().slice(0, 200));
  if (said().includes("нашлось по запросу")) fail("на экране остались строки прежней выдачи");

  // Стёрли совсем: то же самое, доска и никаких промежуточных пустот.
  await type("перв");
  await type("");
  if (sandbox.location.hash.includes("/find/")) {
    fail("пустое поле оставило экран выдачи: " + sandbox.location.hash);
  }
  if (!said().includes("строка доски")) fail("после очистки нет доски: " + said().slice(0, 200));
  if (said().includes("Ждём двух символов")) fail("мелькнула пустая выдача с надписью про символы");

  // Escape это тот же отказ: поле пустеет и экран возвращается.
  await type("перв");
  const field2 = sandbox.document.getElementById("hq");
  field2.value = "перв";
  field2.handlers.keydown({ key: "Escape", stopPropagation: () => {} });
  await settle();
  if (!sandbox.chatOnlyMove(sandbox.route())) {
    await sandbox.refresh();
    await settle();
  }
  if (field2.value !== "") fail("Escape не очистил поле: " + field2.value);
  if (sandbox.location.hash.includes("/find/")) {
    fail("Escape оставил экран выдачи: " + sandbox.location.hash);
  }
}

console.log("poc_find: ok");

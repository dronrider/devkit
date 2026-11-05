import { createRequire } from "node:module";
import assert from "node:assert/strict";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
// Накопитель одной таблицей (DK-1564, утверждённый макет): секция согласования
// держит подготовленные задачи строками с номерами доски, отметка выбирает
// задачи под подтверждение, решение уезжает одной кнопкой полосы запуска.
// Экран задачи называет её номером, исходная запись стоит чипом в строке.
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  const page = await context.newPage();
  page.on("pageerror", (e) => console.error("pageerror:", e.message));
  page.on("console", (m) => { if (m.type() === "error") console.error("console:", m.text()); });
  page.on("response", (r) => {
    if (r.status() >= 400 || r.url().includes("/drafts/XR-005")) console.error("HTTP", r.status(), r.url().replace(base, ""));
  });
  await page.goto(base + "/#demo/drafts");
  await page.locator("tr.secband .shead").filter({ hasText: "Согласовать заведение" }).waitFor();
  await page.locator("tr.secband .shead").filter({ hasText: "Разбор и оформление" }).waitFor();
  const sectionRows = page.locator("tbody.tsec tr.dsrow");
  await sectionRows.first().waitFor();
  const undecided = await sectionRows.count();
  assert.equal(undecided, 6);
  for (const num of await sectionRows.locator("td.id").allInnerTexts()) {
    assert.match(num.trim(), /^XR-[0-9]+$/, "строка согласования названа не номером доски: " + num);
  }
  // Свойства словами и итогом: тип «Баг», ранг суммой слагаемых, состояние
  // чипом ожидания, у удаления тип сам исход (DK-1564, замер QA 2026-11-06).
  const richRow = sectionRows.filter({ hasText: "из XR-010" }).first();
  assert.equal((await richRow.locator("td.dtype").innerText()).trim(), "Баг");
  assert.equal((await richRow.locator("td.drank").innerText()).trim(), "42");
  assert.equal((await richRow.locator("td.dst").innerText()).trim(), "На согласовании");
  const dropRow = sectionRows.filter({ hasText: "из XR-005" }).first();
  assert.equal((await dropRow.locator("td.dtype").innerText()).trim(), "Удаление");
  // Первое чтение закрепило номера за унаследованными задачами, ссылка строки
  // остаётся по прежнему ключу.
  const p1row = sectionRows.filter({ hasText: "из XR-005" }).first();
  await p1row.locator("td.id").click();
  await page.waitForURL(/#demo\/draft\/XR-005\/prepared\/p1/);
  await page.getByText("из XR-005:", { exact: false }).first().waitFor();
  // Заголовок без своего названия занят смысловым fallback из текста: пустое
  // поле в шапке читалось пропажей названия (замер QA 2026-11-06).
  const p1title = await page.getByLabel("заголовок задачи p1").inputValue();
  assert.equal(p1title.trim(), "первая карточка", "заголовок drop-карточки не занят fallback: " + JSON.stringify(p1title));
  const one = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Подтвердить", exact: true }).click();
  assert.equal((await one).status(), 200);
  await page.waitForURL(/#demo\/draft\/XR-005(\/chat\/|$)/);
  // Решённая задача сворачивается из состава, живая остаётся на виду.
  try {
    await page.getByText("Ещё 1 решённая задача", { exact: true }).waitFor({ timeout: 5000 });
  } catch {
    console.error("HASH:", await page.evaluate(() => location.hash));
    console.error("BODY:", (await page.locator("#groups").innerText()).replace(/\s+/g, " ").slice(0, 1500));
    throw new Error("нет свёрнутых решённых на экране записи");
  }
  // Пачка подтверждения: отметки в строках секции и одна кнопка на выбранное.
  await page.goto(base + "/#demo/drafts");
  const rest = page.locator("tbody.tsec tr.dsrow");
  await rest.first().waitFor();
  assert.equal(await rest.count(), 5);
  await rest.filter({ hasText: "из XR-005" }).first().locator("button.dpick").click();
  await rest.filter({ hasText: "из XR-007" }).first().locator("button.dpick").click();
  const batch = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Подтвердить выбранные (2)", exact: true }).click();
  const batched = await (await batch).json();
  assert.equal(batched.results.length, 2);
  // Приписка подтверждается на своём экране, квитанция ведёт на строку доски.
  await page.goto(base + "/#demo/draft/XR-006");
  await page.getByText("Задачи разбора", { exact: true }).waitFor();
  await page.locator("div.srow").filter({ hasText: "третья карточка" }).first().click();
  await page.waitForURL(/#demo\/draft\/XR-006\/prepared\/p3/);
  // Чтение собирает описание разметкой: поля ввода в чтении нет (DK-1438).
  // Экран рисуется после смены адреса, поэтому описание ждётся само, а не
  // читается сразу со старого экрана.
  await page.locator(".fview").filter({ hasText: "третья карточка" }).first().waitFor();
  const attach = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Подтвердить", exact: true }).click();
  assert.equal((await attach).status(), 200);
  // Подтверждение само возвращает на экран записи: переход к задаче ждёт его,
  // иначе экран перехватит адрес назад (та же гонка, что у первой карточки).
  await page.waitForURL(/#demo\/draft\/XR-006(\/chat\/|$)/);
  await page.goto(base + "/#demo/XR-001");
  try {
    await page.locator(".ffold .foldh").filter({ hasText: "Из черновика" }).click({ timeout: 5000 });
  } catch {
    console.error("HASH:", await page.evaluate(() => location.hash));
    console.error("BODY:", (await page.locator("#groups").innerText()).replace(/\s+/g, " ").slice(0, 1500));
    throw new Error("вклейки приписки нет на экране задачи");
  }
  // Вклейка приписки стоит в развёрнутом разделе: текст ищется внутри него,
  // а не по всему дереву, где остались скрытые экраны прошлых адресов.
  await page.locator(".ffold").filter({ hasText: "третья карточка" }).first().waitFor();
  await page.getByRole("link", { name: "черновик XR-006", exact: true }).click();
  await page.waitForURL(/#demo\/draft\/XR-006/);
  // Решённая приписка стоит за разворотом состава, и статус читается там.
  await page.getByText("Ещё 1 решённая задача", { exact: true }).click();
  await page.getByText("Подтверждена", { exact: true }).first().waitFor();
  const tail = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.goto(base + "/#demo/drafts");
  await page.locator("tbody.tsec tr.dsrow").filter({ hasText: "из XR-006" }).first().locator("button.dpick").click();
  await page.getByRole("button", { name: "Подтвердить выбранные (1)", exact: true }).click();
  assert.equal((await tail).status(), 200);
  // Богатая карточка читается словами: тип, приёмка и состояние подписями,
  // ранг итогом суммы, слагаемые и код приёмки в подсказках (замер QA
  // 2026-11-06).
  await page.goto(base + "/#demo/draft/XR-010/prepared/p5");
  // Экран рисуется после смены адреса: сначала ждётся его заголовок.
  await page.getByLabel("заголовок задачи p5").waitFor();
  const richChips = (await page.locator(".tchips .chip").allInnerTexts()).join(" | ");
  for (const part of ["На согласовании", "тип Баг", "ранг 42", "цена L", "Проверка с вашим участием"]) {
    assert.ok(richChips.includes(part), "в чтении нет чипа " + part + ": " + richChips);
  }
  assert.equal(await page.getByLabel("заголовок задачи p5").inputValue(), "баг доставки ленты");
  const rich = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Подтвердить", exact: true }).click();
  assert.equal((await rich).status(), 200);
  // Подтверждение возвращает на экран записи тем же ходом, и следующий адрес
  // ждёт его, а не перехватывается назад.
  await page.waitForURL(/#demo\/draft\/XR-010(\/chat\/|$)/);
  await page.goto(base + "/#demo/draft/XR-008");
  await page.getByText("Разбор ничего не подготовил. Можно подготовить задачу вручную.", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Подготовить вручную", exact: true }).click();
  await page.waitForURL(/#demo\/draft\/XR-008\/prepared\/new/);
  await page.getByLabel("Исход").selectOption("drop");
  // Описание ручной задачи это поле файла постановки с адресной меткой, а
  // причина стоит в своём блоке подписью-legend: оба поля берутся прицельно,
  // а не первым попавшимся textarea со страницы.
  await page.getByLabel("текст файла задачи XR-008").fill("ручной исход");
  await page.locator("fieldset.dfield").filter({ hasText: "Причина" }).locator("textarea").fill("неактуально");
  const saved = page.waitForResponse((r) => r.url().endsWith("/XR-008/prepared") && r.request().method() === "PUT");
  // У формы заведения своя подпись сохранения, «Сохранить» это правка уже
  // сохранённой задачи.
  await page.getByRole("button", { name: "Сохранить задачу", exact: true }).click();
  const savedResp = await saved;
  assert.equal(savedResp.status(), 200);
  // Сохранение выдаёт новой задаче постоянный номер доски, временный ключ
  // живёт только в этом ответе: экран обязан переехать на номер, иначе
  // подтверждению не на что встать (DK-1564, решение 6).
  const savedBody = await savedResp.json();
  const manualID = Object.values(savedBody.assigned || {})[0];
  assert.match(manualID || "", /^XR-[0-9]+$/, "новой задаче не выдан номер доски: " + JSON.stringify(savedBody.assigned || null));
  await page.waitForURL(new RegExp("#demo/draft/XR-008/prepared/" + manualID + "(/chat/)?$"));
  await page.locator("button.btn-acc", { hasText: "Подтвердить" }).waitFor();
  const manual = page.waitForResponse((r) => r.url().endsWith("/drafts/prepared/confirm") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Подтвердить", exact: true }).click();
  assert.equal((await manual).status(), 200);
  // Подтверждение возвращает на экран записи, решённая задача стоит за
  // разворотом состава.
  await page.waitForURL(/#demo\/draft\/XR-008(\/chat\/|$)/);
  await page.getByText("Ещё 1 решённая задача", { exact: true }).click();
  await page.getByText("Подтверждено", { exact: true }).first().waitFor();
  await page.goto(base + "/#demo/draft/XR-009");
  await page.getByText("Не удалось разобрать запись. Можно повторить разбор.", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Подготовить вручную", exact: true }).waitFor();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  const archived = await context.request.post(base + "/__archive");
  assert.equal(archived.status(), 200, await archived.text());
  await page.goto(base + "/#demo/XR-001");
  await page.locator(".ffold .foldh").filter({ hasText: "Из черновика" }).click();
  await page.getByRole("link", { name: "черновик XR-006", exact: true }).click();
  await page.waitForURL(/#demo\/draft\/XR-006/);
  await page.getByText("Ещё 2 решённые задачи", { exact: true }).click();
  await page.getByText("Подтверждена", { exact: true }).first().waitFor();
  console.log(JSON.stringify({ section: 5, one: true, batch: batched.results.length, attach: true, manual: true, mobile: true }));
} finally { await context.close(); await browser.close(); }

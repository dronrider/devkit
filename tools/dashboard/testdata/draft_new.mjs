// Статический тест DK-350: проверяет, что app.js содержит правильные конструкции.
import fs from "node:fs";
const appPath = process.argv[2];
const code = fs.readFileSync(appPath, "utf-8");
const lines = code.split("\n");

// Проверки на уровне текста
let ok = 0, fail = 0;
const check = (label, cond) => { if(cond) ok++; else { fail++; console.error("FAIL: "+label); } };

// 1. В renderDrafts есть кнопка «Новый черновик»
// Ищем: goKeepingChat(project + "/new/draft") внутри renderDrafts (после drafts-foot)
let foundDraftBtn = false;
for (let i = 0; i < lines.length; i++) {
  if (lines[i].includes("Новый черновик") && lines[i].includes("btn")) {
    foundDraftBtn = true;
    break;
  }
}
check("кнопка Новый черновик в app.js", foundDraftBtn);

// 2. В makeMenuAt нет resetNewForm
// Ищем: makeMenuAt и проверяем, что внутри нет resetNewForm
// Сначала найдём makeMenuAt
let menuStart = -1, menuEnd = -1;
let braceCount = 0;
for (let i = 0; i < lines.length; i++) {
  if (lines[i].includes("function makeMenuAt")) { menuStart = i; break; }
}
if (menuStart >= 0) {
  // Считаем скобки до конца функции
  // Ищем открывающую { после заголовка
  for (let i = menuStart; i < lines.length; i++) {
    for (const ch of lines[i]) {
      if (ch === "{") braceCount++;
      if (ch === "}") braceCount--;
    }
    if (braceCount === 0 && i > menuStart) { menuEnd = i; break; }
  }
  if (menuEnd > menuStart) {
    let hasReset = false;
    for (let i = menuStart; i <= menuEnd; i++) {
      if (lines[i].includes("resetNewForm")) { hasReset = true; break; }
    }
    check("в makeMenuAt нет resetNewForm", !hasReset);
  } else {
    check("makeMenuAt body найден", false);
  }
} else {
  check("makeMenuAt найден", false);
}

// 3. В комментарии к renderNew есть указание, что голый /new это задача
// (строка 17679: «голый #проект/new это задача»)
let foundNewIsTask = lines.some(l => l.includes("голый") && l.includes("/new") && l.includes("задача"));
check("комментарий про голый /new", foundNewIsTask);

// 4. resetNewForm определена и сбрасывает draft
let foundReset = lines.some(l => l.includes("newForm.draft = false"));
check("resetNewForm сбрасывает draft", foundReset);

// 5. renderNew принимает kind и ставит draft
let foundKind = lines.some(l => l.includes("newForm.draft = kind === \"draft\""));
check("renderNew ставит draft из kind", foundKind);

if (fail) {
  console.error("FAIL: "+fail+" из "+(ok+fail)+" тестов");
  process.exit(1);
} else {
  console.log("PASS: "+ok+" тестов пройдено");
}

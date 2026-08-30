// Замер нити ленты настоящим движком (POC DK-397, ветка poc-chat).
//
// Живой случай: «я вижу только синюю нитку и коричневую, коричневая на моих и
// твоих сообщениях, при этом она начинается не от прошлой точки, а с разрывом,
// и также заканчивается» (замечание пользователя). Нить рисовалась по коробке
// строки, а между работами лежит поле в восемнадцать точек, ничьё: на нём
// линия и проваливалась. Прежняя правка закрыла только отрезок субагента,
// общая нить осталась рваной.
//
// Предмет замера числами: щель между отрезками соседних записей, расстояние от
// края цвета до середины кружка и то, что выше первого кружка и ниже
// последнего нити нет. Разбором правил такое не берётся: куски складываются
// из переменных, полей и видов записи на живой раскладке.

const ROWS = [
  // вид записи, метки ленты, содержимое
  ["f-bub", "gtop r-user", '<div class="msg me"><div class="bb">реплика человека</div></div>'],
  ["f-head", "r-tool", '<div class="trow2">ход инструмента</div>'],
  ["f-head", "r-tool to-deleg", '<div class="trow2">Agent: заказ субагенту</div>'],
  ["f-fold", "r-assistant sub to-deleg ti-deleg", '<div class="fold">журнал субагента</div>'],
  ["f-head", "r-tool sub to-deleg ti-deleg", '<div class="trow2">ход субагента</div>'],
  ["f-fold", "r-note sub subend ti-deleg", '<div class="fold">фоновый агент завершил работу</div>'],
  ["f-bub", "gend r-assistant", '<div class="msg"><div class="bb">ответ агента</div></div>'],
  ["f-bub", "gtop r-user", '<div class="msg me"><div class="bb">вторая реплика человека</div></div>'],
  ["f-bub", "gend r-assistant", '<div class="msg"><div class="bb">второй ответ</div></div>'],
];

const box = document.getElementById("groups");
box.innerHTML = '<div class="msgs chatfeed">' + ROWS.map(([kind, marks, body], i) => {
  const head = i === 0 ? " thead" : "";
  const tail = i === ROWS.length - 1 ? " ttail" : "";
  return '<div class="frow ' + kind + " " + marks + head + tail + '">' +
    '<span class="fdot"></span><div class="frowb">' + body + "</div></div>";
}).join("") + "</div>";

// Кусок нити: от какого y до какого он идёт и каким цветом. Псевдоэлемент
// прямоугольника не отдаёт, поэтому числа берутся из посчитанных стилей.
function piece(row, which) {
  const cs = getComputedStyle(row, "::" + which);
  const rect = row.getBoundingClientRect();
  const top = parseFloat(cs.top);
  const h = parseFloat(cs.height);
  if (!isFinite(top) || !isFinite(h)) return null;
  return { y0: rect.top + top, y1: rect.top + top + h, color: cs.backgroundColor, h };
}

function dotMid(row) {
  const d = row.querySelector(".fdot").getBoundingClientRect();
  return d.top + d.height / 2;
}

const rows = Array.from(document.querySelectorAll(".frow"));
const clear = (c) => c === "rgba(0, 0, 0, 0)" || c === "transparent";

let gap = 0;      // щель между кусками нити, точки
let offDot = 0;   // насколько край цвета разошёлся с серединой кружка
let overHead = 0; // нить выше первого кружка
let overTail = 0; // нить ниже последнего
let colors = new Set();

for (let i = 0; i < rows.length; i++) {
  const row = rows[i];
  const up = piece(row, "before");
  const down = piece(row, "after");
  const mid = dotMid(row);
  // Куски одной записи сходятся на её кружке.
  if (up && !clear(up.color)) {
    offDot = Math.max(offDot, Math.abs(up.y1 - mid));
    colors.add(up.color);
  }
  if (down && !clear(down.color)) {
    offDot = Math.max(offDot, Math.abs(down.y0 - mid));
    colors.add(down.color);
  }
  if (i === 0 && up && !clear(up.color)) overHead = Math.max(overHead, up.h);
  if (i === rows.length - 1 && down && !clear(down.color)) overTail = Math.max(overTail, down.h);
  // Стык с соседом снизу: конец нижнего куска этой записи против начала
  // верхнего куска следующей.
  const next = rows[i + 1];
  if (!next) continue;
  const lower = piece(next, "before");
  if (!down || !lower) continue;
  if (clear(down.color) || clear(lower.color)) continue;
  gap = Math.max(gap, lower.y0 - down.y1);
}

// Синий отрезок работы субагента: от кружка вызова до кружка вести о конце.
const deleg = rows.filter((r) => r.className.includes("to-deleg") || r.className.includes("ti-deleg"));
const blue = getComputedStyle(deleg[0], "::after").backgroundColor;
const grey = getComputedStyle(rows[1], "::after").backgroundColor;

document.title = [
  "screen=" + Math.round(document.documentElement.clientWidth),
  "rows=" + rows.length,
  "gap=" + Math.round(gap * 10),
  "off-dot=" + Math.round(offDot * 10),
  "over-head=" + Math.round(overHead),
  "over-tail=" + Math.round(overTail),
  "colors=" + colors.size,
  "deleg-differs=" + (blue !== grey ? "1" : "0"),
].join(" ");

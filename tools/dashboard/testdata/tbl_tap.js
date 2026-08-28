// Замер отклика на нажатие настоящим движком (POC DK-397, ветка poc-chat).
//
// «При нажатии кнопок на строке задач происходит выделение блоков внутри серым
// контуром, которое сохраняется, и весь блок моргает однократно светлым
// голубым фоном. На нажатие должна реагировать только конкретная кнопка, а не
// весь блок» (замечание пользователя).
//
// Красит строку не дашборд, а браузер, и красит он её тремя своими приёмами:
// подсветкой нажатия (-webkit-tap-highlight-color, она достаётся ближайшему
// нажимаемому предку, то есть всей строке), кольцом фокуса, которое остаётся
// на кнопке после нажатия мышью или пальцем, и наведением, которое на телефоне
// разбирается из того же нажатия. Ни один из трёх правилами не виден: их
// значения по умолчанию лежат в самом движке, поэтому меряет тут движок.
//
// Меряется тут одна подсветка нажатия: кольцо фокуса и отклик самой кнопки
// живут состояниями, а нажать в --dump-dom нечем, и сторожит их разбор правил
// в самом go-тесте.
//
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const ICO = (cls) => `<button class="btn btn-sm btn-ico ${cls}" type="button">` +
  `<svg viewBox="0 0 24 24"></svg></button>`;

// Строки трёх разделов: подсветка нажатия достаётся каждой из них, и молчать
// обязана каждая.
document.getElementById("groups").innerHTML = `
  <table class="tbl t-tasks"><tbody class="tsec">
    <tr class="trow"><td class="id"><span>DK-517</span></td>
      <td class="meta"><span class="cin"><span class="racts">${
  ICO("btn-acc rmain")}${ICO("rchat")}${ICO("rdots")}</span></span></td></tr>
  </tbody></table>
  <table class="tbl t-sess"><tbody class="tsec">
    <tr class="arow atalk"><td class="ab">работа</td>
      <td class="aacts"><span class="cin"><span class="racts">${ICO("rchat")}</span></span></td></tr>
  </tbody></table>
  <table class="tbl t-drafts"><tbody class="tsec">
    <tr class="dsrow clicky"><td class="id">DK-518</td>
      <td class="sm"><span class="cin"><span class="racts">${ICO("rchat")}</span></span></td></tr>
  </tbody></table>`;
document.getElementById("pname").textContent = "devkit";

// Прозрачность подсветки нажатия: ноль значит, что строку она не красит.
const tap = (sel) => {
  const cs = getComputedStyle(document.querySelector(sel));
  const raw = cs.webkitTapHighlightColor || cs.getPropertyValue("-webkit-tap-highlight-color") || "";
  const m = /rgba?\(([^)]*)\)/.exec(raw);
  if (!m) return -1;
  const parts = m[1].split(",").map((s) => parseFloat(s));
  const a = parts.length > 3 ? parts[3] : 1;
  return Math.round(a * 100);
};

document.title = [
  "screen=" + document.documentElement.clientWidth,
  "taptask=" + tap(".trow"),
  "tapsess=" + tap(".arow"),
  "tapdraft=" + tap(".dsrow"),
  // Кнопке подсветка тоже не нужна: свой отклик у неё есть, а браузерная
  // рисуется поверх и мимо её же скруглений.
  "tapbtn=" + tap(".racts .btn"),
].join(" ");

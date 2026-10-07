// Стенд хвата ширины колонки меню: колонка тянется хватом на правом крае,
// ширина ложится в переменную корня, память переживает перезагрузку, а
// пределы держат снизу читаемость колонки, сверху полосу доски.
//
// Живой случай: колонка стояла 208 точек намертво, в неё не влезали ни
// длинные имена проектов, ни строки квоты, и раздвинуть её было нечем.
//
// Зовётся: node testdata/poc_sidewidth.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Разметка несёт узел хвата: без него колонка тянется только кодом, а человек
// хвата на экране не находит.
const html = fs.readFileSync(path.join(path.dirname(app), "index.html"), "utf8");
if (!html.includes('id="sgrab"')) {
  fail("в index.html нет хвата колонки id=\"sgrab\"");
}

// Хват живёт вне прокрутки: содержимое колонки прокручивается внутренним
// узлом .sbody, а сам хват стоит прямым ребёнком .side. При переполнении
// колонки хват остаётся на месте и полоску прокрутки не накрывает.
if (!html.includes('class="sbody"')) {
  fail("в index.html нет внутреннего узла прокрутки .sbody колонки");
}
if (html.indexOf('id="sgrab"') < html.indexOf('class="sbody"')) {
  fail("хват sgrab стоит до узла прокрутки sbody: он ложится после содержимого");
}
if (!/<i class="sgrab"[^>]*><\/i>\s*<\/aside>/.test(html)) {
  fail("хват sgrab не прямой ребёнок .side");
}
const css = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");
if (/\.side\s*\{[^}]*overflow/.test(css)) {
  fail(".side прокручивается сама: overflow уходит на внутренний узел .sbody");
}
if (!/\.sbody\s*\{[^}]*overflow-y:\s*auto/.test(css)) {
  fail("в правиле .sbody нет прокрутки overflow-y:auto");
}

const { sandbox, byId, store } = makeSandbox(app, () => ({}));
await settle();

const sw = () => sandbox.document.documentElement.style.props["--sw"];

// Старт ставит ширину по умолчанию: память пуста, колонка стоит своей 208.
if (sw() !== "208px") {
  fail("колонка не встала шириной по умолчанию: " + sw());
}

// Прямая правка ширины живёт в пределах.
sandbox.putSideWidth(360);
if (sw() !== "360px") fail("тяга не поменяла ширину колонки: " + sw());
sandbox.putSideWidth(4);
if (sw() !== "176px") fail("минимум колонки не держится: " + sw());
sandbox.putSideWidth(4000);
if (sw() !== "480px") fail("потолок колонки не держится: " + sw());

// Память читается обратно тем же клапаном.
store.set("devkit.side.width", "340");
if (sandbox.sideWidth() !== 340) {
  fail("ширина из памяти не читается: " + sandbox.sideWidth());
}

// Хват: нажатие, движение с зажатой кнопкой, отпускание сохраняет ширину.
const grab = byId.get("sgrab");
if (!grab) fail("хвата sgrab на странице нет");
grab.handlers.pointerdown({ button: 0, pointerId: 1, preventDefault: () => {} });
grab.handlers.pointermove({ clientX: 330, buttons: 1 });
if (sw() !== "330px") fail("тяга хватом не дошла до колонки: " + sw());

// Кнопка отпущена мимо хвата: движение с пустыми кнопками гасит тягу, и
// следующее проведение уже не двигает колонку.
grab.handlers.pointermove({ clientX: 300, buttons: 0 });
if (sw() !== "300px") fail("отпускание не легло последней шириной: " + sw());
grab.handlers.pointermove({ clientX: 250, buttons: 1 });
if (sw() !== "300px") fail("колонка тащится после отпускания: " + sw());

// Повторное нажатие тягу возобновляет: захват не умер вместе с прошлым жестом.
grab.handlers.pointerdown({ button: 0, pointerId: 1, preventDefault: () => {} });
grab.handlers.pointermove({ clientX: 260, buttons: 1 });
if (sw() !== "260px") fail("повторный захват не тянет: " + sw());
grab.handlers.pointerup({ clientX: 260 });
if (store.get("devkit.side.width") !== "260") {
  fail("отпущенная ширина не легла в память: " + store.get("devkit.side.width"));
}

// Правая кнопка хвата не берет: правый клик в колонке открывает меню браузера.
grab.handlers.pointerdown({ button: 2, pointerId: 1, preventDefault: () => {} });
grab.handlers.pointermove({ clientX: 100, buttons: 2 });
if (sw() !== "260px") fail("правая кнопка начала тягу: " + sw());

console.log("poc_sidewidth: ok");

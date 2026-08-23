// Стенд ширины панели разговора (замечание пользователя: «чат нельзя раздвинуть
// шире определённой величины, уберите это ограничение»). Верхнего предела в
// точках у панели нет: потолок меряется самим окном, панель тянется почти во
// весь экран, а доске остаётся узкая полоса. Нижний предел прежний: уже 320
// точек лента нечитаема. Память ширины держит запрошенное человеком, а
// обрезает его окно при чтении, иначе один заход с ноутбука усадил бы панель
// навсегда.
//
// Зовётся: node testdata/poc_width.mjs static/app.js

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const { sandbox } = makeSandbox(appPathArg(), () => ({}));
await settle();

const cw = () => sandbox.document.documentElement.style.props["--cw"];

sandbox.window.innerWidth = 1600;
// Широкая панель на широком мониторе: прежде тут был упор в 640 точек.
sandbox.saveChatWidth(1200);
if (sandbox.chatWidth() !== 1200) {
  fail("широкая панель обрезана потолком в точках: " + sandbox.chatWidth());
}
sandbox.putChatWidth(1200);
if (cw() !== "1200px") fail("ширина не доехала до переменной корня: " + cw());

// Память переживает перезагрузку страницы в новом диапазоне: значение лежит
// как записано, а не как обрезано прежним потолком.
if (sandbox.localStorage.getItem("devkit.chat.width") !== "1200") {
  fail("память ширины хранит обрезанное: " +
    sandbox.localStorage.getItem("devkit.chat.width"));
}

// Панель тянется почти во весь экран, полоса доски остаётся.
const wide = sandbox.chatClamp(99999);
if (wide >= 1600 || wide < 1500) {
  fail("самая широкая панель считается не по окну: " + wide);
}

// Нижний предел не тронут.
if (sandbox.chatClamp(10) !== 320) fail("нижний предел ширины уехал: " + sandbox.chatClamp(10));

// Узкое окно обрезает запомненную ширину при чтении, но памяти не портит.
sandbox.window.innerWidth = 900;
if (sandbox.chatWidth() !== 828) fail("на узком окне ширина не по окну: " + sandbox.chatWidth());
if (sandbox.localStorage.getItem("devkit.chat.width") !== "1200") {
  fail("заход с узкого окна усадил память ширины навсегда");
}

console.log("ok: панель тянется по окну, память ширины переживает перезагрузку, " +
  "нижний предел прежний");

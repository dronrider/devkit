// Стенд всплывающих карточек уведомлений (ветка poc-chat).
//
// Предмет стенда: какое из событий потока поднимает карточку поверх экрана,
// а какое остаётся строкой ленты. Замечание из проверки POC: законченный ход
// конвейера рождает до трёх событий (фоновый стоп субагента, повтор,
// задушенный дросселем, вторая строка хода про зов дашборда), и все три
// всплывали одинаковыми карточками, столбиком закрывая рабочий экран.
// Уведомитель выражает это сам полями события: level говорит, чему быть
// громким, sent дошёл ли баннер до человека. Карточка это второй показ
// доставленного громкого события, фоновые и недоставленные остаются в ленте,
// а точка колокольчика загорается и на них.
//
// Зовётся: node testdata/poc_flash.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const { sandbox, streams } = makeSandbox(appPathArg(), () => ({}));
await settle();

const bell = streams.find((s) => String(s.url).includes("/api/notifications"));
if (!bell) fail("поток уведомлений не поднят: всплывать нечему");
const fire = (n) => bell.onmessage({ data: JSON.stringify(n) });
// Коробки карточек и точки в песочнице создаются ленично, поэтому стенд берёт
// их тем же вызовом, что и статика: раньше первого события узла ещё нет.
const cards = () => sandbox.document.getElementById("flashes");
const dot = () => sandbox.document.getElementById("bell-dot");

// Время дальше мигания курсора потока при подключении и растёт от события к
// событию: курсор двигается любым свежим событием, и повторное время в нём
// тонет, не проверяя повторную всплывашку.
const at = (s) => "2099-01-01T00:00:" + String(s).padStart(2, "0");

// --- фоновый стоп субагента: строка ленты и точка колокольчика, без карточки ---
fire({ time: at(1), reason: "subagent_stop", kind: "stop", level: "фоновый",
  sent: true, title: "devkit: субагент отработал", body: "последние строки статуса" });
await settle();
if (cards().children.length) {
  fail("фоновое событие всплыло карточкой: " + dump(cards()));
}
if (dot().hidden !== false) {
  fail("фоновое событие не зажгло точку колокольчика: " + JSON.stringify(dot().hidden));
}

// --- повтор, задушенный дросселем: доставки не было, карточки нет ---
fire({ time: at(2), reason: "turn_done", kind: "stop", level: "громкий",
  sent: false, result: "пропуск: повтор в окне 30 c",
  title: "devkit: ход окончен", body: "нарезал три штуки" });
await settle();
if (cards().children.length) {
  fail("недоставленное событие всплыло карточкой: " + dump(cards()));
}

// --- вторая строка того же хода про зов дашборда: тоже недоставленная ---
fire({ time: at(3), reason: "turn_done", kind: "stop", level: "громкий",
  sent: false, result: "фокус не определился, зовём",
  title: "devkit: ход окончен", body: "фокус не определился, зовём" });
await settle();
if (cards().children.length) {
  fail("зов дашборда всплыл карточкой: " + dump(cards()));
}

// --- громкое доставленное: единственная карточка хода ---
fire({ time: at(4), reason: "turn_done", kind: "stop", level: "громкий",
  sent: true, result: "код возврата: 0",
  title: "devkit: ход окончен", body: "задача закрыта, ждёт ревью" });
await settle();
if (cards().children.length !== 1 || !dump(cards()).includes("devkit: ход окончен")) {
  fail("громкое доставленное событие не всплыло карточкой: " + dump(cards()));
}

console.log("карточкой всплывает только доставленное громкое, фоновые и недоставленные остаются в ленте");

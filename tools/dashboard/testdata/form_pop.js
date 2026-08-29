// Замер командной панели формы настоящим движком (POC DK-397, ветка poc-chat).
//
// Предмет два, и оба берутся только раскладкой. Первый это место раскрытого
// списка подписок: на телефоне кнопка панели переносится на свою строку и
// встаёт у левого края, и список, висящий на её правом крае, уходил за границу
// экрана. Выбор стороны тут не помогает: список шире свободного места и слева
// от кнопки, и справа, поэтому горизонталь ему считает popFit числом, а стенд
// смотрит получившиеся зазоры.
//
// Второй это ширина самих кнопок: подписи резались ради телефона («кнопки
// слишком длинные», замечание пользователя), и без замера сокращение подписи
// ничего не говорит о ширине, которую складывают ещё поля, значок и кегль.
//
// Экран собирает сам app.js, ответы сервера подкладывает live_mock.js. Числа
// уезжают заголовком страницы: chrome --dump-dom отдаёт разметку после
// исполнения скриптов, и заголовок это самый короткий способ вынести их наружу.
const standWhich = new URLSearchParams(location.search).get("bar") || "draft";

async function standRun() {
  await loadHarnesses();
  if (standWhich === "draft") await renderDraft("demo", [], "d-1");
  else await renderTask("demo", [], "XR-1");
  // Форма дорисовывается таймерами (высота заголовка, ленивые блоки): замер до
  // них поймал бы недособранный экран.
  await new Promise((done) => { setTimeout(done, 80); });

  const split = document.querySelector("#groups .split");
  if (!split) throw new Error("на форме нет составной кнопки запуска");
  const wide = split.querySelector(".btn:not(.more2)");
  const more = split.querySelector(".more2");
  const pop = split.querySelector(".hpop");
  if (!wide || !more || !pop) throw new Error("составная кнопка собрана не теми половинами");
  more.click();
  await new Promise((done) => { setTimeout(done, 30); });
  if (pop.hidden) throw new Error("список подписок не раскрылся");

  const screen = document.documentElement.clientWidth;
  const main = document.getElementById("groups").getBoundingClientRect();
  const w = wide.getBoundingClientRect();
  const m = more.getBoundingClientRect();
  const p = pop.getBoundingClientRect();
  const chip = document.querySelector(".tchips .chip");
  const chipBox = chip ? chip.getBoundingClientRect() : null;
  // Соседи по панели это кнопки-значки той же строки: карандаш правки, чтение,
  // вход в разговор. Стрелка выбора подписки стоит с ними в одном ряду и обязана
  // быть той же ширины, а числа своего у неё нет.
  const kin = document.querySelector(".tmodes .btn-ico, .tmodes .tpen");
  if (!kin) throw new Error("в панели формы нет соседних кнопок-значков");
  const kinBox = kin.getBoundingClientRect();
  // Поля основной кнопки меряются по её содержимому: слева до значка, справа
  // от подписи до стыка с узкой половиной.
  const glyph = wide.querySelector("svg").getBoundingClientRect();
  const lb = wide.querySelector(".lb").getBoundingClientRect();

  document.title = [
    "screen=" + Math.round(screen),
    // Подпись едет длиной, а не словами: буквы в заголовок страницы не
    // положить, а мерить надо всё равно ширину.
    "label-len=" + wide.textContent.trim().length,
    "wide-w=" + Math.round(w.width),
    // Вся составная кнопка целиком: широкая половина со стрелкой выбора.
    "btn-w=" + Math.round(m.right - w.left),
    // Кнопка перенеслась на свою строку: она стоит ниже чипов статуса.
    "wrapped=" + (chipBox && w.top > chipBox.bottom ? 1 : 0),
    "pop-w=" + Math.round(p.width),
    // Зазоры до границ: слева режет главная часть экрана, справа окно.
    "gap-left=" + Math.round(p.left - main.left),
    "gap-right=" + Math.round(screen - p.right),
    // Список висит правым краем на кнопке: там, где места вдоволь, ему двигаться
    // незачем, и замер сторожит, что счёт не растащил его по экрану.
    "pop-hang=" + Math.round(m.right - p.right),
    "arrow-w=" + Math.round(m.width),
    "kin-w=" + Math.round(kinBox.width),
    "pad-left=" + Math.round(glyph.left - w.left),
    "pad-right=" + Math.round(w.right - lb.right),
  ].join(" ");
}

window.addEventListener("load", () => {
  standRun().catch((err) => { document.title = "err=" + String(err.message || err); });
});

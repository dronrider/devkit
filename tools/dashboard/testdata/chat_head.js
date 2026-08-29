// Замер кнопок шапки разговора настоящим движком (POC DK-397, ветка poc-chat).
//
// Крестик закрытия панели был бледным значком без рамки и фона, вдвое меньше
// соседних кнопок шапки, и пальцем в него не попадали («кнопка закрытия чата
// сделана малозаметным крестиком», замечание пользователя). Размер и вид тут
// складываются из правила кнопки с правилами её места, поэтому меряет их
// браузер, а не разбор стилей.
//
// Шапку собирает сам app.js, ответы сервера подкладывает live_mock.js.
async function standRun() {
  const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
    { id: "XR-1", title: "своя задача", sect: "in-progress" },
  ] }] };
  const st = await chatState("demo", "board", board);
  // Шапка живёт в панели разговора: снаружи её правила не действуют, и мерить
  // её надо там, где она стоит.
  const panel = document.getElementById("cpanel");
  panel.hidden = false;
  const pin = document.getElementById("cpin");
  pin.replaceChildren(chatHead("demo", st));
  await new Promise((done) => { setTimeout(done, 60); });

  const shut = pin.querySelector(".nx");
  const kin = pin.querySelector(".cdbtn");
  if (!shut) throw new Error("в шапке разговора нет кнопки закрытия");
  if (!kin) throw new Error("в шапке разговора нет соседних кнопок для сравнения");
  const s = shut.getBoundingClientRect();
  const k = kin.getBoundingClientRect();
  const look = getComputedStyle(shut);
  const kinLook = getComputedStyle(kin);
  const glyph = shut.querySelector("svg").getBoundingClientRect();
  const kinGlyph = kin.querySelector("svg").getBoundingClientRect();

  document.title = [
    "shut-w=" + Math.round(s.width),
    "shut-h=" + Math.round(s.height),
    "kin-w=" + Math.round(k.width),
    "kin-h=" + Math.round(k.height),
    "glyph=" + Math.round(glyph.width),
    "kin-glyph=" + Math.round(kinGlyph.width),
    // Рамка и фон это то, чем кнопка отличается от голого значка: бледный
    // крестик человек за кнопку не считал.
    "border=" + Math.round(parseFloat(look.borderTopWidth) || 0),
    "kin-border=" + Math.round(parseFloat(kinLook.borderTopWidth) || 0),
    "filled=" + (look.backgroundColor === kinLook.backgroundColor ? 1 : 0),
    "same-ink=" + (look.color === kinLook.color ? 1 : 0),
  ].join(" ");
}

window.addEventListener("load", () => {
  standRun().catch((err) => { document.title = "err=" + String(err.message || err); });
});

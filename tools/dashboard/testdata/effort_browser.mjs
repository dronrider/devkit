import { createRequire } from "node:module";
import assert from "node:assert/strict";
import fs from "node:fs";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token, boardNow, boardNext, flipMark] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
await context.addInitScript(() => {
  for (const [key, ms] of Object.entries({ ask: 250, groom: 250, pulse: 250 }))
    localStorage.setItem("devkit.poll." + key, String(ms));
});

const proof = process.env.DASHBOARD_EFFORT_PROOF_DIR;
if (proof) fs.mkdirSync(proof, { recursive: true });
const results = [];
const focus = process.env.DASHBOARD_EFFORT_CASE || "";
const levelsOnly = process.env.DASHBOARD_EFFORT_LEVELS_ONLY === "1";
let flipped = false;
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  // Оболочка открывает функции компонента тесту. Сами app.js и style.css
  // загружаются с сервера побайтово, без вставки кода или правил стиля.
  await context.route(base + "/", async (route) => {
    const response = await route.fetch();
    const html = (await response.text()).replace('<script type="module" src="/assets/app.js', '<script src="/assets/app.js');
    await route.fulfill({ response, body: html });
  });
  const page = await context.newPage();
  page.on("pageerror", (error) => console.error("pageerror", String(error)));
  page.on("requestfailed", (request) => console.error("requestfailed", request.url(), request.failure()?.errorText));
  for (const viewport of [{ width: 320, height: 480 }, { width: 390, height: 844 }, { width: 1280, height: 800 }]) {
    // Фокус называет сочетание целиком, и вьюпорты без назначенных сочетаний
    // страницу не грузят: загрузка и две выдержки тут дороже самих проверок.
    if (focus && ![8, 24].some((count) => ["run", "chat"].some((kind) => ["top", "bottom"].some((side) => focus === `${viewport.width}/${count}/${kind}/${side}`)))) continue;
    await page.setViewportSize(viewport);
    await page.goto(base + "/#demo");
    await page.waitForFunction(() => typeof runControl === "function" && typeof modelPick === "function", null, { timeout: 5000 }).catch(async (error) => { throw new Error(String(error) + " " + await page.locator("body").innerText() + " scripts=" + await page.locator("script").evaluateAll((nodes) => nodes.map((n) => n.outerHTML).join("|"))); });
    await page.waitForFunction(() => boardPainted.screen && document.querySelector("#groups").children.length, null, { timeout: 5000 });
    for (const count of [8, 24]) for (const kind of ["run", "chat"]) for (const side of ["top", "bottom"]) {
      if (focus && focus !== `${viewport.width}/${count}/${kind}/${side}`) continue;
      await page.evaluate(({ count, kind, side }) => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        document.querySelector("#effort-geometry")?.remove();
        document.querySelector("#effort-clip")?.remove();
        document.querySelector("#effort-own")?.remove();
        const models = Array.from({ length: count }, (_, i) => ({
          model: "детерминированная-модель-" + i,
          tier: ["mini", "base", "pro", "max"][i % 4], harness: "stand-" + Math.floor(i / 4),
        }));
        const names = [...new Set(models.map((m) => m.harness))];
        harnessView = { harnesses: names.map((name, i) => ({
          name, default: i === 0, models: models.filter((m) => m.harness === name),
        })) };
        quotaView = { harnesses: names.map((name) => ({ name, age_sec: 1,
          buckets: [{ name: "window5h_all", used_pct: 31 }, { name: "week_all", used_pct: 63 }],
        })) };
        const host = el("div", kind === "chat" ? "cbox" : "");
        host.id = "effort-geometry";
        host.style.cssText = "position:absolute;width:180px;margin:0;z-index:30;" +
          (kind === "chat" ? "left:24px;" : "right:24px;") +
          (side === "top" ? "top:70px" : "bottom:70px");
        // Контейнер задаёт положение и узкий вход. Всё меню и его стили
        // берутся из production-компонентов и текущих assets сервера.
        if (kind === "run") host.append(runControl("demo", "XR-4", (text) => el("button", "btn", text), "Выполнить"));
        else host.append(modelPick("demo", { models, entry: { model: models[0].model, pickHarness: models[0].harness } }));
        const stage = el("div");
        stage.style.cssText = "position:relative;height:100%;min-height:0;flex:1";
        stage.append(host);
        if (kind === "chat") {
          const panel = el("aside", "cpanel");
          panel.id = "effort-clip";
          panel.style.cssText = "position:fixed;top:0;right:0;bottom:0;z-index:40;--cw:420px";
          const pin = el("div", "cpin");
          pin.append(stage);
          panel.append(pin);
          document.body.append(panel);
        } else {
          // Стенд не занимает чужой #groups: группа принадлежит приложению, и
          // его перерисовка заменяет дочерние узлы #groups. Собственный
          // контейнер повторяет размеры и положение настоящей группы:
          // компонент меряет границы раскрытого списка по прокручиваемому
          // предку, и те же размеры с тем же overflow дают ему те же
          // границы. Контейнер не участвует в раскладке приложения и
          // переживает любую перерисовку доски.
          const board = document.querySelector("#groups");
          const own = el("div");
          own.id = "effort-own";
          own.style.cssText = "position:fixed;overflow-y:auto;z-index:30";
          const fit = () => {
            const r = board.getBoundingClientRect();
            own.style.left = r.left + "px";
            own.style.top = r.top + "px";
            own.style.width = r.width + "px";
            own.style.height = r.height + "px";
          };
          fit();
          new ResizeObserver(fit).observe(board);
          own.append(stage);
          own.scrollTop = 0;
          document.body.append(own);
          window.__stand_fit = fit;
        }
        window.__stand_stage = stage;
        window.__stand_host = host;
      }, { count, kind, side });
      const label = JSON.stringify({ viewport, count, kind, side });
      const host = page.locator("#effort-geometry");
      const menu = host.locator(kind === "run" ? ".hpop" : ".menu");
      const anchor = host.locator(kind === "run" ? ".more2" : "select");
      const clicks = [];
      const nativeClick = async (target, expected) => {
        const point = await target.evaluate((n) => {
          const menu = n.closest(".hpop,.menu");
          if (menu) {
            const r = n.getBoundingClientRect(), m = menu.getBoundingClientRect();
            if (r.top < m.top + 2) menu.scrollTop -= m.top + 2 - r.top;
            else if (r.bottom > m.bottom - 2) menu.scrollTop += r.bottom - m.bottom + 2;
          }
          const r = n.getBoundingClientRect();
          const x = r.left + r.width / 2, y = r.top + r.height / 2;
          const hit = document.elementFromPoint(x, y);
          return { x, y, hit: Boolean(hit && (hit === n || n.contains(hit))),
            actual: hit?.outerHTML.slice(0, 220), pageScroll: document.scrollingElement.scrollTop,
            clipping: [...(function* () { for (let at = n.parentElement; at; at = at.parentElement) yield at; })()]
              .filter((at) => /auto|scroll|hidden|clip/.test(getComputedStyle(at).overflowX + getComputedStyle(at).overflowY))
              .map((at) => { const r = at.getBoundingClientRect(); const s = getComputedStyle(at); return { class: at.className, left: r.left, right: r.right, top: r.top, bottom: r.bottom, overflowX: s.overflowX, overflowY: s.overflowY }; }),
          };
        });
        clicks.push({ expected, ...point });
        if (proof && !point.hit) await page.screenshot({ path: proof + "/failed-hit.png" });
        await page.mouse.move(point.x, point.y);
        await page.mouse.down();
        await page.mouse.up();
        assert(point.hit, "реальный click перекрыт " + JSON.stringify({ viewport, count, kind, side, expected, point }));
        assert.equal(await page.evaluate(() => document.scrollingElement.scrollTop), point.pageScroll, "click сдвинул страницу");
      };
      const open = async () => {
        await nativeClick(anchor, "open");
        if (proof && !(await menu.isVisible())) await page.screenshot({ path: proof + "/failed-open.png" });
        assert(await menu.isVisible(), "полный click закрыл меню модели " + JSON.stringify({ viewport, count, kind, side, click: clicks.at(-1) }));
      };
      const boxState = () => page.evaluate(() => {
        const own = document.querySelector("#effort-own"), board = document.querySelector("#groups");
        const r = (n) => n ? n.getBoundingClientRect().toJSON() : null;
        const a = r(own), b = r(board);
        return {
          match: Boolean(a && b) &&
            Math.abs(a.left - b.left) <= 1 && Math.abs(a.top - b.top) <= 1 &&
            Math.abs(a.width - b.width) <= 1 && Math.abs(a.height - b.height) <= 1,
          stand: a, board: b,
        };
      });
      await open();
      if (!flipped && boardNext) {
        // Известная точка сценария: подменяем ответ доски и зовём настоящий
        // refresh, пока перерисовка не покажет новую строку. Контейнер стенда
        // доске не принадлежит, и перерисовка обязана пройти мимо него, не
        // тронув ни узла, ни меню.
        flipped = true;
        fs.copyFileSync(boardNext, boardNow);
        let painted = false;
        for (let i = 0; i < 60 && !painted; i++) {
          painted = await page.evaluate((m) => document.querySelector("#groups").textContent.includes(m), flipMark);
          if (!painted) {
            await page.evaluate(() => refresh().catch(() => {}));
            await page.waitForTimeout(200);
          }
        }
        assert(painted, "принудительное обновление доски не прошло " + label);
        assert(await page.evaluate(() => window.__stand_stage.isConnected &&
          document.querySelector("#effort-geometry") === window.__stand_host),
          "перерисовка доски сняла тестовый узел проверки геометрии " + label);
        assert(await page.evaluate(() => !window.__stand_stage.closest("#groups")),
          "контейнер стенда стоит внутри перерисовываемой доски " + label);
        if (kind !== "chat") {
          const boxes = await boxState();
          assert(boxes.match, "коробка контейнера стенда разошлась с коробкой доски " +
            label + " стенд=" + JSON.stringify(boxes.stand) + " доска=" + JSON.stringify(boxes.board));
        }
        assert(await menu.isVisible(), "перерисовка доски закрыла меню " + label);
      }
      await nativeClick(anchor, "toggle");
      assert(!(await menu.isVisible()), "повторный click не закрыл меню");
      await open();
      await page.mouse.click(5, 5);
      assert(!(await menu.isVisible()), "click снаружи не закрыл меню");
      await open();
      await page.keyboard.press("Escape");
      assert(!(await menu.isVisible()), "Escape не закрыл меню");
      await open();
      if (!levelsOnly) {
      const next = menu.locator(".mrow").nth(1);
      const choice = await next.getAttribute("data-model");
      await nativeClick(next, "model-1");
      assert(await menu.isVisible(), "выбор модели закрыл меню до выбора глубины");
      assert(await next.evaluate((n) => n.classList.contains("on")), "модель не выбрана");
      if (kind === "chat") assert.equal(await anchor.inputValue(), choice, "выбор не отразился в поле модели");
      }
      const geometry = await menu.evaluate((menu) => {
        const rect = (n) => { const r = n.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width }; };
        const marks = [...menu.querySelectorAll(".effmark")];
        return { menu: rect(menu), viewport: { width: innerWidth, height: innerHeight },
          scrollWidth: menu.scrollWidth, clientWidth: menu.clientWidth,
          scrollHeight: menu.scrollHeight, clientHeight: menu.clientHeight,
          overflowY: getComputedStyle(menu).overflowY,
          levels: marks.map((n) => ({ text: n.textContent, ...rect(n) })),
          content: [...menu.querySelectorAll(".hname,.hchip,.hmt,.meter")].map(rect),
          rowCount: menu.querySelectorAll(".mrow").length,
          quotaPerRow: [...menu.querySelectorAll(".mrow")].map((n) => n.querySelectorAll(".hmt").length),
          navigation: rect(document.querySelector(".ptabs")),
          inPanel: Boolean(menu.closest(".cpanel .cpin")),
          inOwn: Boolean(menu.closest("#effort-own")),
        };
      });
      results.push({ viewport, count, kind, side, geometry, clicks });
      if (proof) {
        fs.writeFileSync(proof + "/rects.json", JSON.stringify(results, null, 2));
        await page.screenshot({ path: `${proof}/${viewport.width}-${count}-${kind}-${side}.png` });
      }
      assert(kind === "chat" ? geometry.inPanel : geometry.inOwn, "меню вне своего контейнера " + label);
      assert.equal(geometry.rowCount, count, "состав лестницы " + label);
      assert(geometry.quotaPerRow.every((n) => n === 2), "у каждой подписки две квоты " + label);
      assert(geometry.menu.left >= 0 && geometry.menu.right <= viewport.width + 1, "ширина меню " + label);
      assert(geometry.menu.top >= 0 && geometry.menu.bottom <= viewport.height + 1, "высота меню " + label);
      assert(geometry.scrollWidth <= geometry.clientWidth + 1, "горизонтальное переполнение " + label);
      assert(geometry.content.every((r) => r.width > 0 && r.left >= geometry.menu.left && r.right <= geometry.menu.right), "имена и квоты выходят за фон " + label);
      assert.deepEqual(geometry.levels.map((r) => r.text), ["low", "medium", "high", "xhigh", "max"]);
      assert(geometry.levels.every((r, i, rows) => !i || rows[i - 1].right <= r.left), "подписи глубины перекрываются " + label);
      if (geometry.scrollHeight > geometry.clientHeight + 1) assert.equal(geometry.overflowY, "auto", "прокрутка меню " + label);
      if (!levelsOnly) for (const selector of [".mrow:first-of-type", ".mrow:last-of-type", ".effmark:last-child"]) {
        const target = selector.startsWith(".mrow") ? menu.locator(".mrow")[selector.includes("first") ? "first" : "last"]() : menu.locator(selector);
        await nativeClick(target, selector);
        assert(await menu.isVisible(), "click пункта закрыл меню " + label);
      }
      const checkLevels = async (phase) => {
        for (const level of ["low", "medium", "high", "xhigh", "max"]) {
          const mark = menu.locator(".effmark").filter({ hasText: new RegExp("^" + level + "$") });
          await nativeClick(mark, phase + "/" + level);
          assert(await menu.isVisible(), "click глубины закрыл меню " + label);
          assert(await mark.evaluate((n) => n.classList.contains("on")), "уровень не выбран " + phase + "/" + level + " " + label);
        }
      };
      await checkLevels("initial");
      if (!levelsOnly && viewport.width === 1280 && count === 8 && side === "bottom") {
        for (const resized of [{ width: 390, height: 844 }, viewport]) {
          await page.keyboard.press("Escape");
          await page.setViewportSize(resized);
          // Барьер готовой геометрии: наблюдатель своей коробки асинхронный,
          // и клик по старой коробке уходил за край нового окна. Размеры и
          // положение контейнера обновляются синхронно, до первого клика,
          // без ожидания кадра.
          await page.evaluate(() => window.__stand_fit?.());
          if (kind !== "chat") {
            const boxes = await boxState();
            assert(boxes.match, "после смены окна коробка стенда разошлась с коробкой доски " +
              label + " стенд=" + JSON.stringify(boxes.stand) + " доска=" + JSON.stringify(boxes.board));
          }
          await open();
          await checkLevels("resize-" + resized.width);
        }
      }
      if (proof) {
        results.at(-1).afterScroll = await menu.evaluate((n) => ({ scrollTop: n.scrollTop, scrollHeight: n.scrollHeight, clientHeight: n.clientHeight }));
        fs.writeFileSync(proof + "/rects.json", JSON.stringify(results, null, 2));
        await page.screenshot({ path: `${proof}/${viewport.width}-${count}-${kind}-${side}-scrolled.png` });
      }
    }
  }
  assert.equal(results.length, focus ? 1 : 24, "проверены все назначенные сочетания");
  console.log(JSON.stringify({ ok: true, cases: results.length }));
} finally {
  await browser.close();
}

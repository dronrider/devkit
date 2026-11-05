import { createRequire } from "node:module";
import assert from "node:assert/strict";
import fs from "node:fs";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
const proof = process.env.DASHBOARD_EFFORT_PROOF_DIR;
if (proof) fs.mkdirSync(proof, { recursive: true });
const results = [];
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
    await page.setViewportSize(viewport);
    await page.goto(base + "/#demo");
    await page.waitForFunction(() => typeof runControl === "function" && typeof modelPick === "function", null, { timeout: 5000 }).catch(async (error) => { throw new Error(String(error) + " " + await page.locator("body").innerText() + " scripts=" + await page.locator("script").evaluateAll((nodes) => nodes.map((n) => n.outerHTML).join("|"))); });
    for (const count of [8, 24]) for (const kind of ["run", "chat"]) for (const side of ["top", "bottom"]) {
      await page.evaluate(({ count, kind, side }) => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        document.querySelector("#effort-geometry")?.remove();
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
        host.style.cssText = "position:fixed;right:24px;width:180px;margin:0;z-index:100;" +
          (side === "top" ? "top:70px" : "bottom:70px");
        // Контейнер задаёт положение и узкий вход. Всё меню и его стили
        // берутся из production-компонентов и встроенных assets сервера.
        if (kind === "run") host.append(runControl("demo", "XR-4", (text) => el("button", "btn", text), "Выполнить"));
        else host.append(modelPick("demo", { models, entry: { model: models[0].model, pickHarness: models[0].harness } }));
        document.body.append(host);
      }, { count, kind, side });
      const host = page.locator("#effort-geometry");
      if (kind === "run") await host.locator(".more2").click();
      else await host.locator("select").dispatchEvent("mousedown", { bubbles: true, cancelable: true });
      const menu = host.locator(kind === "run" ? ".hpop" : ".menu");
      await menu.waitFor({ state: "visible" });
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
        };
      });
      const label = JSON.stringify({ viewport, count, kind, side });
      results.push({ viewport, count, kind, side, geometry });
      if (proof) {
        fs.writeFileSync(proof + "/rects.json", JSON.stringify(results, null, 2));
        await page.screenshot({ path: `${proof}/${viewport.width}-${count}-${kind}-${side}.png` });
      }
      assert.equal(geometry.rowCount, count, "состав лестницы " + label);
      assert(geometry.quotaPerRow.every((n) => n === 2), "у каждой подписки две квоты " + label);
      assert(geometry.menu.left >= 0 && geometry.menu.right <= viewport.width + 1, "ширина меню " + label);
      assert(geometry.menu.top >= 0 && geometry.menu.bottom <= viewport.height + 1, "высота меню " + label);
      assert(geometry.scrollWidth <= geometry.clientWidth + 1, "горизонтальное переполнение " + label);
      assert(geometry.content.every((r) => r.width > 0 && r.left >= geometry.menu.left && r.right <= geometry.menu.right), "имена и квоты выходят за фон " + label);
      assert.deepEqual(geometry.levels.map((r) => r.text), ["low", "medium", "high", "xhigh", "max"]);
      assert(geometry.levels.every((r, i, rows) => !i || rows[i - 1].right <= r.left), "подписи глубины перекрываются " + label);
      if (geometry.scrollHeight > geometry.clientHeight + 1) assert.equal(geometry.overflowY, "auto", "прокрутка меню " + label);
      for (const selector of [".mrow:first-of-type", ".mrow:last-of-type", ".effmark:last-child"]) {
        const target = selector.startsWith(".mrow") ? menu.locator(".mrow")[selector.includes("first") ? "first" : "last"]() : menu.locator(selector);
        const visible = await target.evaluate((n) => {
          n.scrollIntoView({ block: "nearest" });
          const r = n.getBoundingClientRect(), box = n.closest(".hpop,.menu").getBoundingClientRect();
          return r.top >= box.top && r.bottom <= box.bottom && r.top >= 0 && r.bottom <= innerHeight;
        });
        assert(visible, "недоступный пункт " + selector + " " + label);
      }
      await menu.locator(".effmark").last().click();
      assert(await menu.locator(".effmark").last().evaluate((n) => n.classList.contains("on")), "max недоступен " + label);
      if (proof) {
        results.at(-1).afterScroll = await menu.evaluate((n) => ({ scrollTop: n.scrollTop, scrollHeight: n.scrollHeight, clientHeight: n.clientHeight }));
        fs.writeFileSync(proof + "/rects.json", JSON.stringify(results, null, 2));
        await page.screenshot({ path: `${proof}/${viewport.width}-${count}-${kind}-${side}-scrolled.png` });
      }
    }
  }
  console.log(JSON.stringify({ ok: true, cases: results.length }));
} finally {
  await browser.close();
}

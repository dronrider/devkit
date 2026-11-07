import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const { chromium } = createRequire(import.meta.url)('playwright');
const [base, executablePath, token, sid, mode] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
try {
  assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
  const prepared = await (await context.request.get(base+'/__worktree?mode=state')).json();
  const key = prepared.key;
  const number = prepared.number;
  assert.match(number,/^XR-\d+$/);
  const page = await context.newPage();
  await page.goto(base+'/#demo/chat/'+sid);
  // Вопрос согласования короткий: тел задач в чате нет, каждая задача это
  // строка-ссылка на свой экран (DK-1469, решение 4). Снимок записи в чат
  // не попадает и тут.
  await page.locator('.askfb').getByText('Подготовил заключение разбора. Согласуй весь результат.',{exact:true}).waitFor();
  const text = await page.locator('.askfb').innerText();
  assert.ok(!/Показанный снимок|[a-f0-9]{64}/.test(text));
  assert.ok(!text.includes('веточный результат'));
  assert.ok(!text.includes('## DoD'));
  assert.ok(text.includes('Результат'));
  await page.locator('.askfb').getByText('Состав разбора',{exact:true}).waitFor();
  const creation=page.locator('.askfb li').filter({hasText:'Создать '+number+' - Результат'});
  assert.equal(await creation.count(),1,text);
  const taskLink=creation.locator('a[href$="/draft/XR-4/prepared/'+key+'"]');
  assert.equal(await taskLink.innerText(),number);
  assert.equal(await page.locator('.askfb a[href*="/prepared/"]').count(),1);
  await page.locator('.askfb a[href$="/draft/XR-4"]').waitFor();
  assert.equal(await page.locator('.askfb input.caskpick').count(),1);
  await page.locator('.askfb a[href$="/draft/XR-4/prepared/'+key+'"]').waitFor();
  await page.getByLabel('Подтвердить разбор XR-4').check();
  assert.equal(await page.locator('textarea.csay').inputValue(),'');
  if (mode === 'linked') {
    const response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
    await page.getByRole('button',{name:'Отправить',exact:true}).click();
    assert.notEqual((await response).status(),200);
    await page.getByText(/линкованный worktree/).first().waitFor();
    const state=await (await context.request.get(base+'/__worktree?mode=state')).json();
    assert.equal(state.status,''); assert.equal(state.mainUnchanged,true);
  } else {
    assert.equal((await context.request.get(base+'/__worktree?mode=break')).status(),200);
    let response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
    await page.getByRole('button',{name:'Отправить',exact:true}).click();
    assert.equal((await response).status(),502);
    let state=await (await context.request.get(base+'/__worktree?mode=state')).json();
    assert.equal(state.status,'accepted'); assert.equal(state.rows,1);
    assert.equal((await context.request.get(base+'/__worktree?mode=rebind')).status(),200);
    await page.reload(); await page.locator('textarea.csay').waitFor();
    response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST' && r.status()===200);
    await page.evaluate(()=>window.dispatchEvent(new Event('online'))); await response;
    state=await (await context.request.get(base+'/__worktree?mode=state')).json();
    assert.equal(state.status,'accepted'); assert.equal(state.rows,1);
    assert.equal(state.oldCarrier,true); assert.equal(state.otherCarrier,false);
  }
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  console.log(JSON.stringify({worktree:true,rebind:true,retry:true,mobile:390}));
} finally { await context.close(); await browser.close(); }

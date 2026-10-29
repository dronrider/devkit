import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const { chromium } = createRequire(import.meta.url)('playwright');
const [base, executablePath, token, sid] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
try {
  assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
  const page = await context.newPage();
  await page.goto(base+'/#demo/chat/'+sid);
  await page.locator('.askproposal').getByText('веточный результат',{exact:false}).waitFor();
  await page.getByLabel('Решение XR-4 p1').selectOption('reject');
  await page.getByLabel('Причина отказа XR-4 p1').fill('не нужно');
  assert.equal((await context.request.get(base+'/__worktree?mode=break')).status(),200);
  let response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
  await page.getByRole('button',{name:'Отправить',exact:true}).click();
  assert.equal((await response).status(),502);
  let state=await (await context.request.get(base+'/__worktree?mode=state')).json();
  assert.equal(state.status,'rejected'); assert.equal(state.mainUnchanged,true);
  assert.equal((await context.request.get(base+'/__worktree?mode=rebind')).status(),200);
  await page.reload(); await page.locator('textarea.csay').waitFor();
  response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST' && r.status()===200);
  await page.evaluate(()=>window.dispatchEvent(new Event('online'))); await response;
  state=await (await context.request.get(base+'/__worktree?mode=state')).json();
  assert.equal(state.status,'rejected'); assert.equal(state.mainUnchanged,true);
  assert.equal(state.oldCarrier,true); assert.equal(state.otherCarrier,false);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  console.log(JSON.stringify({worktree:true,rebind:true,retry:true,mobile:390}));
} finally { await context.close(); await browser.close(); }

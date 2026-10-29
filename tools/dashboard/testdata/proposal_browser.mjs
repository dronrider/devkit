import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const { chromium } = createRequire(import.meta.url)('playwright');
const [base, executablePath, token, sid] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
try {
  assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
  const page=await context.newPage(); const sent=[];
  page.on('request',(r)=>{ if(r.method()==='POST' && r.url().endsWith('/say')) sent.push(r.postDataJSON()); });
  await page.goto(base+'/#demo/chat/'+sid);
  await page.locator('.askproposal').getByText('карточка один',{exact:false}).waitFor();
  const ta=page.locator('textarea.csay'); await ta.fill('почему такой исход?');
  await page.reload(); await page.locator('.askproposal').waitFor(); assert.equal(await ta.inputValue(),'почему такой исход?');
  let response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
  await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),200);
  assert.equal(sent[0].proposal.mode,'comment'); assert.equal(sent[0].proposal.decisions.length,0);
  let view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.proposals[0].status,undefined);
  const select=page.getByLabel('Решение XR-4 p1'); await select.selectOption('reject'); await page.getByLabel('Причина отказа XR-4 p1').fill('не нужно');
  await page.reload(); await page.locator('.askproposal').waitFor(); assert.equal(await page.getByLabel('Решение XR-4 p1').inputValue(),'reject'); assert.equal(await page.getByLabel('Причина отказа XR-4 p1').inputValue(),'не нужно');
  response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST'); await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),200);
  view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.proposals[0].status,'rejected'); assert.equal(view.state.proposals[1].status,undefined);
  assert.equal((await context.request.get(base+'/__proposal?fresh=1')).status(),200); await page.reload(); await page.getByRole('button',{name:'Принять все показанные'}).waitFor();
  await page.getByRole('button',{name:'Принять все показанные'}).click(); response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST'); await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),200);
  view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.state,'history'); assert.equal(view.state.proposals[1].status,'accepted');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  console.log(JSON.stringify({comment:true,refresh:true,reject:true,all:true,mobile:390}));
} finally { await context.close(); await browser.close(); }

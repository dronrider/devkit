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
  // Вопрос согласования идёт репликой со строками-ссылками, а не витриной тел
  // (DK-1469, решение 4): галки висят на строках задач, тело читается по ссылке.
  await page.locator('.askfb').getByText('Подготовил задачи',{exact:false}).waitFor();
  await page.getByLabel('Подтвердить p1').waitFor();
  await page.getByLabel('Подтвердить p2').waitFor();
  await page.getByLabel('Подтвердить p3').waitFor();
  const link=page.locator('.askfb a[href="#demo/draft/XR-4/prepared/p1"]');
  await link.waitFor(); assert.match(await link.textContent(),/#demo\/draft\/XR-4\/prepared\/p1/);
  const ta=page.locator('textarea.csay'); await ta.fill('почему такой исход?');
  await page.reload(); await page.locator('.askfb').waitFor(); assert.equal(await ta.inputValue(),'почему такой исход?');
  let response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
  await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),200);
  assert.equal(sent[0].proposal.mode,'comment'); assert.equal(sent[0].proposal.decisions.length,0);
  let view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.proposals[0].status,undefined);
  // Отмеченная галкой задача подтверждается, отметка переживает перезагрузку.
  await page.getByLabel('Подтвердить p1').check();
  await page.reload(); await page.getByLabel('Подтвердить p1').waitFor();
  assert.equal(await page.getByLabel('Подтвердить p1').isChecked(),true);
  assert.equal((await context.request.get(base+'/__delivery?mode=break')).status(),200);
  response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST'); await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),502);
  view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.proposals[0].status,'accepted'); assert.equal(view.state.proposals[1].status,undefined);
  assert.equal((await context.request.get(base+'/__delivery?mode=restore')).status(),200);
  await page.reload(); await page.locator('textarea.csay').waitFor();
  response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST' && r.status()===200);
  await page.evaluate(()=>window.dispatchEvent(new Event('online'))); await response;
  view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.proposals[0].status,'accepted'); assert.equal(view.state.proposals[1].status,undefined);
  await page.reload(); await page.locator('.msg.me .proposal-attachment').filter({hasText:'p1'}).first().waitFor();
  assert.equal(await page.locator('.askresume').count(),0);
  assert.doesNotMatch(await page.locator('.chatfeed').innerText(),/Ответ панели обработан taskctl/);
  assert.equal((await context.request.get(base+'/__proposal?fresh=both')).status(),200); await page.reload();
  await page.getByLabel('Подтвердить p2').check(); await page.getByLabel('Подтвердить p3').check();
  response=page.waitForResponse(r=>r.url().endsWith('/say') && r.request().method()==='POST');
  await page.getByRole('button',{name:'Отправить',exact:true}).click(); assert.equal((await response).status(),200);
  view=await (await context.request.get(base+'/__proposal')).json(); assert.equal(view.state.state,'history'); assert.equal(view.state.proposals[1].status,'accepted');
  view=await (await context.request.get(base+'/__proposal?id=XR-5')).json(); assert.equal(view.state.proposals[0].status,'accepted');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  console.log(JSON.stringify({comment:true,refresh:true,accept:true,marks:true,mobile:390}));
} finally { await context.close(); await browser.close(); }

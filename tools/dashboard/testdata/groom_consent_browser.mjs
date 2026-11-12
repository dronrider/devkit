import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const { chromium }=createRequire(import.meta.url)('playwright');
const [base,executablePath,token,sid,mode,selection]=process.argv.slice(2);
const browser=await chromium.launch({executablePath,headless:true});
const context=await browser.newContext({viewport:{width:1440,height:1000}});
await context.addInitScript(() => {
 for(const [key,ms] of Object.entries({ask:250,groom:250,pulse:250}))
  localStorage.setItem("devkit.poll."+key,String(ms));
});
try {
 assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
 const page=await context.newPage();const sent=[];
 page.on('request',r=>{if(r.method()==='POST'&&r.url().endsWith('/say'))sent.push(r.postDataJSON());});
 await page.goto(base+'/#demo/draft/XR-4');
 const confirm=page.getByRole('button',{name:'Подтвердить разбор',exact:true});
 await confirm.waitFor();
 if(mode==='writer') {
  assert.equal(await confirm.isDisabled(),true);
  assert.equal(await page.getByRole('button',{name:'Стоп',exact:true}).count(),1);
  assert.equal(await page.getByText('Сначала завершите разбор',{exact:true}).count(),0);
  console.log('active writer keeps disabled confirm and Stop without extra warning: ok');
 } else {
 assert.equal(await confirm.isEnabled(),true);
 assert.equal(await page.getByRole('button',{name:'Стоп',exact:true}).count(),0);
 assert.equal(await page.getByText('Сначала завершите разбор',{exact:true}).count(),0);
 await page.goto(base+'/#demo/chat/'+sid);
 const first=()=>page.getByLabel('Подтвердить разбор XR-4',{exact:true});
 const second=()=>page.getByLabel('Подтвердить разбор XR-5',{exact:true});
 await first().waitFor();assert.equal(await first().isChecked(),true);assert.equal(await second().isChecked(),true);
 await page.getByRole('button',{name:'По рекомендации',exact:true}).waitFor();
 await page.locator('.busyrow').waitFor({state:'hidden'});
 assert.equal(await page.locator('.askfb a[href*="/prepared/"]').count(),2);
 assert.equal(await page.getByRole('button',{name:'Стоп',exact:true}).count(),0);
 if(mode!=='surface') {
 // Source choices always carry their complete plans. Text is discussion.
 await second().uncheck();await page.getByRole('button',{name:'Мой вариант',exact:true}).waitFor();
 const noAsk=()=>page.waitForResponse(async r=>r.url().endsWith('/ask')&&r.request().method()==='GET'&&!(await r.json()).ask);
 let poll=noAsk();
 assert.equal((await context.request.post(base+'/__consent-fixture?mode=writer')).status(),200);
 await poll;await noAsk();
 assert.equal(await first().isChecked(),true);assert.equal(await second().isChecked(),false);
 let stale=page.waitForResponse(r=>r.url().endsWith('/say')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'Мой вариант',exact:true}).click();
 assert.notEqual((await stale).status(),200,'a touched stale choice applied while a writer was active');
 const restored=page.waitForResponse(async r=>r.url().endsWith('/ask')&&r.request().method()==='GET'&&(await r.json()).ask?.kind==='agent');
 assert.equal((await context.request.post(base+'/__consent-fixture?mode=restore')).status(),200);
 await restored;
 await page.getByRole('button',{name:'По рекомендации',exact:true}).waitFor();
 await second().uncheck();

 const ta=page.locator('textarea.csay');await ta.fill('почему такой исход?');
 let response=page.waitForResponse(r=>r.url().endsWith('/say')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'Выбрать и обсудить',exact:true}).click();
 assert.equal((await response).status(),200);assert.equal(sent.at(-1).proposal.mode,'comment');assert.equal(sent.at(-1).proposal.decisions.length,0);
 await page.reload();await first().waitFor();assert.equal(await first().isChecked(),false);assert.equal(await second().isChecked(),false);
 await first().check();if(selection!=='one-source') await second().check();
 const applyCaption=selection==='one-source'?'Мой вариант':'По рекомендации';
 await page.getByRole('button',{name:applyCaption,exact:true}).waitFor();
 response=page.waitForResponse(r=>r.url().endsWith('/say')&&r.request().method()==='POST');
 await page.getByRole('button',{name:applyCaption,exact:true}).click();
 const applied=await response;assert.equal(applied.status(),200,await applied.text());
 const answer=sent.at(-1).proposal;assert.equal(answer.decisions.length,selection==='one-source'?3:4);assert.equal(answer.snapshots.length,selection==='one-source'?1:2);
 const replay=await context.request.post(base+'/api/projects/demo/chats/'+sid+'/say',{data:sent.at(-1)});assert.equal(replay.status(),200,await replay.text());
 await page.reload();await page.locator('textarea.csay').waitFor();
 const ask=await (await context.request.get(base+'/api/projects/demo/chats/'+sid+'/ask')).json();
 assert.equal(ask.ask,undefined);assert.equal(ask.consent,true);assert.match(ask.continuation,/подтвержден/);
 const status=await (await context.request.get(base+'/api/projects/demo/chats/'+sid+'/status')).json();assert.equal(status.busy,false);assert.equal(status.consent,true);
 await page.locator('.busyrow').waitFor({state:'hidden'});
 await page.waitForFunction(()=>document.querySelectorAll('.caskpick').length===0);
 assert.equal(await page.locator('.caskpick').count(),0);
 assert.equal(await page.getByText('Клиент ждёт ответа',{exact:true}).count(),0);
 console.log('canonical source choices, ready form, native duplicate, comments, apply and replay: ok');
 } else console.log('current pending native displays canonical whole-consent surface: ok');
 }
} finally {await context.close();await browser.close();}

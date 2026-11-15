import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const { chromium }=createRequire(import.meta.url)('playwright');
const [base,executablePath,token,sid]=process.argv.slice(2);
const browser=await chromium.launch({executablePath,headless:true});
const context=await browser.newContext({viewport:{width:1440,height:1000}});
await context.addInitScript(() => {
 for(const [key,ms] of Object.entries({ask:250,groom:250,pulse:250}))
  localStorage.setItem("devkit.poll."+key,String(ms));
});
try {
 assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
 const page=await context.newPage();await page.goto(base+'/#demo/draft/XR-4');
 const confirm=page.getByRole('button',{name:'Подтвердить разбор',exact:true});await confirm.waitFor();assert.equal(await confirm.isEnabled(),true);
 const response=page.waitForResponse(r=>r.url().endsWith('/drafts/prepared/confirm')&&r.request().method()==='POST');
 await confirm.click();const applied=await response;assert.equal(applied.status(),200,await applied.text());
 const payload=applied.request().postDataJSON();
 assert.equal(payload.snapshots.length,1);assert.equal(payload.snapshots[0].source,'XR-4');
 assert.equal(payload.decisions.length,3);assert.ok(payload.decisions.every(d=>d.source==='XR-4'&&d.action==='accept'));
 const receipt=await applied.json();assert.ok(receipt.key);assert.equal(receipt.results.length,3);
 const replay=await context.request.post(base+'/api/projects/demo/drafts/prepared/confirm',{data:payload});
 assert.equal(replay.status(),200,await replay.text());const repeated=await replay.json();assert.equal(repeated.key,receipt.key);assert.equal(repeated.repeated,true);
 for(let pass=0;pass<2;pass++) {
  if(!pass) await page.goto(base+'/#demo/chat/'+sid);else await page.reload();
  await page.locator('textarea.csay').waitFor();
  const ask=await (await context.request.get(base+'/api/projects/demo/chats/'+sid+'/ask')).json();
  const status=await (await context.request.get(base+'/api/projects/demo/chats/'+sid+'/status')).json();
  assert.equal(ask.ask,undefined,'actual form click resurfaced old canonical/native question');
  assert.equal(status.consent,true,'actual form click returned to busy/native wait');assert.equal(status.busy,false);
  assert.match(ask.continuation,/подтвержден/);
  await page.waitForFunction(()=>document.querySelectorAll('.caskpick').length===0);
  await page.locator('.busyrow').waitFor({state:'hidden'});
  assert.equal(await page.getByText('Клиент ждёт ответа',{exact:true}).count(),0);
 }
 console.log('actual form click, selected receipt, replay and chat reload without native resurfacing: ok');
} finally {await context.close();await browser.close();}

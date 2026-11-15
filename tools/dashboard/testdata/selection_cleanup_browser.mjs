import {createRequire} from 'node:module';
import assert from 'node:assert/strict';
const {chromium}=createRequire(import.meta.url)('playwright');
const [base,executablePath,token]=process.argv.slice(2);
const browser=await chromium.launch({executablePath,headless:true});
const context=await browser.newContext();
await context.addInitScript(() => {
 for(const [key,ms] of Object.entries({ask:250,groom:250,pulse:250}))
  localStorage.setItem("devkit.poll."+key,String(ms));
});

try {
 assert.equal((await context.request.post(base+'/api/login',{data:{token}})).status(),200);
 const page=await context.newPage();
 const errors=[]; page.on('pageerror',(e)=>errors.push(String(e)));
 await page.goto(base+'/#demo/chat/aa144600-1111-4111-8111-111111111111');
 await page.locator('.caskpick').first().waitFor();
 assert.equal(await page.locator('.askfb').count(),0,'вопрос должен быть настоящей репликой');
 assert.equal(await page.locator('.attach-selection,.selection-preview,.selection-attachment').count(),0);
 const sent=page.waitForResponse((r)=>r.url().endsWith('/say')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'По рекомендации',exact:true}).click();
 assert.equal((await sent).status(),200);
 await page.locator('.caskpick:disabled').first().waitFor();
 assert.equal(await page.locator('.caskpick:disabled').count(),3);
 const left=await page.locator('.attach-selection').count();
 if (left) await page.locator('.attach-selection').first().click();
 assert.equal(left,0,'после снятия Ask осталась кнопка прежнего вопроса');
 assert.deepEqual(errors,[]);
 let releaseTranscript;
 const transcriptReady=new Promise((resolve)=>{releaseTranscript=resolve;});
 await page.route('**/sessions/aa144600-1111-4111-8111-111111111111?*',async(route)=>{
  if (new URL(route.request().url()).searchParams.has('n')) await transcriptReady;
  await route.continue();
 });
 assert.equal((await context.request.get(base+'/__scenario?do=fresh')).status(),200);
 await page.locator('.askfb .caskpick:not(:disabled)').first().waitFor();
 assert.equal(await page.locator('.askfb').count(),1,'до транскрипта вопрос показан временной репликой');
 releaseTranscript();
 await page.evaluate(()=>window.dispatchEvent(new Event('online')));
 await page.locator('.msg:not(.askfb) .caskpick:not(:disabled)').first().waitFor();
 assert.equal(await page.locator('.askfb').count(),0,'после транскрипта временная реплика снята');
 assert.equal(await page.locator('.caskpick:disabled').count(),3,'подтверждённый выбор сохранён после догона');
 assert.equal((await context.request.get(base+'/__scenario?do=next-real')).status(),200);
 await page.locator('.msg:not(.askfb) .caskpick[aria-label="Ответить «доставка 1»"]').waitFor();
 assert.equal(await page.locator('.askfb').count(),0,'следующий вопрос должен быть настоящей репликой');
 assert.equal(await page.locator('.caskpick:disabled').count(),3,'подтверждённый выбор остаётся в своём вопросе');
 assert.equal(await page.locator('.attach-selection').count(),0,'кнопка прежнего вопроса пережила смену вопроса');
 assert.deepEqual(errors,[]);
 console.log(JSON.stringify({realQuestion:true,closedCleanup:true,nextQuestion:true,errors}));
} finally {await context.close(); await browser.close();}

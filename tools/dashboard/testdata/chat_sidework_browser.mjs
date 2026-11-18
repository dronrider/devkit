import {createRequire} from "node:module";
import assert from "node:assert/strict";
const {chromium}=createRequire(import.meta.url)("playwright");
const [base,executablePath,token]=process.argv.slice(2);
const browser=await chromium.launch({executablePath,headless:true});
try {
 const context=await browser.newContext();
 assert.equal((await context.request.post(base+"/api/login",{data:{token}})).status(),200);
 await context.route(base+"/",async(route)=>{
  const response=await route.fetch();
  const html=(await response.text()).replace('<script type="module" src="/assets/app.js','<script src="/assets/app.js');
  await route.fulfill({response,body:html});
 });
 const page=await context.newPage();
 await page.goto(base+"/#demo");
 await page.waitForFunction(()=>typeof chatPanel==="function");
 const result=await page.evaluate(async()=>{
  const sid="aaaa1726-1111-4111-8111-111111111111";
  const calls=[];
  api=async(url,opts)=>{
   if(opts?.method==="POST")calls.push({url,body:opts.body});
   return {ok:true,status:200,body:{items:[],way:"send-keys"}};
  };
  const st={project:"demo",addr:sid,sid,task:"XR-5",models:[],chats:[],
   wait:{state:"ждёт",questions:["вопрос побочной задачи"]},
   entry:{id:sid,state:"live",idle:true,own:true,tasks:["XR-5","XR-4"],leads:["XR-5","XR-4"]}};
  const kind=chatWay(st).kind;
  const panel=chatPanel("demo",st);
  document.body.replaceChildren(panel);
  const ta=panel.querySelector("textarea.csay");
  ta.value="давай, довози поезд";
  panel.querySelector(".csend").click();
  await Promise.resolve();await Promise.resolve();await Promise.resolve();
  const echo=panel.querySelector(".mlocal").textContent;
  const draft=ta.value;
  for(const stop of chatLive.splice(0))stop();
  return {kind,calls,echo,draft,sid};
 });
 assert.equal(result.kind,"say");
 assert.equal(result.calls.length,1);
 assert.equal(result.calls[0].url,"/api/projects/demo/chats/"+result.sid+"/say");
 assert.equal(result.calls[0].body.text,"давай, довози поезд");
 assert.ok(result.calls[0].body.msg_id);
 assert.ok(result.echo.includes("давай, довози поезд"));
 assert.equal(result.draft,"");
 console.log("live idle + side task waiting: один /say в SID, текст и ключ сохранены");
} finally {await browser.close();}

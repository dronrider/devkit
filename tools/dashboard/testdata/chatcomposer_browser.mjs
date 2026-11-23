import { createRequire } from "node:module";
import assert from "node:assert/strict";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
const errors = [];
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  await context.route(base + "/", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace('<script type="module" src="/assets/app.js', '<script src="/assets/app.js') });
  });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(base + "/#demo");
  await page.waitForFunction(() => typeof chatPanel === "function" && document.querySelector("#groups").children.length);
  await page.evaluate(() => {
    refresh = async () => {};
    window.testModels = [{model:"stand-a",harness:"codex",tier:"pro"},{model:"stand-b",harness:"codex",tier:"base"}];
    window.mount = (extra = {}) => {
      closeChatLive();
      const st = { project:"demo",addr:"blank-stand",sid:"",task:"",blank:"blank-stand",fresh:true,
        chats:[],models:testModels,entry:{model:"stand-a",pickHarness:"codex",state:"blank"},...extra };
      window.testState = st;
      const panel = document.getElementById("cpanel");
      panel.hidden = false;
      const pin = document.getElementById("cpin");
      chatOpen="demo|"+st.addr;
      chatSlotPut(pin,chatOpen,[chatHead("demo",st),chatPanel("demo",st)]);
      return st;
    };
    mount();
  });
  const ta = page.locator("#cpin .cslot:not(.off) textarea");
  const send = page.locator("#cpin .cslot:not(.off) .csend");
  const geometry = () => page.evaluate(() => {
    const ta=document.querySelector("#cpin .cslot:not(.off) textarea");
    const wrap=document.querySelector("#cpin .cslot:not(.off) .chatwrap").getBoundingClientRect();
    const button=document.querySelector("#cpin .cslot:not(.off) .csend").getBoundingClientRect();
    return {height:ta.getBoundingClientRect().height, top:ta.getBoundingClientRect().top,
      bottom:button.bottom, buttonTop:button.top, scroll:ta.scrollHeight, client:ta.clientHeight,
      cap:Math.max(44,Math.min(420,(Math.min(wrap.bottom,visualViewport.height)-Math.max(0,wrap.top))/2))};
  });
  for (const width of [1280,390]) {
    await page.setViewportSize({width,height:800});
    await ta.fill("");
    const small=await geometry();
    await ta.fill("строка с переносом ".repeat(30));
    const grown=await geometry();
    assert(grown.height>small.height,JSON.stringify({small,grown}));
    assert(Math.abs(grown.bottom-small.bottom)<2,"кнопка уехала при росте: "+JSON.stringify({width,small,grown}));
    assert(grown.top<small.top,"поле не растёт вверх");
    await ta.fill(Array(80).fill("абзац").join("\n"));
    const cap=await geometry();
    assert(cap.height<=cap.cap+2,JSON.stringify(cap));
    assert(cap.scroll>cap.client,"нет внутренней прокрутки");
    await ta.fill("коротко");
    assert((await geometry()).height<cap.height,"поле не сжалось");
  }
  // Высота видимой панели после появления клавиатуры ограничивает рост.
  await page.setViewportSize({width:390,height:400});
  await ta.fill(Array(80).fill("абзац").join("\n"));
  const keyboard=await geometry();
  assert(keyboard.height<=keyboard.cap+2 && keyboard.bottom<=400,JSON.stringify(keyboard));
  await page.setViewportSize({width:1280,height:800});
  await ta.fill("черновик после обновления");
  await page.evaluate(() => {
    chatEffortSet("demo","blank-stand","high");
    chatDraftHeightWrite("blank-stand",180);
  });
  await page.reload();
  await page.waitForFunction(() => typeof chatPanel === "function" && document.querySelector("#groups").children.length);
  assert.equal(await page.evaluate(()=>chatEffortOf("demo","blank-stand")),"high");
  // Вернуть стенд после настоящей перезагрузки приложения.
  await page.evaluate(() => {
    refresh = async () => {};
    window.testModels=[{model:"stand-a",harness:"codex",tier:"pro"},{model:"stand-b",harness:"codex",tier:"base"}];
    window.mount=(extra={})=>{
      closeChatLive();
      const st={project:"demo",addr:"blank-stand",sid:"",task:"",blank:"blank-stand",fresh:true,chats:[],models:testModels,entry:{model:"stand-a",pickHarness:"codex",state:"blank"},...extra};
      window.testState=st;
      document.getElementById("cpanel").hidden=false;
      chatOpen="demo|"+st.addr;
      chatSlotPut(document.getElementById("cpin"),chatOpen,[chatHead("demo",st),chatPanel("demo",st)]);
    };
    mount();
  });
  assert.equal(await ta.inputValue(),"черновик после обновления");
  assert((await geometry()).height>=180,"ручной минимум не восстановлен");
  assert.equal(await page.locator("#cpin .cslot:not(.off) .mbar").innerText(),"high");
  // Вложение открывается локально до ответа на upload и после восстановления.
  const pngs = await page.evaluate(()=>["red","blue"].map(color=>{
    const c=document.createElement("canvas");c.width=8;c.height=8;
    const x=c.getContext("2d");x.fillStyle=color;x.fillRect(0,0,8,8);return c.toDataURL();
  }));
  let releaseShots;
  const shotsGate = new Promise(resolve => { releaseShots = resolve; });
  let shotCount = 0;
  await page.route("**/api/projects/demo/chats/*/shot", async route => {
    const path = "/tmp/blank-stand/shot-" + (++shotCount) + ".png";
    await shotsGate;
    await route.fulfill({ json: { path } });
  });
  await ta.evaluate((node, pics) => {
    for (const [i, pic] of pics.entries()) {
      const bytes = Uint8Array.from(atob(pic.split(",")[1]), c => c.charCodeAt(0));
      const data = new DataTransfer();
      data.items.add(new File([bytes], "снимок-" + i + ".png", { type: "image/png" }));
      node.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
    }
  }, pngs);
  const thumbs=page.locator("#cpin .cslot:not(.off) .cshot");
  await thumbs.nth(1).waitFor({ state: "visible" });
  assert.equal(await thumbs.count(),2);
  for(let i=0;i<2;i++) {
    await thumbs.nth(i).click();
    assert.equal(await page.locator(".shotbig img").getAttribute("src"),pngs[i]);
    await page.keyboard.press("Escape");
    assert.equal(await ta.inputValue(),"черновик после обновления");
  }
  await page.locator("#cpin .cslot:not(.off) .cclipx").first().click();
  assert.equal(await thumbs.count(),1);
  assert.equal(await page.locator(".shotbig").count(),0);
  await page.locator("#cpin .cslot:not(.off) .cclipx").click();
  assert.equal(await thumbs.count(),0);
  releaseShots();
  // Новый чат: управляемая задержка ответа, затем задержка состояния новой панели.
  let releaseStart,releaseState;
  const startGate=new Promise(resolve=>releaseStart=resolve);
  const stateGate=new Promise(resolve=>releaseState=resolve);
  let firstBody;
  await page.route("**/api/projects/demo/chats",async route=>{
    if(route.request().method()!=="POST") {await route.fallback();return;}
    firstBody=route.request().postDataJSON();
    await startGate;
    await route.fulfill({json:{way:"appserver",session:"as-stand",message:"чат поднят"}});
  });
  await page.evaluate(()=>{
    window.toastCalls=[];
    sayResult=(...args)=>toastCalls.push(args);
    chatState=async()=>{
      await fetch("/stand-state");
      return {project:"demo",addr:"as-stand",sid:"as-stand",task:"",chats:[],models:testModels,
        entry:{id:"as-stand",protocol:"codex",state:"live",model:"stand-a",pickHarness:"codex"}};
    };
    switchChat=(sid)=>paintChat("demo",sid,{sections:[]},[]);
  });
  await page.route("**/stand-state",async route=>{await stateGate;await route.fulfill({json:{}});});
  let releaseHistory;
  const historyGate=new Promise(resolve=>releaseHistory=resolve);
  await page.route("**/api/projects/demo/sessions/as-stand**",async route=>{
    await historyGate;
    const items=[{role:"user",text:"первая видимая фраза",wire:"первая видимая фраза",key:"app:0",seq:0,time:new Date().toISOString()},
      {role:"assistant",text:"готовый ответ",key:"app:1",seq:1,time:new Date().toISOString()}];
    if(route.request().url().includes("stream=1")) {
      await route.fulfill({contentType:"text/event-stream",body:items.map(it=>"data: "+JSON.stringify(it)+"\n\n").join("")});
    } else await route.fulfill({json:{session:"as-stand",head:{id:"as-stand"},items,total:2,start:true}});
  });
  await ta.fill("первая видимая фраза");
  await send.click();
  await page.waitForFunction(()=>document.querySelector("#cpin .cslot:not(.off) .m-local")?.textContent.includes("первая видимая фраза"));
  assert.equal(await page.locator("#cpin .cslot:not(.off) .busyrow:not([hidden])").count(),0);
  releaseStart();
  await page.waitForFunction(()=>document.getElementById("cpanel").classList.contains("cload"));
  assert((await page.locator("#cpin").innerText()).includes("первая видимая фраза"),"реплика пропала на переходе");
  releaseState();
  await page.waitForFunction(()=>document.querySelector("#cpin .cslot:not(.off) textarea")?.value==="");
  assert((await page.locator("#cpin").innerText()).includes("первая видимая фраза"),"реплика пропала до истории");
  releaseHistory();
  await page.getByText("готовый ответ",{exact:true}).waitFor();
  assert.equal((await page.locator("#cpin").innerText()).split("первая видимая фраза").length-1,1);
  assert.equal(firstBody.effort,"high");
  assert.equal(await page.evaluate(()=>chatEffortOf("demo","blank-stand")),"");
  assert.equal(await page.evaluate(()=>toastCalls.length),0,"служебные уведомления запуска");
  assert((await geometry()).height<180,"после отправки не сброшена ручная высота");
  assert.equal(await page.locator("#cpin .cslot:not(.off) .cdsel").isDisabled(),false,"Codex заблокирован как внешняя сессия");
  // Ошибка обычной отправки остаётся на своей реплике; успешная не всплывает.
  let failSend=true;
  await page.route("**/api/projects/demo/chats/as-stand/say",async route=>{
    await route.fulfill({status:failSend?409:200,json:failSend?{refused:"проверочная причина"}:{way:"appserver",message:"реплика ушла клиенту"}});
  });
  await ta.fill("неудачная реплика");await send.click();
  await page.waitForFunction(()=>document.querySelector("#cpin .cslot:not(.off) .m-local .mm")?.title.includes("проверочная причина"));
  assert((await page.locator("#cpin").innerText()).includes("неудачная реплика"));
  failSend=false;
  await page.evaluate(()=>toastCalls=[]);
  await ta.fill("следующая реплика");await send.click();
  await page.waitForFunction(()=>!document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
  assert.equal(await page.evaluate(()=>toastCalls.length),0,"подтверждение обычной отправки");
  // Задача без основного чата: одна кнопка, пустой текст допустим, открытие не запускает работу.
  const runs=[];
  await page.route("**/api/projects/demo/runs",async route=>{
    runs.push(route.request().postDataJSON());
    await route.fulfill({json:{way:"appserver",session:"as-task",wire:"Выполни XR-9\n\nРеплика человека:\nпервая для задачи"}});
  });
  for(const text of ["","первая для задачи"]) {
    await page.evaluate(() => {
      switchChat = sid => mount({ addr: sid, sid, task: "XR-9", blank: "", fresh: false,
        entry: { id: sid, protocol: "codex", state: "live", model: "stand-a", pickHarness: "codex" } });
      mount({ addr: "XR-9", blank: "", fresh: false, task: "XR-9", unassigned: true, entry: null });
    });
    assert.equal(await ta.isDisabled(),false);
    assert.equal(await send.innerText(),"Начать работу");
    assert(!(await page.locator("#cpin").innerText()).includes("нет назначенного разговора"));
    const count=runs.length;
    await ta.fill(text);assert.equal(runs.length,count);
    await send.click();
    await page.waitForFunction(()=>document.querySelector("#cpin .cslot:not(.off) .csend")?.textContent==="Отправить");
    assert.equal(runs.length,count+1);
    assert.equal(runs.at(-1).text,text);
  }
  // Один и тот же открытый поповер обновляется при переходах плана.
  const plans = await page.evaluate(() => {
    const pop=ringPop("demo",{plan:[]}); document.body.append(pop);
    const empty=pop.textContent;
    const plan=[{text:"Факты",state:"completed"},{text:"Правка",state:"in_progress"},{text:"Проверка",state:"pending"}];
    fillPop(pop,"demo",{plan});
    const first={text:pop.textContent,number:ringNumber({},plan),current:pop.querySelector(".p-in_progress .ptext2")?.textContent};
    plan[1].state="completed";plan[2].state="in_progress";
    fillPop(pop,"demo",{plan});
    const next={text:pop.textContent,number:ringNumber({},plan)};
    pop.remove();return {empty,first,next};
  });
  assert(plans.empty.includes("Плана пока нет"));
  assert.equal(plans.first.number,"1/3");
  assert.equal(plans.first.current,"Правка");
  assert(plans.first.text.indexOf("Факты")<plans.first.text.indexOf("Правка"));
  assert.equal(plans.next.number,"2/3");
  assert.equal(errors.length,0,errors.join("\n"));
  console.log("DK-1741 Chromium: рост/сжатие/предел/ширина/клавиатура, черновик/high, скриншоты, первая отправка с двумя задержками, ноль служебных карточек, запуск задачи с текстом и без текста: OK");
} finally { await browser.close(); }

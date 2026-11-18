import { makeSandbox, settle, byClass, allByClass, tag, deepBtn, appPathArg }
  from "./poc_dom.mjs";

const failures = [];
const check = (ok, message) => { if (!ok) failures.push(message); };
const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
const triggers = ["click", "enter", "ctrl-enter", "cmd-enter"];

function composer(way, bound, trigger) {
  const requests = [];
  const addr = way === "new" ? "blank-probe" : "aaaa1111-1111";
  const goal = way === "goal" ? "XR-100" : "";
  const { sandbox, store } = makeSandbox(appPathArg(), (path, init) => {
    if (init?.method === "POST") {
      const body = JSON.parse(init.body);
      requests.push({ path, body });
      if (path.endsWith("/shot")) return { path: "/tmp/" + addr + "/one.png", bytes: 100 };
      // Проверяем настоящий запрос подъёма; новый процесс стенду не нужен.
      if (path.endsWith("/chats")) return { unknown: true, error: "исход запуска не подтверждён" };
      if (path.includes("/goals/")) return { id: goal, line: "входящее", message: "отправлено" };
      return { way: "appserver" };
    }
    if (path.includes("/goals/")) return { id: goal, pending: [], delivered: [], live: true };
    if (path.includes("/sessions/")) return { session: addr, head: { id: addr }, items: [], total: 0 };
    if (path.endsWith("/ask")) return { ask: null };
    return {};
  });
  const entry = { id: addr, project: "demo", state: way === "say" || goal ? "live" : "dead",
    idle: true, model: "opus" };
  const st = { project: "demo", addr, task: goal || (bound ? "XR-1" : ""), goal,
    sid: way === "new" ? "" : addr, blank: way === "new" ? addr : "",
    fresh: way === "new", entry, chats: [], models: [] };
  sandbox.location.hash = "#demo/chat/" + addr;
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  const send = deepBtn(panel, "Отправить");
  const fire = () => {
    if (trigger === "click") send.handlers.click({});
    else ta.handlers.keydown({ key: "Enter", ctrlKey: trigger === "ctrl-enter",
      metaKey: trigger === "cmd-enter", preventDefault() {} });
  };
  const paste = () => ta.handlers.paste({ clipboardData: {
    items: [{ type: "image/png", getAsFile: () => ({ dataURL: png }) }],
  }, preventDefault() {} });
  const clips = () => allByClass(byClass(panel, "cclips"), "cclip");
  const deliveries = () => requests.filter((r) => r.path.endsWith("/chats") || r.path.endsWith("/say"));
  const routeIsOwn = (r) => way === "new"
    ? r.path.endsWith("/chats") && r.body.chat === addr && r.body.id === (bound ? "XR-1" : undefined)
    : r.path.endsWith("/" + addr + "/say") && Boolean(r.body.msg_id);
  return { ta, send, fire, paste, clips, requests, deliveries, routeIsOwn, addr, goal, store };
}

// Реальные paste и send-обработчики chatPanel: PNG сначала кладётся /shot,
// затем один и тот же fire обслуживает кнопку и три способа нажать Enter.
for (const payload of ["image", "text"]) {
  for (const bound of [false, true]) {
    for (const way of ["new", "say", "resume"]) {
      for (const trigger of triggers) {
        const label = [payload, bound ? "task" : "ordinary", way, trigger].join("/");
        const c = composer(way, bound, trigger);
        await settle();
        c.fire();
        await settle();
        check(c.deliveries().length === 0, label + ": пустая реплика ушла");
        c.ta.value = "   ";
        c.fire();
        await settle();
        check(c.deliveries().length === 0, label + ": пробельная реплика ушла");
        c.ta.value = "";
        if (payload === "text") {
          c.ta.value = "обычная реплика";
          c.fire();
          await settle();
          const sent = c.deliveries();
          check(sent.length === 1 && sent[0].body.text === "обычная реплика" && c.routeIsOwn(sent[0]),
            label + ": обычный текст или его адрес изменился");
          continue;
        }
        c.paste();
        await settle();
        check(c.requests.filter((r) => r.path.endsWith("/shot")).length === 1,
          label + ": PNG не загрузился");
        check(c.clips().length === 1, label + ": загруженного PNG нет в поле ввода");
        c.send.disabled = true;
        c.fire();
        await settle();
        check(c.deliveries().length === 0, label + ": блокировка отправки снята");
        c.send.disabled = false;
        c.fire();
        await settle();
        const sent = c.deliveries();
        check(sent.length === 1, label + ": снимок без текста не создал одну доставку");
        if (sent.length) {
          const want = '<screenshot file="/tmp/' + c.addr + '/one.png">\nвставлен снимок экрана\n</screenshot>\n';
          check(sent[0].body.text === want, label + ": провод снимка изменился: " + JSON.stringify(sent[0].body.text));
          check(c.routeIsOwn(sent[0]), label + ": чужой адрес или потерян ключ доставки");
          check(c.clips().length === 0, label + ": отправленный снимок остался в поле ввода");
        }
      }
    }
  }
}

// Очередь цели имеет текстовый контракт и обходит post: вложение само по
// себе не должно создавать пустую запись ни на сервере, ни в персисте.
for (const trigger of triggers) {
  const label = "goal/" + trigger;
  const c = composer("goal", true, trigger);
  await settle();
  c.paste();
  await settle();
  check(c.requests.filter((r) => r.path.endsWith("/shot")).length === 1, label + ": PNG не загрузился");
  c.fire();
  await settle();
  const inbox = () => c.requests.filter((r) => r.path.includes("/goals/") && r.path.endsWith("/message"));
  check(inbox().length === 0, label + ": снимок создал пустое сообщение цели");
  check(JSON.parse(c.store.get("devkit.chat.sent.demo/" + c.goal) || "[]").length === 0,
    label + ": пустая запись попала в персист очереди");
  check(c.clips().length === 1, label + ": неотправленное вложение пропало");
  c.ta.value = "обычная реплика цели";
  c.fire();
  await settle();
  check(inbox().length === 1 && inbox()[0].body.text === "обычная реплика цели",
    label + ": текстовая реплика цели изменилась");
}
if (failures.length) {
  console.error(failures.join("\n"));
  process.exit(1);
}
console.log("composer: снимок без текста отправляется в new/say/resume; пустой ввод, disabled и очередь цели защищены");

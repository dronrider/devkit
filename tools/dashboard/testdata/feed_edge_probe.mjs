// Замерщик абсолютного края записей ленты живым браузером.
//
// Стенд poc_feedfit.mjs считает края по объявлениям style.css, и этого хватает
// для сторожа, но глаз человека видит другое: настоящую координату на экране,
// со значками, полями flex и всем, чего в объявлениях не прочесть. Расхождение
// один раз уже стоило круга («отступ блоков в чате по-прежнему разный» при
// сошедшихся числах стенда), поэтому приёмка меряется этим замерщиком, а не
// стендом.
//
// Меряется живая лента выкаченного экземпляра: браузер поднимается headless,
// входит ключом входа, открывает разговор, подтягивает раннее прокруткой вверх и
// отдаёт по каждой записи её вид, левый край видимой коробки и левый край
// первых чернил (текста или значка).
//
// Как звать:
//   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
//     --headless=new --disable-gpu --remote-debugging-port=9333 \
//     --user-data-dir=<временный каталог> about:blank &
//   node testdata/feed_edge_probe.mjs <вход дашборда> <ширина>
//
// Разговор и адрес зашиты в замерщик: он инструмент приёмки, а не стенд.

// Зовётся: node measure.mjs <токен> <ширина>
const enter = process.argv[2];
const WIDE = Number(process.argv[3] || 1440);
const PORT = 9333;

const list = async () => (await (await fetch(`http://127.0.0.1:${PORT}/json`)).json());

const sock = async (url) => {
  const ws = new WebSocket(url);
  await new Promise((ok, no) => { ws.onopen = ok; ws.onerror = no; });
  let id = 0;
  const waits = new Map();
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && waits.has(msg.id)) { waits.get(msg.id)(msg); waits.delete(msg.id); }
  };
  return {
    send: (method, params) => new Promise((ok) => {
      id += 1;
      waits.set(id, ok);
      ws.send(JSON.stringify({ id, method, params: params || {} }));
    }),
    close: () => ws.close(),
  };
};

const tabs = await list();
const page = tabs.find((t) => t.type === 'page');
const cdp = await sock(page.webSocketDebuggerUrl);
await cdp.send('Page.enable');
await cdp.send('Runtime.enable');
await cdp.send('Emulation.setDeviceMetricsOverride',
  { width: WIDE, height: 900, deviceScaleFactor: 1, mobile: WIDE < 700 });

const go = async (url) => {
  await cdp.send('Page.navigate', { url });
  await new Promise((r) => setTimeout(r, 1500));
};
const run = async (expr) => {
  const r = await cdp.send('Runtime.evaluate',
    { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.result && r.result.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails));
  return r.result && r.result.result && r.result.result.value;
};

await go('http://127.0.0.1:7131/login');
await run(`fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:${JSON.stringify(enter)}})}).then(r=>r.ok)`);
await go('http://127.0.0.1:7131/#devkit/chat/8257b5e0-a1a7-424c-a334-d9e7d23bccf5');
await new Promise((r) => setTimeout(r, 5000));

// Лента показывает окно, и виды за его краем в замер не попадают. Прокрутка
// вверх подтягивает раннее, как это делает человек.
for (let i = 0; i < 6; i++) {
  await run(`(() => { const f = document.querySelector('.chatfeed'); if (f) f.scrollTop = 0; return f ? f.scrollHeight : 0; })()`);
  await new Promise((r) => setTimeout(r, 1200));
}

const got = await run(`(() => {
  const feed = document.querySelector('.chatfeed');
  if (!feed) return { err: 'ленты нет' };
  const rows = Array.from(feed.querySelectorAll('.frow'));
  const out = [];
  // Левее всего то, что человек видит первым: знак текста или значок строки.
  const textLeft = (node) => {
    let best = null;
    let who = '';
    const eat = (r, path, said) => {
      if (!r || (!r.width && !r.height)) return;
      if (best === null || r.left < best) { best = r.left; who = path + ' | ' + String(said).slice(0, 20); }
    };
    const walk = (n, path) => {
      for (const kid of n.childNodes) {
        if (kid.nodeType === 3) {
          if (!kid.textContent.trim()) continue;
          // Пункт списка слева отмечен маркером, а он рисуется в поле списка и
          // текстовым узлом не бывает: краем тут считается сам список.
          if (/>(ol|ul)\./.test(path)) continue;
          const rng = document.createRange();
          rng.selectNodeContents(kid);
          eat(rng.getBoundingClientRect(), path, kid.textContent.trim());
          continue;
        }
        if (kid.nodeType !== 1) continue;
        const st = getComputedStyle(kid);
        if (st.display === 'none' || st.visibility === 'hidden') continue;
        const way = path + '>' + kid.tagName.toLowerCase() + '.' + (kid.className || '');
        const tag = kid.tagName.toLowerCase();
        if (tag === 'ol' || tag === 'ul') {
          // Список рисует маркеры в своём поле: левее них он ничего не ставит.
          eat(kid.getBoundingClientRect(), way, '(список)');
        }
        if (!kid.children.length && !kid.textContent.trim()) {
          const r = kid.getBoundingClientRect();
          const up = kid.parentNode.getBoundingClientRect();
          // Полоса во всю ширину родителя это фон, а не знак: она заливает
          // коробку, и краем записи её считать нечего.
          if (r.width < up.width * 0.9) eat(r, way, '(значок)');
        }
        walk(kid, way);
      }
    };
    walk(node, '');
    return { left: best, who };
  };
  const probe = [];
  for (const row of rows) {
    const cls = row.className;
    if (!/sub/.test(cls)) continue;
    const body = row.querySelector('.frowb');
    if (!body) continue;
    const walk = (n, path) => {
      for (const kid of n.childNodes) {
        if (kid.nodeType === 3) {
          if (!kid.textContent.trim()) continue;
          const rng = document.createRange();
          rng.selectNodeContents(kid);
          const r = rng.getBoundingClientRect();
          if (!r.width && !r.height) continue;
          probe.push({ cls, path, left: Math.round(r.left), said: kid.textContent.trim().slice(0, 24) });
          continue;
        }
        if (kid.nodeType !== 1) continue;
        const st = getComputedStyle(kid);
        if (st.display === 'none') continue;
        const way = path + '>' + kid.tagName.toLowerCase() + '.' + (kid.className || '');
        if (!kid.children.length && !kid.textContent.trim()) {
          const r = kid.getBoundingClientRect();
          if (r.width || r.height) probe.push({ cls, path: way, left: Math.round(r.left), said: '(значок)' });
        }
        walk(kid, way);
      }
    };
    walk(body, '');
    if (probe.length > 400) break;
  }
  for (const row of rows) {
    const body = row.querySelector('.frowb');
    if (!body) continue;
    const node = Array.from(body.children).find((n) => getComputedStyle(n).display !== 'none');
    if (!node) continue;
    // Видимая коробка: ближайший потомок с рамкой или фоном.
    let box = null;
    const seek = (n) => {
      if (box) return;
      const st = getComputedStyle(n);
      const bg = st.backgroundColor;
      const bord = parseFloat(st.borderLeftWidth) || 0;
      const solid = bg && bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent';
      if (solid || bord > 0) { box = n; return; }
      for (const kid of n.children) seek(kid);
    };
    seek(node);
    out.push({
      cls: row.className,
      box: box ? Math.round(box.getBoundingClientRect().left * 10) / 10 : null,
      text: (() => { const v = textLeft(node); return v.left === null ? null : Math.round(v.left * 10) / 10; })(),
      who: textLeft(node).who,
    });
  }
  return { probe, rows: out, feed: Math.round(feed.getBoundingClientRect().left * 10) / 10 };
})()`);
console.log(JSON.stringify(got, null, 1));
cdp.close();

// Замер строки активности чата живым браузером (DK-1586).
// Зовётся: node testdata/busyrow_probe.mjs [путь к chrome] [ширина] [путь к style.css]
// Печатает JSON с рамками строки, поля ввода и записи ленты и с положением
// фона по фазам блеска. Код возврата 1, если края разошлись, полоса идёт
// справа налево или фон уходит из слова.

import {spawn} from 'node:child_process';
import {mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const chrome = process.argv[2] || process.env.DASHBOARD_CHROME ||
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const width = Number(process.argv[3] || 440);
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(process.argv[4] || join(here, '..', 'static', 'style.css')), 'utf8');
const port = 9300 + Math.floor(Math.random() * 600);
const profile = mkdtempSync(join(tmpdir(), 'busyrow-probe-'));
const proc = spawn(chrome, [
  '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
  `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, 'about:blank',
], {stdio: 'ignore'});

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const waitPort = async () => {
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/json/list`);
      if (res.ok) return (await res.json()).find((t) => t.type === 'page');
    } catch {}
    await sleep(100);
  }
  throw new Error('браузер не поднялся на порту ' + port);
};

const connect = async (url) => {
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
      ws.send(JSON.stringify({id, method, params: params || {}}));
    }),
    close: () => ws.close(),
  };
};

const html = `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<style>${css}</style>
<style>html,body{margin:0;background:var(--bg0)}.stage{width:${width - 20}px}</style>
</head><body><div class="stage"><div class="chatwrap">
<div class="msgs mlist">
  <div class="frow f-line"><div class="frowb"><div class="msg"><div class="msaid">запись ленты</div></div></div></div>
</div>
<div class="busyrow" data-cat="think">
  <span class="bicon"></span>
  <span class="bword">читает вывод</span>
  <span class="bmeta"><span class="bclock">0:12</span><span class="bsep">,</span><span class="btok">1,2k ток</span></span>
</div>
<div class="cbox"><textarea rows="2" placeholder="написать"></textarea></div>
</div></div></body></html>`;

let cdp;
try {
  const page = await waitPort();
  cdp = await connect(page.webSocketDebuggerUrl);
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Emulation.setDeviceMetricsOverride',
    {width, height: 900, deviceScaleFactor: 1, mobile: false});
  const tree = await cdp.send('Page.getFrameTree');
  await cdp.send('Page.setDocumentContent', {frameId: tree.result.frameTree.frame.id, html});
  await sleep(300);
  const expr = `(async () => {
    const q = (s) => document.querySelector(s);
    const rect = (s) => {
      const b = q(s).getBoundingClientRect();
      return {left: +b.left.toFixed(2), right: +b.right.toFixed(2), width: +b.width.toFixed(2)};
    };
    const word = q('.bword');
    const anim = word.getAnimations().find((a) => a.animationName === 'bshine');
    const phases = [0, 0.25, 0.5, 0.75];
    const positions = [];
    anim.pause();
    for (const p of phases) {
      anim.currentTime = 3200 * p;
      await new Promise((r) => requestAnimationFrame(r));
      positions.push(getComputedStyle(word).backgroundPosition.split(' ')[0]);
    }
    const cs = getComputedStyle(word);
    return {
      edges: {row: rect('.busyrow'), cbox: rect('.cbox'), frow: rect('.frowb')},
      phases, positions,
      size: cs.backgroundSize, opacity: cs.opacity, animation: cs.animationName,
    };
  })()`;
  const got = await cdp.send('Runtime.evaluate',
    {expression: expr, awaitPromise: true, returnByValue: true});
  const m = got.result.result.value;
  const problems = [];
  const left = (k) => m.edges[k].left;
  const right = (k) => m.edges[k].right;
  if (Math.abs(left('row') - left('cbox')) > 0.5 || Math.abs(left('row') - left('frow')) > 0.5) {
    problems.push(`левый край: строка ${left('row')}, поле ввода ${left('cbox')}, лента ${left('frow')}`);
  }
  if (Math.abs(right('row') - right('cbox')) > 0.5 || Math.abs(right('row') - right('frow')) > 0.5) {
    problems.push(`правый край: строка ${right('row')}, поле ввода ${right('cbox')}, лента ${right('frow')}`);
  }
  const pos = m.positions.map((v) => parseFloat(v));
  if (pos.some((v, i) => Math.abs(v - [100, 75, 50, 25][i]) > 1)) {
    problems.push(`положение фона по фазам ${m.positions}, ожидалось 100, 75, 50, 25 процентов`);
  }
  if (pos.some((v, i) => i > 0 && v >= pos[i - 1])) {
    problems.push(`полоса не идёт слева направо: ${m.positions}`);
  }
  const size = parseFloat(m.size);
  for (const v of pos) {
    const off = (1 - size / 100) * v / 100;
    if (off > 0.001 || off + size / 100 < 0.999) {
      problems.push(`фон ${m.size} при положении ${v}% не накрывает слово`);
    }
  }
  if (m.opacity !== '1') problems.push(`прозрачность слова ${m.opacity}, ожидалась 1`);
  console.log(JSON.stringify({width, ok: problems.length === 0, problems, ...m}, null, 1));
  if (problems.length) process.exitCode = 1;
} catch (e) {
  console.error(String(e));
  process.exitCode = 1;
} finally {
  if (cdp) cdp.close();
  proc.kill();
  await sleep(200);
  rmSync(profile, {recursive: true, force: true});
}

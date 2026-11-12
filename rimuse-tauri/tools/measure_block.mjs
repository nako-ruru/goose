// Prove (or refute) that the shell's main thread is blocked while the backend
// respawns. Method: kill the owned backend, then fire TWO invokes in parallel:
//   get_acp_url      -> must spawn + poll until ready (slow, expected)
//   get_app_config   -> reads an env var, should return in <10ms
// If get_app_config comes back only AFTER get_acp_url, both ran on the same
// thread => the main thread is blocked => the UI (window dragging) freezes.
import fs from 'node:fs';
import { execSync } from 'node:child_process';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const run = async () => {
  // 0) kill the owned backend so get_acp_url has real work to do
  let killed = 'none';
  try {
    const raw = fs.readFileSync(`${process.env.LOCALAPPDATA}\\rimuse\\child.pid`, 'utf8').trim();
    const pid = Number(raw.split(/\s+/)[0]);
    if (pid) {
      execSync(`taskkill /pid ${pid} /f /t`, { stdio: 'ignore' });
      killed = String(pid);
    }
  } catch (e) {
    killed = 'err:' + (e.message || e);
  }
  console.log('killed backend pid:', killed);
  await sleep(400); // let the reaper notice

  // 1) connect CDP
  const list = await (await fetch('http://127.0.0.1:9224/json')).json();
  const page = list.find((t) => t.type === 'page' && t.url.includes('tauri.localhost'));
  if (!page) { console.log('NO_PAGE'); process.exit(1); }
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  let id = 0;
  const p = new Map();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && p.has(m.id)) { p.get(m.id)(m); p.delete(m.id); }
  };
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = () => j(new Error('ws failed')); });
  const send = (m, pr = {}) => new Promise((r) => { const i = ++id; p.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: pr })); });

  // 2) fire both at once from the page and timestamp each completion
  const expr = `(() => {
    const t0 = performance.now();
    const a = window.__TAURI_INTERNALS__.invoke('get_acp_url')
      .then(v => ({ which: 'get_acp_url', ms: Math.round(performance.now() - t0), v: String(v).slice(0,90) }))
      .catch(e => ({ which: 'get_acp_url', ms: Math.round(performance.now() - t0), e: String(e).slice(0,150) }));
    const b = window.__TAURI_INTERNALS__.invoke('get_app_config')
      .then(v => ({ which: 'get_app_config', ms: Math.round(performance.now() - t0), v: JSON.stringify(v).slice(0,90) }))
      .catch(e => ({ which: 'get_app_config', ms: Math.round(performance.now() - t0), e: String(e).slice(0,150) }));
    return Promise.all([a, b]);
  })()`;

  const t0 = Date.now();
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  const wall = Date.now() - t0;

  const results = r.result?.result?.value;
  if (!Array.isArray(results)) {
    console.log('UNEXPECTED:', JSON.stringify(r.result?.exceptionDetails || r.result?.result).slice(0, 400));
    process.exit(1);
  }
  console.log('---');
  for (const x of results) console.log(`  ${x.which}: ${x.ms}ms  ${x.v || x.e || ''}`);
  const acp = results.find((x) => x.which === 'get_acp_url');
  const cfg = results.find((x) => x.which === 'get_app_config');
  console.log(`wall clock: ${wall}ms`);

  // Verdict: get_app_config is trivial (env read). If it took nearly as long
  // as the respawn, it was queued behind it on one thread.
  const blocked = cfg.ms >= Math.max(150, acp.ms * 0.7);
  console.log('---');
  if (blocked) {
    console.log(`MAIN THREAD BLOCKED: get_app_config waited ${cfg.ms}ms behind a ${acp.ms}ms respawn`);
    console.log('=> sync get_acp_url runs its readiness poll on the main thread; UI freezes.');
    process.exit(2);
  }
  console.log(`NO BLOCK: get_app_config returned in ${cfg.ms}ms while get_acp_url took ${acp.ms}ms`);
  process.exit(0);
};
run().catch((e) => { console.log('FATAL:', e.message || e); process.exit(1); });

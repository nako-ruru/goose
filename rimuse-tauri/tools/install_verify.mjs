// CDP verification for a REAL installed copy (not the dev exe).
// Called by test_install.py after the installer has run; expects the app to
// be up with --remote-debugging-port=9224 (baked into tauri.conf, so the
// installed build keeps it).
import fs from 'node:fs';

const OUT = process.argv[2] || 'D:\\temp\\opencode\\install_verify.png';

const race = (expr, ms) =>
  `Promise.race([${expr}.then(v => 'OK ' + JSON.stringify(v)).catch(e => 'ERR ' + String(e).slice(0, 250)), new Promise(r => setTimeout(() => r('TIMEOUT_${ms}'), ${ms}))])`;

const run = async () => {
  // CDP can take a few seconds to come up after launch
  let list = null;
  for (let i = 0; i < 30; i++) {
    try {
      list = await (await fetch('http://127.0.0.1:9224/json')).json();
      if (list.some((t) => t.type === 'page')) break;
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 1000));
  }
  const page = (list || []).find((t) => t.type === 'page' && t.url.includes('tauri.localhost'));
  if (!page) { console.log('FAIL: no page on CDP9224'); process.exit(1); }
  console.log('page:', page.url.slice(0, 90));

  const ws = new WebSocket(page.webSocketDebuggerUrl);
  let id = 0;
  const p = new Map();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && p.has(m.id)) { p.get(m.id)(m); p.delete(m.id); }
  };
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = () => j(new Error('ws failed')); });
  const send = (m, pr = {}) => new Promise((r) => { const i = ++id; p.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: pr })); });
  const ev = async (expression) => {
    const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (r.result?.exceptionDetails) return 'EVAL_EX: ' + JSON.stringify(r.result.exceptionDetails.exception?.description || r.result.exceptionDetails.text).slice(0, 250);
    return r.result?.result?.value;
  };
  await send('Runtime.enable');
  await send('Page.enable');

  const results = {};

  //1) shell-owned backend (no env vars in an installed run)
  results.acp = await ev(race(`window.__TAURI_INTERNALS__.invoke('get_acp_url')`, 30000));
  console.log('1 get_acp_url:', results.acp);

  //2) per-run secret
  results.secret = await ev(race(`window.__TAURI_INTERNALS__.invoke('get_secret_key')`, 15000));
  console.log('2 get_secret_key:', results.secret);

  //3) does the ACP endpoint actually answer? (proves the spawned backend works)
  let acpOk = false;
  try {
    const u = JSON.parse(String(results.acp).replace(/^OK /, ''));
    if (typeof u === 'string' && u.startsWith('ws://')) {
      const res = await ev(race(`(async () => {
        try {
          const ws = new WebSocket(${JSON.stringify(u)});
          return await new Promise(res => {
            const t = setTimeout(() => res('TIMEOUT'), 6000);
            ws.onopen = () => { clearTimeout(t); res('WS_OPEN'); ws.close(); };
            ws.onerror = () => { clearTimeout(t); res('WS_ERROR'); };
          });
        } catch (e) { return 'THROW ' + (e.message || e); }
      })()`, 10000));
      results.acpWs = res;
      acpOk = String(res).includes('WS_OPEN');
    }
  } catch { /* acp parse failed */ }
  console.log('3 acp ws:', results.acpWs || '(skipped)');

  //4) page actually rendered data (not a blank shell)
  await send('Page.reload');
  await new Promise((r) => setTimeout(r, 10000));
  results.page = await ev(`JSON.stringify({hash: location.hash.slice(0,40), hasSidebar: !!document.body.innerText.match(/新对话|会话历史/), len: document.body.innerText.length})`);
  console.log('4 page:', results.page);

  //5) screenshot as visual evidence
  try {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(OUT, Buffer.from(shot.result.data, 'base64'));
    console.log('5 screenshot:', OUT);
    results.shot = true;
  } catch (e) {
    console.log('5 screenshot FAILED:', e.message);
    results.shot = false;
  }

  const secretOk = String(results.secret).includes('"len":64') || /OK.*[0-9a-f]{64}/.test(String(results.secret));
  const acpIsOwned = /127\.0\.0\.1:\d+\/acp\?token=[0-9a-f]{64}/.test(String(results.acp));
  const pageOk = String(results.page).includes('"hasSidebar":true');
  const pass = acpIsOwned && secretOk && acpOk && pageOk && results.shot;
  console.log('---');
  console.log(`ownedBackend=${acpIsOwned} secret64=${secretOk} acpHandshake=${acpOk} pageRendered=${pageOk} shot=${results.shot}`);
  console.log(pass ? 'INSTALL VERIFY PASS' : 'INSTALL VERIFY FAIL');

  ws.close();
  process.exit(pass ? 0 : 1);
};
run().catch((e) => { console.log('FATAL:', e.message || e); process.exit(1); });

// Invoke a Tauri command over CDP. Usage:
//   node invoke_cmd.mjs <command> [jsonArgs] [--nowait]
// --nowait: fire and forget (for the modal folder dialog, which blocks the
// main thread until the user dismisses it, so awaiting would hang forever).
const [, , command, jsonArgs = '{}', ...flags] = process.argv;
const nowait = flags.includes('--nowait');
const args = JSON.parse(jsonArgs);

const run = async () => {
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

  // Build the invoke expression; always bound it so a modal dialog can't hang us.
  const expr = nowait
    ? `window.__TAURI_INTERNALS__.invoke(${JSON.stringify(command)}, ${JSON.stringify(args)})
         .then(v => { window.__lastInvoke = {ok: true, v}; })
         .catch(e => { window.__lastInvoke = {ok: false, e: String(e).slice(0,300)}; });
       'FIRED'`
    : `Promise.race([
         window.__TAURI_INTERNALS__.invoke(${JSON.stringify(command)}, ${JSON.stringify(args)})
           .then(v => 'OK ' + JSON.stringify(v))
           .catch(e => 'ERR ' + String(e).slice(0, 300)),
         new Promise(r => setTimeout(() => r('TIMEOUT_15S'), 15000)),
       ])`;

  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: !nowait });
  if (r.result?.exceptionDetails) {
    console.log('EVAL_EX: ' + JSON.stringify(r.result.exceptionDetails.exception?.description || r.result.exceptionDetails.text).slice(0, 300));
  } else {
    console.log(String(r.result?.result?.value));
  }
  ws.close();
  process.exit(0);
};
run().catch((e) => { console.log('FATAL:', e.message || e); process.exit(1); });

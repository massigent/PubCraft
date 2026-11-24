'use strict';
// Edge case del modulo di igiene `cleanup.cjs` (nessun browser, nessun server):
//  - stopGui su un figlio già uscito: no-op rapida, nessun errore;
//  - stopGui chiamato due volte / concorrentemente sullo stesso figlio:
//    entrambe le attese risolvono e il figlio muore una volta sola;
//  - trackGui di un figlio che esce subito: nessun leak nel registro;
//  - un SIGINT verso il processo di test stesso chiude comunque i figli
//    tracciati (rete di sicurezza sui segnali);
//  - un figlio che esce normalmente viene rimosso dal registro e stopGui NON
//    emette alcuna chiamata process.kill (verificato con uno spy temporaneo).

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const path = require('node:path');
const { trackGui, stopGui, alive, tracked, KILL_GRACE_MS } = require('./cleanup.cjs');

function pidAlive(pid) {
  if (!pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (_) {
    return false;
  }
}

const sleepForever = () => spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });

// Attende che un figlio esca; non usa attese fisse sulla durata del figlio.
function exited(proc) {
  if (!alive(proc)) return Promise.resolve();
  return new Promise(resolve => proc.once('exit', resolve));
}

// 1. stopGui su figlio già uscito: risolve subito, senza errori.
async function testStopGuiOnExitedChild() {
  const child = trackGui(sleepForever());
  child.kill('SIGKILL');
  await exited(child);
  assert.ok(!alive(child), 'il figlio deve essere già uscito');
  const t0 = Date.now();
  await stopGui(child);
  assert.ok(Date.now() - t0 < 300, `stopGui su figlio morto deve essere quasi immediata (${Date.now() - t0} ms)`);
}

// 2. Doppia e concorrente stopGui sullo stesso figlio.
async function testStopGuiIdempotent() {
  const child = trackGui(sleepForever());
  await Promise.all([stopGui(child), stopGui(child)]);
  assert.ok(!pidAlive(child.pid), 'il figlio non deve sopravvivere a stopGui doppie');
  await stopGui(child); // terza chiamata a vuoto: nessun errore
}

// 3. trackGui di un figlio che esce subito: nessun registro residuo.
async function testTrackShortLivedChild() {
  const before = tracked.size;
  const child = trackGui(sleepForever());
  child.kill('SIGTERM');
  await exited(child);
  await wait(100); // solo per lasciare girare l'evento 'exit' del registro
  assert.equal(tracked.size, before, 'il figlio breve non deve restare tracciato');
}

// 4. SIGINT verso il processo di test stesso: i figli tracciati vengono chiusi.
async function testSelfSignalKillsTracked() {
  const helper = JSON.stringify(path.join(__dirname, 'cleanup.cjs'));
  const script = `
    const { spawn } = require('node:child_process');
    const { trackGui } = require(${helper});
    const child = trackGui(spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' }));
    process.stdout.write(String(child.pid));
    setTimeout(() => process.kill(process.pid, 'SIGINT'), 200);
  `;
  const out = await new Promise(resolve => {
    const p = spawn(process.execPath, ['-e', script], { stdio: ['ignore', 'pipe', 'ignore'] });
    let buf = '';
    p.stdout.on('data', d => (buf += d.toString()));
    p.on('exit', code => resolve(JSON.stringify({ pid: buf.trim(), code })));
  });
  const { pid, code } = JSON.parse(out);
  assert.ok(Number(pid) > 0, `pid del nipote non letto (${out})`);
  assert.equal(code, 1, 'il processo di test deve uscire 1 dopo SIGINT gestito');
  await wait(300);
  assert.ok(!pidAlive(Number(pid)), `il nipote ${pid} è rimasto orfano dopo il SIGINT al padre`);
}

// 5. Figlio che esce normalmente: nessun SIGKILL postumo, osservato con uno spy
//    su process.kill. Si verifica che: (a) il registro rimuova il figlio;
//    (b) stopGui successiva non emetta alcuna chiamata process.kill.
async function testNormalExitUntouched() {
  const child = trackGui(spawn(process.execPath, ['-e', 'process.exit(0)'], { stdio: 'ignore' }));
  await exited(child);
  assert.ok(!alive(child), 'il figlio breve deve essere uscito da solo');

  const before = tracked.size;
  assert.ok(!tracked.has(child), 'il figlio uscito normalmente non deve restare nel registro');

  // Spy temporaneo su process.kill: ripristinato nel finally, in ogni caso.
  const originalKill = process.kill;
  const killCalls = [];
  process.kill = function spyKill(pid, signal) {
    killCalls.push({ pid, signal });
    return originalKill.call(process, pid, signal);
  };
  try {
    await stopGui(child);
  } finally {
    process.kill = originalKill;
  }
  assert.equal(tracked.size, before, 'stopGui non deve lasciare residui nel registro');
  assert.deepEqual(killCalls, [], `nessun process.kill atteso su figlio uscito normalmente (viste: ${JSON.stringify(killCalls)})`);
}

function wait(ms) {
  return new Promise(r => setTimeout(r, ms));
}

(async () => {
  console.log('===> Edge case igiene dei test (cleanup.cjs)');
  await testStopGuiOnExitedChild();
  console.log('  ✔ stopGui su figlio già uscito: no-op rapida');
  await testStopGuiIdempotent();
  console.log('  ✔ stopGui doppia/concorrente: idempotente');
  await testTrackShortLivedChild();
  console.log('  ✔ figli di vita breve: nessun leak nel registro');
  await testSelfSignalKillsTracked();
  console.log('  ✔ SIGINT al processo di test: figli tracciati chiusi');
  await testNormalExitUntouched();
  console.log('  ✔ uscita normale del figlio: registro pulito e zero process.kill (spy)');
  assert.ok(KILL_GRACE_MS >= 500, 'la tolleranza SIGTERM deve essere realistica');
  console.log('✔ EDGE CASE IGIENE VERIFICATI');
  process.exit(0);
})().catch(err => {
  console.error('FALLITO:', err);
  process.exit(1);
});

'use strict';
// Verifica dell'igiene dei test: un fallimento intenzionale non deve lasciare
// server orfani, la chiusura non deve toccare processi che non appartengono al
// test (browser/istanze dell'utente) e deve escalare quando il figlio ignora
// SIGTERM.
//
// Non usa puppeteer: verifica il comportamento del cleanup, non la UI.

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { trackGui, stopGui, releaseBrowser, alive } = require('./cleanup.cjs');

const wait = ms => new Promise(r => setTimeout(r, ms));

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

// Processo Node unico che ignora SIGTERM: segnala la prontezza via stdout.
// (Nessuna shell: nessun figlio `sleep` non tracciato.)
function stubbornChild() {
  return spawn(process.execPath, ['-e',
    `process.on('SIGTERM', () => {}); // ignora SIGTERM
     process.stdout.write('READY\\n');
     setInterval(() => {}, 1000);`],
    { stdio: ['ignore', 'pipe', 'ignore'] });
}

// Attende che il figlio esca (o che scada la tolleranza).
function exited(proc) {
  if (!alive(proc)) return Promise.resolve();
  return new Promise(resolve => proc.once('exit', resolve));
}

// Attende la riga di prontezza su stdout.
function ready(proc) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('figlio non pronto (timeout readiness)')), 5000);
    const onExit = () => { clearTimeout(t); reject(new Error('figlio uscito prima della readiness')); };
    proc.stdout.once('data', d => {
      clearTimeout(t);
      proc.removeListener('exit', onExit);
      if (String(d).includes('READY')) resolve(); else reject(new Error('readiness inattesa: ' + JSON.stringify(String(d))));
    });
    proc.once('exit', onExit);
  });
}

function freePort() {
  return new Promise(resolve => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const p = srv.address().port;
      srv.close(() => resolve(p));
    });
  });
}

function portOpen(port) {
  return new Promise(resolve => {
    const sock = net.connect({ host: '127.0.0.1', port });
    let settled = false;
    const timer = setTimeout(() => done(false), 1500);
    const done = ok => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      sock.destroy();
      resolve(ok);
    };
    sock.once('connect', () => done(true));
    sock.once('error', () => done(false));
  });
}

// Attende che il pid risulti vivo (poll breve, senza attesa fissa lunga).
async function aliveSoon(proc, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (pidAlive(proc.pid)) return;
    if (!alive(proc) && !pidAlive(proc.pid)) throw new Error('il figlio è uscito subito');
    await wait(25);
  }
  throw new Error('figlio mai diventato vivo');
}

// 1. Fallimento intenzionale: il finally chiude il figlio, nessun orfano.
async function testIntentionalFailureClosesChild() {
  const child = trackGui(sleepForever());
  await aliveSoon(child);
  assert.ok(pidAlive(child.pid), 'il figlio di prova deve essere vivo prima del fallimento');
  try {
    throw new Error('fallimento intenzionale del test');
  } catch (_) {
    await stopGui(child); // equivale al `finally` dei test reali
  }
  assert.ok(!pidAlive(child.pid), 'dopo il fallimento il figlio non deve restare vivo');
}

// 2. Figlio che ignora SIGTERM: escalation a SIGKILL sullo stesso pid.
async function testEscalationOnStubbornChild() {
  const child = trackGui(stubbornChild());
  try {
    await ready(child); // pronto (e SIGTERM-handler registrato) quando lo dice lui
    assert.ok(pidAlive(child.pid), 'il figlio ostinato deve essere vivo');
    const t0 = Date.now();
    await stopGui(child, 600);
    const elapsed = Date.now() - t0;
    assert.ok(!pidAlive(child.pid), 'il figlio che ignora SIGTERM deve essere terminato');
    assert.ok(elapsed >= 500, `stopGui deve attendere la tolleranza prima di escalare (${elapsed} ms)`);
  } finally {
    await stopGui(child, 600); // anche un assert fallito chiude il figlio
  }
}

// 3. Processi non creati dal test (browser/istanze dell'utente) non si toccano.
async function testForeignProcessPreserved() {
  const foreign = sleepForever(); // non tracciato: simula l'istanza dell'utente
  const mine = trackGui(sleepForever());
  // try/finally: anche un assert fallito non deve lasciare né l'uno né l'altro.
  try {
    await Promise.all([aliveSoon(foreign), aliveSoon(mine)]);
    assert.ok(pidAlive(foreign.pid) && pidAlive(mine.pid), 'i due processi devono essere vivi');
    await stopGui(mine);
    assert.ok(!pidAlive(mine.pid), 'il processo del test deve essere chiuso');
    assert.ok(pidAlive(foreign.pid), 'un processo non creato dal test non deve essere terminato');
  } finally {
    await stopGui(mine); // anche un assert fallito chiude il figlio del test
    foreign.kill('SIGKILL'); // pulizia del nostro stesso processo di prova
    await exited(foreign);
  }
}

// 4. Rete di sicurezza: un processo che muore per eccezione non gestita
//    (senza finally) chiude comunque il figlio tracciato prima di uscire.
async function testExitGuardKillsTrackedChild() {
  const helper = JSON.stringify(path.join(__dirname, 'cleanup.cjs'));
  const script = `
    const { spawn } = require('node:child_process');
    const { trackGui } = require(${helper});
    const child = trackGui(spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' }));
    process.stdout.write(String(child.pid));
    setTimeout(() => { throw new Error('eccezione intenzionale non gestita'); }, 300);
  `;
  const out = await new Promise(resolve => {
    const p = spawn(process.execPath, ['-e', script], { stdio: ['ignore', 'pipe', 'ignore'] });
    let buf = '';
    p.stdout.on('data', d => (buf += d.toString()));
    p.on('exit', () => resolve(buf.trim()));
  });
  const childPid = Number(out);
  assert.ok(childPid > 0, `pid del nipote non letto (output: ${JSON.stringify(out)})`);
  await wait(300); // il nipote può essere raccolto da init con un piccolo ritardo
  assert.ok(!pidAlive(childPid), `il nipote ${childPid} è rimasto orfano dopo un\'eccezione non gestita`);
}

// 5. Server reale: SIGTERM onorato anche durante l'avvio.
//    Non si attende che il server sia pronto: qui si verifica la chiusura, non
//    la latenza di avvio (che dipende dal volume/asset). Un server già pronto è
//    coperto dai test browser che lo usano davvero.
async function testRealServerStops() {
  const bin = process.env.PUBCRAFT_GUI_BIN || path.join(__dirname, '..', 'build', 'pubcraftgui');
  if (!fs.existsSync(bin)) {
    console.log('  (saltato) binario GUI assente: ' + bin);
    return 'skipped';
  }
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-orphan-'));
  const vault = path.join(tmp, 'V');
  fs.mkdirSync(vault);
  fs.writeFileSync(path.join(vault, 'book.yaml'),
    'title: Igiene\nlanguage: it\nchapters:\n- file: 01.md\n  title: Uno\n');
  fs.writeFileSync(path.join(vault, '01.md'), 'Testo.\n');
  const port = await freePort();
  let proc = null;
  try {
    proc = trackGui(spawn(bin, [vault], {
      env: { ...process.env, HOME: tmp, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
      stdio: 'ignore',
    }));
    // Si attende che il server sia in ascolto sulla porta (prontezza reale,
    // non attesa fissa sulla latenza di avvio).
    const deadline = Date.now() + 15000;
    while (pidAlive(proc.pid) && !(await portOpen(port))) {
      if (Date.now() > deadline) break;
      await wait(100);
    }
    assert.ok(pidAlive(proc.pid), 'il server GUI di prova deve essere vivo prima della chiusura');
    const t0 = Date.now();
    await stopGui(proc);
    assert.ok(!pidAlive(proc.pid), 'il server deve essere terminato da SIGTERM');
    assert.ok(Date.now() - t0 < 10000, `chiusura troppo lenta: ${Date.now() - t0} ms`);
    assert.ok(!(await portOpen(port)), 'nessun processo deve restare in ascolto sulla porta del server');
  } finally {
    if (proc) await stopGui(proc); // fermare il server PRIMA di rimuovere tmp
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  return 'ok';
}

(async () => {
  console.log('===> Verifica igiene dei test (cleanup, nessun orfano)');
  await testIntentionalFailureClosesChild();
  console.log('  ✔ fallimento intenzionale: nessun figlio residuo');
  await testEscalationOnStubbornChild();
  console.log('  ✔ figlio che ignora SIGTERM: SIGKILL sullo stesso pid');
  await testForeignProcessPreserved();
  console.log('  ✔ processi non creati dal test preservati');
  await testExitGuardKillsTrackedChild();
  console.log('  ✔ eccezione non gestita: il figlio tracciato viene chiuso');
  const real = await testRealServerStops();
  if (real === 'ok') console.log('  ✔ server reale: SIGTERM onorato, porta liberata');
  // il browser condiviso non deve mai essere chiuso: releaseBrowser sconnette e basta
  let disconnected = 0;
  await releaseBrowser({ disconnect: async () => { disconnected++; }, close: async () => { throw new Error('close non deve essere chiamato'); } });
  assert.equal(disconnected, 1, 'releaseBrowser deve disconnettere, non chiudere');
  console.log('  ✔ browser condiviso: solo disconnect');
  console.log('✔ IGIENE DEI TEST VERIFICATA');
  process.exit(0);
})().catch(err => {
  console.error('FALLITO:', err);
  process.exit(1);
});

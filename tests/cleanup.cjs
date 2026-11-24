'use strict';
// Igiene dei test browser di PubCraft.
//
// Regole:
//  - si terminano SOLO i processi creati dal test, per pid (mai pkill/killall,
//    mai per nome: il browser e le istanze dell'utente non vanno toccati);
//  - la chiusura attende l'uscita reale del figlio e, se non risponde a
//    SIGTERM entro la tolleranza, escala a SIGKILL sullo stesso pid;
//  - una rete di sicurezza su 'exit'/segnali/eccezioni non gestite evita che
//    un fallimento prima del `finally` lasci server orfani;
//  - il browser condiviso (CDP) si sconnette soltanto: `browser.close()`
//    chiuderebbe il processo dell'utente.

const KILL_GRACE_MS = 4000; // SIGTERM -> attesa -> SIGKILL
const KILL_FINAL_MS = 2000; // attesa dopo SIGKILL

const tracked = new Set();
let guardsInstalled = false;

function alive(proc) {
  return !!proc && proc.exitCode === null && proc.signalCode === null && !!proc.pid;
}

function forceKill(proc) {
  if (!alive(proc)) return;
  try {
    process.kill(proc.pid, 'SIGKILL');
  } catch (_) {
    /* già terminato o non nostro */
  }
}

function installGuards() {
  if (guardsInstalled) return;
  guardsInstalled = true;
  const flush = () => {
    for (const p of tracked) forceKill(p);
    tracked.clear();
  };
  process.on('exit', flush);
  for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    process.on(sig, () => {
      flush();
      process.exit(1);
    });
  }
  process.on('uncaughtException', err => {
    flush();
    console.error(err);
    process.exit(1);
  });
  process.on('unhandledRejection', err => {
    flush();
    console.error(err);
    process.exit(1);
  });
}

// Registra un processo creato dal test (da chiamare subito dopo spawn).
function trackGui(proc) {
  if (!proc) return proc;
  installGuards();
  tracked.add(proc);
  proc.once('exit', () => tracked.delete(proc));
  return proc;
}

// Chiude un processo creato dal test: SIGTERM, attesa dell'uscita, SIGKILL sullo
// stesso pid se non risponde. Idempotente.
async function stopGui(proc, graceMs = KILL_GRACE_MS) {
  if (!proc) return;
  tracked.add(proc);
  if (!alive(proc)) {
    tracked.delete(proc);
    return;
  }
  const exited = new Promise(resolve => proc.once('exit', resolve));
  try {
    proc.kill('SIGTERM');
  } catch (_) {}
  await Promise.race([exited, new Promise(resolve => setTimeout(resolve, graceMs))]);
  if (alive(proc)) {
    forceKill(proc);
    await Promise.race([exited, new Promise(resolve => setTimeout(resolve, KILL_FINAL_MS))]);
  }
  tracked.delete(proc);
}

// Il browser è condiviso con l'utente: si chiude solo la connessione.
function releaseBrowser(browser) {
  if (!browser) return Promise.resolve();
  return Promise.resolve()
    .then(() => browser.disconnect())
    .catch(() => {});
}

module.exports = { trackGui, stopGui, releaseBrowser, installGuards, forceKill, alive, tracked, KILL_GRACE_MS };

// Regressione: selezionare/importare file in una cartella che è già un libro
// non deve ridurre la lista capitoli né alterare ordine, ID, titoli e tipi.
// Un book.yaml invalido deve essere rifiutato senza modificarlo.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');

const root = path.resolve(__dirname, '..');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

async function freePort() {
  return await new Promise((resolve, reject) => {
    const s = net.createServer();
    s.once('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const p = s.address().port;
      s.close(() => resolve(p));
    });
  });
}

(async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-selection-'));
  const vault = path.join(tmp, 'libro');
  const badVault = path.join(tmp, 'libro-rotto');
  fs.mkdirSync(vault, { recursive: true });
  fs.mkdirSync(badVault, { recursive: true });

  const bookYAML = `title: "Libro Selezione"
author: "Autrice"
language: it
custom_meta: "da preservare"
chapters:
  - file: "01.md"
    id: "legacy-id-uno"
    title: "Uno"
    type: "dedication"
  - file: "02.md"
    id: "legacy-id-due"
    title: "Due"
  - file: "03.md"
    id: "legacy-id-tre"
    title: "Tre"
    type: "epilogue"
`;
  fs.writeFileSync(path.join(vault, 'book.yaml'), bookYAML);
  for (const n of ['01', '02', '03']) {
    fs.writeFileSync(path.join(vault, `${n}.md`), `---\ntitle: Cap ${n}\n---\n\nTesto ${n}.\n`);
  }
  const originalBookYAML = fs.readFileSync(path.join(vault, 'book.yaml'));

  const invalidYAML = 'title: "Rotto"\nchapters:\n\t- file: "01.md"\n    title: "Uno"\n';
  fs.writeFileSync(path.join(badVault, 'book.yaml'), invalidYAML);
  fs.writeFileSync(path.join(badVault, '01.md'), 'testo');
  const invalidBytes = fs.readFileSync(path.join(badVault, 'book.yaml'));

  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, HOME: tmp, PUBCRAFT_CONFIG_DIR: path.join(tmp, 'cfg'),
      PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser, context, page;
  try {
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { const r = await fetch(`${url}/api/state`); if (r.ok) { ready = true; break; } } catch {}
      await wait(50);
    }
    assert(ready, 'server did not start');

    browser = await puppeteer.connect({
      browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
      defaultViewport: null,
    });
    context = await browser.createBrowserContext();
    page = await context.newPage();
    page.on('pageerror', e => console.log('PAGE ERROR:', e.message));
    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md', { polling: 100 });

    const snapshot = () => page.evaluate(() => state.book.chapters.map(c => ({
      file: c.file, id: c.id, title: c.title, type: c.type,
    })));
    const initial = await snapshot();
    assert.deepEqual(initial.map(c => c.file), ['01.md', '02.md', '03.md']);

    console.log('[1] Selezione singola via import manoscritto...');
    await page.evaluate(async (v) => {
      const orig = window.pickPath;
      window.pickPath = async () => ({ dir: v, files: ['02.md'], path: v + '/02.md', toString: () => v });
      try { await importArchive(); } finally { window.pickPath = orig; }
    }, vault);
    await wait(800);
    assert.deepEqual(await snapshot(), initial, 'la selezione singola ha alterato i capitoli');
    assert.deepEqual(fs.readFileSync(path.join(vault, 'book.yaml')), originalBookYAML,
      'book.yaml modificato da una selezione già presente');
    console.log('  ✔ Selezione singola non distruttiva.');

    console.log('[2] Selezione multipla con un file nuovo...');
    fs.writeFileSync(path.join(vault, '04.md'), '---\ntitle: Quattro\n---\n\nTesto quattro.\n');
    await page.evaluate(async (v) => {
      const orig = window.pickPath;
      window.pickPath = async () => ({ dir: v, files: ['02.md', '04.md'], path: v + '/04.md', toString: () => v });
      try { await importArchive(); } finally { window.pickPath = orig; }
    }, vault);
    await page.waitForFunction(() => state.book && state.book.chapters.length === 4, { polling: 100 });
    const afterAdd = await snapshot();
    assert.deepEqual(afterAdd.slice(0, 3), initial, 'capitoli esistenti alterati');
    assert.equal(afterAdd[3].file, '04.md', 'file nuovo non accodato');
    const reparsed = fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8');
    assert(reparsed.includes('custom_meta:'), 'campo custom perso');
    console.log('  ✔ Selezione multipla conserva il libro e accoda il nuovo file.');

    console.log('[3] Book.yaml invalido: rifiuto senza modifiche...');
    const status = await page.evaluate(async (bad) => {
      const res = await fetch('/api/import-manuscript', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir: bad, files: ['01.md'] }),
      });
      return res.status;
    }, badVault);
    assert.notEqual(status, 200, 'book.yaml invalido accettato');
    assert.deepEqual(fs.readFileSync(path.join(badVault, 'book.yaml')), invalidBytes,
      'book.yaml invalido modificato');
    console.log('  ✔ Book.yaml invalido preservato byte-per-byte.');

    console.log('\n=== TEST SELEZIONE FILE SUPERATI ===');
  } finally {
    if (context) await context.close().catch(() => {});
    browser && browser.disconnect();
    server.kill();
    await wait(200);
    fs.rmSync(tmp, { recursive: true, force: true });
  }
})().catch(err => { console.error(err); process.exit(1); });

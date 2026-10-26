const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');

const root = path.resolve(__dirname, '..');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

(async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-breaks-test-'));
  const vault = path.join(tmp, 'vault');

  fs.mkdirSync(vault, { recursive: true });
  fs.writeFileSync(path.join(vault, 'book.yaml'), 'title: Test Andare a Capo\nchapters:\n- file: 01.md\n  title: Capitolo 1\n');
  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo 1\nTesto iniziale');

  const port = await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const value = server.address().port;
      server.close(() => resolve(value));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser;
  let context;
  let page;

  try {
    let ready = false;
    for (let i = 0; i < 80; i++) {
      try {
        const res = await fetch(`${url}/api/state`);
        if (res.ok) { ready = true; break; }
      } catch (_) {}
      await wait(100);
    }
    assert.equal(ready, true, 'Server must start');

    browser = await puppeteer.connect({
      browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
      defaultViewport: null,
    });
    context = await browser.createBrowserContext();
    page = await context.newPage();

    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md', { polling: 100 });

    console.log('[Test 1] Digitazione nell\'editor con invio per andare a capo...');
    // Scrivi 3 righe separate da singolo invio \n
    const linesText = '# Capitolo 1\nQuesta è la prima riga del testo.\nQuesta è la seconda riga che deve andare a capo.\nQuesta è la terza riga di chiusura.';

    await page.evaluate((text) => {
      const ta = document.getElementById('ta');
      ta.value = text;
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      queueSave();
    }, linesText);

    // Attendi aggiornamento anteprima
    await page.waitForFunction(() => {
      const frame = document.getElementById('prevFrame');
      const doc = frame && (frame.contentDocument || frame.contentWindow?.document);
      return doc && doc.querySelectorAll('article.preview p').length >= 3;
    }, { timeout: 5000, polling: 100 });

    console.log('[Test 2] Verifica rendering anteprima live (prevFrame)...');
    const previewData = await page.evaluate(() => {
      const frame = document.getElementById('prevFrame');
      const doc = frame.contentDocument || frame.contentWindow.document;
      const paras = Array.from(doc.querySelectorAll('article.preview p'));
      return paras.map(p => {
        const r = p.getBoundingClientRect();
        return {
          text: p.textContent.trim(),
          top: r.top,
          bottom: r.bottom,
          height: r.height,
        };
      });
    });

    console.log('Paragrafi trovati in anteprima:', previewData);

    assert.equal(previewData.length, 3, `L'anteprima deve contenere esattamente 3 paragrafi distinti, trovati ${previewData.length}`);
    assert(previewData[0].text.includes('prima riga'), 'Primo paragrafo presente');
    assert(previewData[1].text.includes('seconda riga'), 'Secondo paragrafo presente');
    assert(previewData[2].text.includes('terza riga'), 'Terzo paragrafo presente');

    // Verifica che vadano effettivamente a capo (ognuno sotto il precedente)
    assert(previewData[1].top >= previewData[0].bottom - 2, 'Il secondo paragrafo deve andare a capo sotto il primo');
    assert(previewData[2].top >= previewData[1].bottom - 2, 'Il terzo paragrafo deve andare a capo sotto il secondo');
    console.log('  ✔ Anteprima live va a capo correttamente su 3 righe/paragrafi distinti!');

    console.log('[Test 3] Salvataggio e verifica build di stampa (print.html)...');
    await page.evaluate(async () => {
      await saveNow();
    });

    // Avvia la build di stampa
    const buildRes = await fetch(`${url}/api/build`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kind: 'print', trim: '6x9', bleed: false, outdir: path.join(tmp, 'out-print') }),
    }).then(r => r.json());

    assert.equal(buildRes.ok, true, 'Build print deve avere successo: ' + JSON.stringify(buildRes));

    const printHtmlPath = path.join(tmp, 'out-print', 'print.html');
    assert(fs.existsSync(printHtmlPath), 'print.html deve esistere su disco');
    const printHtml = fs.readFileSync(printHtmlPath, 'utf8');

    // Verifica che print.html contenga i 3 paragrafi separati
    assert(printHtml.includes('<p class="noindent first-para">'), 'Primo paragrafo con stile first-para');
    assert(printHtml.includes('uesta è la prima riga del testo.'), 'Contenuto riga 1 in print.html');
    assert(printHtml.includes('<p>Questa è la seconda riga che deve andare a capo.</p>'), 'Riga 2 deve essere un <p> separato in print.html');
    assert(printHtml.includes('<p>Questa è la terza riga di chiusura.</p>'), 'Riga 3 deve essere un <p> separato in print.html');
    console.log('  ✔ Stampa (print.html) va a capo correttamente con paragrafi distinti!');

    console.log('\n==========================================================');
    console.log(' TUTTI I TEST ANDARE A CAPO SUPERATI CON SUCCESSO! ');
    console.log('==========================================================\n');
  } finally {
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    if (browser) await browser.disconnect().catch(() => {});
    if (server) {
      server.kill();
      await wait(500);
    }
    fs.rmSync(tmp, { recursive: true, force: true });
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});

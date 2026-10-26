const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

(async () => {
  console.log('===> Avvio test anteprima pubblicazione (tests/test_publication_preview.cjs)...');
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-preview-test-'));
  const vault = path.join(tmp, 'TestBook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Libro Sintetico Anteprima"
author: "Autore Test"
language: "it"
style: "classic"
trim: "6x9"
chapters:
  - file: "01.md"
    title: "Capitolo Uno"
  - file: "02.md"
    title: "Capitolo Due"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Uno\n\nQuesto è il primo capitolo di prova per verificare l\'anteprima reale.\n');
  fs.writeFileSync(path.join(vault, '02.md'), '# Capitolo Due\n\nQuesto è il secondo capitolo con testo e accenti: caffè, perché, città.\n');

  const port = await new Promise(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, HOME: tmp, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser, context, page;
  try {
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try {
        const res = await fetch(`${url}/api/state`);
        if (res.ok) { ready = true; break; }
      } catch {}
      await wait(50);
    }
    assert(ready, 'server did not start');

    browser = await puppeteer.connect({ browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222' });
    context = await browser.createBrowserContext();
    page = await context.newPage();
    const errors = [];
    page.on('pageerror', err => { console.log('PAGE ERROR:', err); errors.push(err.message); });
    page.on('console', msg => console.log('PAGE LOG:', msg.text()));

    // Monitora chiamate di rete
    let previewCalls = 0;
    let buildCalls = 0;
    page.on('request', req => {
      if (req.url().includes('/api/publication-preview') && req.method() === 'POST') previewCalls++;
      if (req.url().includes('/api/build') && req.method() === 'POST') buildCalls++;
    });

    page.on('console', msg => console.log('PAGE LOG:', msg.text()));
    page.on('pageerror', err => console.log('PAGE ERROR:', err.message));
    page.on('requestfailed', req => console.log('REQ FAILED:', req.url(), req.failure().errorText));

    await page.goto(url);
    await page.waitForFunction(() => currentFile === '01.md' && !isProjectSwitching);

    // A bounded page width must not be achieved by clipping actions or dropdowns.
    await page.setViewport({ width: 1024, height: 768 });
    await page.$eval('#btnTypography', el => el.click());
    await page.waitForSelector('#typographyList:not(.hidden) button', { visible: true });
    const responsive = await page.evaluate(() => {
      const menu = document.querySelector('#typographyList');
      const item = menu.querySelector('button');
      const reachable = el => {
        const r = el.getBoundingClientRect();
        const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
        return r.width > 0 && r.left >= 0 && r.right <= innerWidth && !!hit && (hit === el || el.contains(hit));
      };
      return { width: document.documentElement.scrollWidth, menu: !!item && reachable(item) };
    });
    assert.equal(responsive.width, 1024, 'Topbar must fit the viewport');
    assert(responsive.menu, 'Typography menu must be visible and clickable, not overflow-clipped');
    await page.$eval('#btnTypography', el => el.click());

    await page.$eval('#btnExportMenu', el => el.click());
    await page.waitForSelector('#exportList:not(.hidden)', { visible: true });
    assert(await page.$eval('#btnWatch', el => {
      const r = el.getBoundingClientRect();
      const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return !!hit && (hit === el || el.contains(hit));
    }), 'Watch action must remain clickable at 1024px');
    await page.setViewport({ width: 1440, height: 900 });

    // [Test 1] Aprire il pannello: ZERO richieste di generazione
    console.log('[Test 1] Apertura pannello anteprima pubblicazione...');
    await page.$eval('#btnPubPreview', el => el.click());
    await page.waitForSelector('#pubPreviewModal:not(.hidden)');
    assert.equal(previewCalls, 0, 'Apertura del pannello non deve avviare build');
    assert.equal(buildCalls, 0, 'Apertura del pannello non deve avviare build');

    const statusTextInitial = await page.$eval('#pubStatusText', el => el.textContent.trim());
    assert(statusTextInitial.includes('Nessuna anteprima') || statusTextInitial.includes('No preview'), 'Stato iniziale deve essere Nessuna anteprima');

    // [Test 2] Digitare: badge diventa "Da aggiornare", ZERO build
    console.log('[Test 2] Digitazione in editor...');
    await page.type('#ta', '\nTesto aggiunto per verifica obsolescenza.');
    await wait(100);
    const statusTextAfterTyping = await page.$eval('#pubStatusText', el => el.textContent.trim());
    assert(statusTextAfterTyping.includes('Nessuna anteprima') || statusTextAfterTyping.includes('Da aggiornare'), 'Badge deve riflettere stato');
    assert.equal(previewCalls, 0, 'Digitazione non deve avviare alcuna build');

    // [Test 3] Cambio trim o controlli: ZERO build
    console.log('[Test 3] Cambio trim in pannello...');
    await page.select('#pubTrim', '5x8');
    await wait(50);
    assert.equal(previewCalls, 0, 'Cambio trim non deve avviare build automatica');

    // [Test 4] Clic "Aggiorna anteprima": flush salvataggio terminato prima della generazione
    console.log('[Test 4] Clic Aggiorna anteprima...');
    const printConfigBefore = fs.existsSync(path.join(vault, '.pubcraft-print.json'));
    assert.equal(printConfigBefore, false, '.pubcraft-print.json non deve esistere prima');

    const resPromise = page.waitForResponse(r => r.url().includes('/api/publication-preview') && r.request().method() === 'POST');
    await page.$eval('#btnPubUpdate', el => el.click());
    const resp = await resPromise;
    assert.equal(resp.status(), 200, await resp.text());
    const data = await resp.json();
    assert(data.preview_id, 'preview_id presente');
    assert(data.artifact_url, 'artifact_url presente');
    assert(data.download_url, 'download_url presente');
    assert(data.page_count > 0, 'page_count positivo');
    assert.deepEqual(data.settings, { trim: '5x8', bleed: false }, 'Artifact must return actual print settings, not just a hash');

    // Attendi rendering PDF e aggiornamento stato
    await page.waitForSelector('.pub-page-card');
    await page.waitForFunction(() => {
      const el = document.getElementById('pubStatusText');
      return el && (el.textContent.includes('Aggiornata') || el.textContent.includes('Updated'));
    }, { polling: 100 });
    const statusAfterUpdate = await page.$eval('#pubStatusText', el => el.textContent.trim());
    assert(statusAfterUpdate.includes('Aggiornata') || statusAfterUpdate.includes('Updated'), 'Stato deve essere Aggiornata');

    // Verifica isolamento: .pubcraft-print.json NON deve essere stato creato nel vault dalla preview
    const printConfigAfter = fs.existsSync(path.join(vault, '.pubcraft-print.json'));
    assert.equal(printConfigAfter, false, 'Anteprima non deve alterare .pubcraft-print.json del vault');

    // [Test 5] Download file visualizzato
    console.log('[Test 5] Verifica endpoint download artefatto...');
    const downloadHref = await page.$eval('#btnPubDownload', el => el.href);
    assert(downloadHref.includes(data.download_url), 'Pulsante download deve puntare all\'URL dell\'artefatto');
    const dlFetch = await fetch(downloadHref);
    assert.equal(dlFetch.status, 200);
    assert.equal(dlFetch.headers.get('content-type'), 'application/pdf');
    const pdfData = await dlFetch.arrayBuffer();
    assert(pdfData.byteLength > 100, 'I byte del PDF scaricato devono essere validi');

    // [Test 6] Interazioni visualizzatore PDF (zoom, guide, cambio pagina): ZERO rigenerazioni
    console.log('[Test 6] Interazioni visualizzatore PDF (zoom, guide, vista affiancata)...');
    const callsBeforeInteractions = previewCalls;
    await page.$eval('#btnPubViewFacing', el => el.click());
    await wait(100);
    await page.$eval('#pubGuideCut', el => el.click());
    await wait(100);
    await page.$eval('#pubGuideSafe', el => el.click());
    await wait(100);
    await page.select('#pubZoomSelect', '1.25');
    await wait(100);
    assert.equal(previewCalls, callsBeforeInteractions, 'Interazioni visualizzatore non devono avviare build');

    // [Test 7] Passaggio a modalità Ebook: ZERO richieste di generazione automatiche
    console.log('[Test 7] Passaggio a modalità Ebook...');
    await page.$eval('#btnPubTargetEbook', el => el.click());
    await wait(100);
    assert.equal(previewCalls, callsBeforeInteractions, 'Passaggio a tab Ebook non deve avviare build');
    const ebookStatusBefore = await page.$eval('#pubStatusText', el => el.textContent.trim());
    assert(ebookStatusBefore.includes('Nessuna anteprima') || ebookStatusBefore.includes('No preview'), 'Ebook deve avere stato separato');

    // [Test 8] Clic "Aggiorna anteprima" in Ebook: compilazione EPUB reale
    console.log('[Test 8] Aggiorna anteprima Ebook...');
    const epubResPromise = page.waitForResponse(r => r.url().includes('/api/publication-preview') && r.request().method() === 'POST');
    await page.$eval('#btnPubUpdate', el => el.click());
    const epubResp = await epubResPromise;
    assert.equal(epubResp.status(), 200, await epubResp.text());
    const epubData = await epubResp.json();
    assert.equal(epubData.target, 'ebook', 'Target deve essere ebook');
    assert(epubData.preview_id, 'preview_id presente');
    assert(epubData.artifact_url, 'artifact_url presente');

    // Attendi caricamento capitolo in iframe e aggiornamento stato
    await page.waitForFunction(() => {
      const fr = document.querySelector('#pubEbookFrame');
      const st = document.querySelector('#pubStatusText');
      const stText = st ? st.textContent.trim() : '';
      return fr && fr.src && fr.src.includes('/epub/') && (stText.includes('Aggiornata') || stText.includes('Updated'));
    }, { polling: 100 });

    // [Test 9] Tre profili, orientamento e font: reflow locale senza build
    console.log('[Test 9] Reflow e profili Ebook (Tablet, Smartphone, Orizzontale, Font)...');
    const callsBeforeEbookControls = previewCalls;
    await page.$eval('#btnPubProfileTablet', el => el.click());
    await wait(50);
    const hasTabletClass = await page.$eval('#pubEbookDeviceFrame', el => el.classList.contains('profile-tablet'));
    assert(hasTabletClass, 'Classe profile-tablet applicata');

    await page.$eval('#btnPubProfilePhone', el => el.click());
    await wait(50);
    const hasPhoneClass = await page.$eval('#pubEbookDeviceFrame', el => el.classList.contains('profile-phone'));
    assert(hasPhoneClass, 'Classe profile-phone applicata');

    await page.$eval('#btnPubOrientLandscape', el => el.click());
    await wait(50);
    const hasLandscapeClass = await page.$eval('#pubEbookDeviceFrame', el => el.classList.contains('orient-landscape'));
    assert(hasLandscapeClass, 'Classe orient-landscape applicata');

    await page.$eval('#btnPubFontInc', el => el.click());
    await wait(50);
    const fontVal = await page.$eval('#pubFontVal', el => el.textContent.trim());
    assert.equal(fontVal, '110%', 'Testo ridimensionato a 110%');

    await page.$eval('#btnPubFontReset', el => el.click());
    await wait(50);
    const fontValReset = await page.$eval('#pubFontVal', el => el.textContent.trim());
    assert.equal(fontValReset, '100%', 'Testo ripristinato a 100%');

    assert.equal(previewCalls, callsBeforeEbookControls, 'Controlli di lettura non devono avviare alcuna ricompilazione');

    // [Test 10] Download EPUB reale
    console.log('[Test 10] Verifica download EPUB...');
    const epubDownloadHref = await page.$eval('#btnPubDownload', el => el.href);
    assert(epubDownloadHref.includes(epubData.download_url), 'Download deve puntare all\'EPUB');
    const epubDlFetch = await fetch(epubDownloadHref);
    assert.equal(epubDlFetch.status, 200);
    assert.equal(epubDlFetch.headers.get('content-type'), 'application/epub+zip');
    const epubBytes = await epubDlFetch.arrayBuffer();
    // I primi 4 byte di uno zip/epub valido sono PK\x03\x04
    const pkMagic = new Uint8Array(epubBytes.slice(0, 4));
    assert.equal(pkMagic[0], 0x50, 'Magic P');
    assert.equal(pkMagic[1], 0x4b, 'Magic K');

    // [Test 11] Chiusura con tasto Escape e ripristino focus
    console.log('[Test 11] Chiusura con Escape e ripristino focus...');
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => document.querySelector('#pubPreviewModal').classList.contains('hidden'), { polling: 100 });

    console.log('✔ TUTTI I TEST ANTEPRIMA PUBBLICAZIONE SUPERATI CON SUCCESSO!');

  } finally {
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    if (browser) await browser.disconnect().catch(() => {});
    if (server) {
      await new Promise(resolve => {
        server.on('exit', resolve);
        server.kill();
        setTimeout(resolve, 2000);
      });
    }
    try { fs.rmSync(tmp, { recursive: true, force: true }); } catch {}
  }
  process.exit(0);
})().catch(err => {
  console.error('FAIL test_publication_preview:', err);
  process.exit(1);
});

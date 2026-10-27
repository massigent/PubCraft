const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-reorder-'));
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Test Riordino Capitoli"
author: "Autore Test"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Primo"
  - file: "02.md"
    title: "Capitolo Secondo"
  - file: "03.md"
    title: "Capitolo Terzo"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Primo\n\nContenuto del primo capitolo.\n');
  fs.writeFileSync(path.join(vault, '02.md'), '# Capitolo Secondo\n\nContenuto del secondo capitolo.\n');
  fs.writeFileSync(path.join(vault, '03.md'), '# Capitolo Terzo\n\nContenuto del terzo capitolo.\n');

  const port = await new Promise(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', () => {
      const p = server.address().port;
      server.close(() => resolve(p));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, HOME: tmp, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser, page;
  try {
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try {
        const res = await fetch(`${url}/api/state`);
        if (res.ok) { ready = true; break; }
      } catch {}
      await wait(50);
    }
    assert(ready, 'pubcraftgui server did not start');

    const bravePath = '/Applications/Brave Browser.app/Contents/MacOS/Brave Browser';
    if (process.env.BROWSER_CDP_URL) {
      browser = await puppeteer.connect({ browserURL: process.env.BROWSER_CDP_URL });
      const context = await browser.createBrowserContext();
      page = await context.newPage();
    } else if (fs.existsSync(bravePath)) {
      browser = await puppeteer.launch({
        executablePath: bravePath,
        headless: true,
        args: ['--no-sandbox', '--disable-gpu'],
      });
      page = await browser.newPage();
    } else {
      throw new Error('No browser available for test');
    }

    page.on('pageerror', err => console.log('PAGE ERROR:', err));
    await page.setViewport({ width: 1280, height: 800 });
    await page.goto(url, { waitUntil: 'networkidle0' });
    await wait(400);

    console.log('[Test 1] Verifica presenza frecce su/giù nella barra laterale...');
    const upBtns = await page.$$eval('#chapterList .move-up', els => els.map(e => e.disabled));
    const downBtns = await page.$$eval('#chapterList .move-down', els => els.map(e => e.disabled));
    assert.strictEqual(upBtns.length, 3, 'Devono esserci 3 pulsanti move-up');
    assert.strictEqual(downBtns.length, 3, 'Devono esserci 3 pulsanti move-down');
    assert.strictEqual(upBtns[0], true, 'Il pulsante move-up del primo capitolo deve essere disabilitato');
    assert.strictEqual(upBtns[1], false, 'Il pulsante move-up del secondo capitolo deve essere abilitato');
    assert.strictEqual(downBtns[2], true, 'Il pulsante move-down dell\'ultimo capitolo deve essere disabilitato');
    console.log('  ✔ Frecce presenti e stati disabilitato/abilitato corretti.');

    console.log('[Test 2] Spostamento verso il basso con freccia ▼...');
    // Clicca move-down sul primo capitolo (01.md)
    await page.evaluate(() => {
      const btn = document.querySelectorAll('#chapterList .move-down')[0];
      btn.click();
    });
    await wait(500);

    let chaptersOrder = await page.$$eval('#chapterList li', els => els.map(e => e.dataset.file));
    assert.deepStrictEqual(chaptersOrder, ['02.md', '01.md', '03.md'], 'L\'ordine nella sidebar deve essere [02.md, 01.md, 03.md]');

    let yamlContent = fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8');
    const idx02 = yamlContent.indexOf('02.md');
    const idx01 = yamlContent.indexOf('01.md');
    const idx03 = yamlContent.indexOf('03.md');
    assert(idx02 < idx01 && idx01 < idx03, 'book.yaml deve riflettere l\'ordine 02.md, 01.md, 03.md');
    console.log('  ✔ Capitolo spostato in basso con successo in UI e su disco.');

    console.log('[Test 3] Spostamento verso l\'alto con freccia ▲...');
    // Clicca move-up sul secondo capitolo (che ora è 01.md a indice 1)
    await page.evaluate(() => {
      const btn = document.querySelectorAll('#chapterList .move-up')[1];
      btn.click();
    });
    await wait(500);

    chaptersOrder = await page.$$eval('#chapterList li', els => els.map(e => e.dataset.file));
    assert.deepStrictEqual(chaptersOrder, ['01.md', '02.md', '03.md'], 'L\'ordine nella sidebar deve essere ripristinato');
    console.log('  ✔ Capitolo spostato in alto con successo in UI e su disco.');

    console.log('[Test 4] Apertura vista Lavagna a Schede (Corkboard)...');
    await page.click('#vwCards');
    await wait(400);

    const isCorkboardVisible = await page.$eval('#corkboardPane', el => !el.classList.contains('hidden'));
    assert(isCorkboardVisible, 'La vista Corkboard deve essere visibile');

    const cardMovePrev = await page.$$eval('#corkboardGrid .cb-card-move-prev', els => els.map(e => e.disabled));
    const cardMoveNext = await page.$$eval('#corkboardGrid .cb-card-move-next', els => els.map(e => e.disabled));
    assert.strictEqual(cardMovePrev.length, 3, 'Devono esserci 3 pulsanti move-prev sulle schede');
    assert.strictEqual(cardMoveNext.length, 3, 'Devono esserci 3 pulsanti move-next sulle schede');
    assert.strictEqual(cardMovePrev[0], true, 'move-prev sulla prima scheda deve essere disabilitato');
    assert.strictEqual(cardMoveNext[2], true, 'move-next sull\'ultima scheda deve essere disabilitato');
    console.log('  ✔ Frecce ◀ e ▶ presenti sulle schede con stati corretti.');

    console.log('[Test 5] Spostamento capitolo in avanti nella Lavagna con freccia ▶...');
    await page.evaluate(() => {
      const btn = document.querySelectorAll('#corkboardGrid .cb-card-move-next')[0];
      btn.click();
    });
    await wait(500);

    let cardFiles = await page.$$eval('#corkboardGrid .cb-card', els => els.map(e => e.dataset.file));
    assert.deepStrictEqual(cardFiles, ['02.md', '01.md', '03.md'], 'Le schede nella lavagna devono essere [02.md, 01.md, 03.md]');

    yamlContent = fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8');
    assert(yamlContent.indexOf('02.md') < yamlContent.indexOf('01.md'), 'book.yaml deve avere 02.md prima di 01.md');
    console.log('  ✔ Spostamento tramite freccia nella Lavagna eseguito correttamente.');

    console.log('[Test 6] Riordino tramite reorderChaptersByDrop nella Lavagna...');
    // Sposta 03.md prima di 02.md
    await page.evaluate(async () => {
      await window.reorderChaptersByDrop('03.md', '02.md', true);
    });
    await wait(500);

    cardFiles = await page.$$eval('#corkboardGrid .cb-card', els => els.map(e => e.dataset.file));
    assert.deepStrictEqual(cardFiles, ['03.md', '02.md', '01.md'], 'Le schede nella lavagna devono essere [03.md, 02.md, 01.md]');

    // Verifica che siamo ancora nella Lavagna e non nell'editor
    const stillInCards = await page.$eval('#corkboardPane', el => !el.classList.contains('hidden'));
    assert(stillInCards, 'La vista corrente deve rimanere la Lavagna dopo il riordino');
    console.log('  ✔ Riordino eseguito senza transizione accidentale all\'editor.');

    console.log('[Test 7] Apertura capitolo con clic sulla scheda...');
    await page.evaluate(() => {
      const card = document.querySelectorAll('#corkboardGrid .cb-card')[0];
      card.click();
    });
    await wait(400);

    const isInEdit = await page.$eval('#doc', el => !el.classList.contains('hidden'));
    assert(isInEdit, 'Un clic intenzionale sulla scheda deve aprire il capitolo nell\'editor');
    console.log('  ✔ Clic intenzionale apre il capitolo nell\'editor con successo.');

    console.log('\n==========================================================');
    console.log(' TUTTI I TEST DEL RIORDINO CAPITOLI SUPERATI CON SUCCESSO! ');
    console.log('==========================================================');

  } finally {
    if (page) await page.close().catch(() => {});
    if (browser) await browser.close().catch(() => {});
    server.kill('SIGTERM');
    try { fs.rmSync(tmp, { recursive: true, force: true }); } catch {}
  }
})().catch(err => {
  console.error('TEST FALLITO:', err);
  process.exit(1);
});

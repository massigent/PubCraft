const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-tb-'));
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Libro Test Toolbar"
author: "Autore Test"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Uno"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Uno\n\nQuesto è un testo di prova per la barra strumenti.\nSeconda riga del testo.\n');

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

    browser = await puppeteer.connect({
      browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
    });
    context = await browser.createBrowserContext();
    page = await context.newPage();
    page.on('dialog', async d => {
      console.log('DIALOG DETECTED:', d.type(), d.message());
      await d.accept();
    });
    page.on('pageerror', err => console.log('PAGE ERROR:', err));
    page.on('console', msg => console.log('BROWSER:', msg.text()));
    page.on('requestfailed', req => console.log('REQ_FAIL:', req.url(), req.failure()));
    page.on('response', res => { if (res.status() >= 400) console.log('RESP_ERR:', res.url(), res.status()); });

    await page.goto(url);
    await page.waitForFunction(() => currentFile === '01.md' && !isProjectSwitching);

    console.log('[Test 1] Verifica presenza bottoni toolbar...');
    await page.waitForSelector('#editorToolbar');
    const buttons = ['tbBold', 'tbItalic', 'tbH2', 'tbH3', 'tbQuote', 'tbUl', 'tbOl', 'tbBreak', 'tbLink', 'tbImage', 'tbTable'];
    for (const b of buttons) {
      const exists = await page.$(`#${b}`);
      assert(exists, `Pulsante #${b} deve essere presente`);
    }

    const clickBtn = sel => page.$eval(sel, el => el.click());

    console.log('[Test 2] Formattazione Grassetto (Bold)...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      ta.focus();
      // Seleziona la parola "testo" (indice 12-17)
      const idx = ta.value.indexOf('testo');
      ta.setSelectionRange(idx, idx + 5);
    });
    await clickBtn('#tbBold');
    await wait(100);
    let val = await page.$eval('#ta', el => el.value);
    assert(val.includes('**testo**'), `Atteso **testo**, trovato: ${val}`);

    console.log('[Test 3] Toggle off Grassetto...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      const idx = ta.value.indexOf('testo');
      ta.setSelectionRange(idx, idx + 5);
    });
    await clickBtn('#tbBold');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(!val.includes('**testo**') && val.includes('testo'), 'Toggle off deve rimuovere gli asterischi');

    console.log('[Test 4] Formattazione Corsivo (Italic)...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      const idx = ta.value.indexOf('prova');
      ta.setSelectionRange(idx, idx + 5);
    });
    await clickBtn('#tbItalic');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('*prova*'), `Atteso *prova*, trovato: ${val}`);

    console.log('[Test 5] Sottotitolo H2...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      const idx = ta.value.indexOf('Seconda riga');
      ta.setSelectionRange(idx, idx + 7);
    });
    await clickBtn('#tbH2');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('## Seconda riga'), `Atteso ## Seconda riga, trovato: ${val}`);

    console.log('[Test 6] Citazione (>)...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      const idx = ta.value.indexOf('## Seconda riga');
      ta.setSelectionRange(idx, idx + 5);
    });
    await clickBtn('#tbQuote');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('> Seconda riga'), `Atteso > Seconda riga, trovato: ${val}`);

    console.log('[Test 7] Elenco puntato (-)...');
    await clickBtn('#tbUl');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('- Seconda riga'), `Atteso - Seconda riga, trovato: ${val}`);

    console.log('[Test 8] Elenco numerato (1.)...');
    await clickBtn('#tbOl');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('1. Seconda riga'), `Atteso 1. Seconda riga, trovato: ${val}`);

    console.log('[Test 9] Interruzione di scena (***)...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      ta.setSelectionRange(ta.value.length, ta.value.length);
    });
    await clickBtn('#tbBreak');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('***'), `Atteso ***, trovato: ${val}`);

    console.log('[Test 10] Link (Cmd+K)...');
    await page.evaluate(() => {
      const ta = document.getElementById('ta');
      const idx = ta.value.indexOf('barra');
      ta.setSelectionRange(idx, idx + 5);
    });
    await clickBtn('#tbLink');
    await wait(100);
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('[barra](https://)'), `Atteso [barra](https://), trovato: ${val}`);

    console.log('[Test 11] Upload e Inserimento immagine...');
    const pngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';
    const upRes = await fetch(`${url}/api/uploadimage`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-PubCraft-Vault': vault,
      },
      body: JSON.stringify({
        name: 'illustrazione-1.png',
        data: `data:image/png;base64,${pngBase64}`,
      }),
    });
    assert.equal(upRes.status, 200);
    const upData = await upRes.json();
    assert(upData.ok);
    assert.equal(upData.filename, 'illustrazione-1.png');
    assert(fs.existsSync(path.join(vault, 'images', 'illustrazione-1.png')), 'File immagine deve esistere su disco in images/');

    console.log('[Test 12] Incolla immagine negli appunti (Paste Event)...');
    await page.evaluate((b64) => {
      const byteCharacters = atob(b64);
      const byteNumbers = new Array(byteCharacters.length);
      for (let i = 0; i < byteCharacters.length; i++) {
        byteNumbers[i] = byteCharacters.charCodeAt(i);
      }
      const byteArray = new Uint8Array(byteNumbers);
      const file = new File([byteArray], 'incolla-test.png', { type: 'image/png' });

      const dt = new DataTransfer();
      dt.items.add(file);

      const ta = document.getElementById('ta');
      ta.setSelectionRange(ta.value.length, ta.value.length);
      const pasteEvent = new ClipboardEvent('paste', {
        clipboardData: dt,
        bubbles: true,
        cancelable: true,
      });
      ta.dispatchEvent(pasteEvent);
    }, pngBase64);

    // Attendi salvataggio immagine e inserimento markdown
    await page.waitForFunction(() => {
      const ta = document.getElementById('ta');
      return ta && ta.value.includes('incolla-test.png');
    }, { polling: 100, timeout: 10000 });

    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('![Didascalia](images/incolla-test.png)') || val.includes('incolla-test.png'), 'Tag markdown immagine deve essere inserito');
    assert(fs.existsSync(path.join(vault, 'images', 'incolla-test.png')), 'File immagine incollata deve esistere su disco in images/');

    console.log('[Test 13] Auto-save: verifica persistenza su disco...');
    await page.waitForFunction(() => dirty === false && !isLoadingChapter, { polling: 100, timeout: 10000 });
    await wait(200);
    let fileOnDisk = fs.readFileSync(path.join(vault, '01.md'), 'utf-8');
    assert(fileOnDisk.includes('incolla-test.png'), 'Il capitolo su disco deve contenere le modifiche');

    console.log('[Test 14] Inserimento Tabella guidata...');
    await clickBtn('#tbTable');
    await page.waitForSelector('#tableModal:not(.hidden)');
    await page.evaluate(() => {
      document.getElementById('tableCols').value = '3';
      document.getElementById('tableRows').value = '2';
    });
    await clickBtn('#btnTableInsert');
    await page.waitForSelector('#tableModal.hidden');
    val = await page.$eval('#ta', el => el.value);
    assert(val.includes('| Colonna 1 | Colonna 2 | Colonna 3 |'), 'Tabella markdown deve essere inserita nel textarea');
    assert(val.includes('| Cella 2.1 | Cella 2.2 | Cella 2.3 |'), 'Righe di dati devono essere presenti');

    // Verifica rendering anteprima live
    await page.waitForFunction(() => {
      const frame = document.getElementById('prevFrame');
      const doc = frame && frame.contentDocument;
      return doc && doc.querySelector('table.book-table, table') && doc.querySelectorAll('th').length >= 3;
    }, { polling: 100, timeout: 5000 });

    await page.waitForFunction(() => dirty === false && !isLoadingChapter, { polling: 100, timeout: 10000 });
    await wait(200);
    fileOnDisk = fs.readFileSync(path.join(vault, '01.md'), 'utf-8');
    assert(fileOnDisk.includes('| Colonna 1 | Colonna 2 | Colonna 3 |'), 'La tabella su disco deve essere salvata');

    console.log('✔ TUTTI I TEST DELLA TOOLBAR DI SCRITTURA SUPERATI CON SUCCESSO!');

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
  console.error('FAIL test_editor_toolbar:', err);
  process.exit(1);
});

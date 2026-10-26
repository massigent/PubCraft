const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const fs = require('fs');
const path = require('path');
const net = require('net');
const { spawn } = require('child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmpDir = fs.mkdtempSync('/tmp/pubcraft-i18n-test-');
  const vault = path.join(tmpDir, 'mybook');
  fs.mkdirSync(vault, { recursive: true });

  const customText1 = 'Questo è il primo capitolo personalizzato con testo reale.';
  const customText2 = 'Second chapter with specific English words for testing.';

  fs.writeFileSync(path.join(vault, 'book.yaml'), 'title: Il Mio Libro Reale\nauthor: Autore Prova\nchapters:\n- file: 01.md\n  title: Capitolo Uno\n- file: 02.md\n  title: Chapter Two\n');
  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Uno\n\n' + customText1);
  fs.writeFileSync(path.join(vault, '02.md'), '# Chapter Two\n\n' + customText2);

  const port = await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const val = server.address().port;
      server.close(() => resolve(val));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const guiProc = spawn(path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let ready = false;
  for (let i = 0; i < 80; i++) {
    try {
      const res = await fetch(`${url}/api/state`);
      if (res.ok) { ready = true; break; }
    } catch (_) {}
    await new Promise(r => setTimeout(r, 75));
  }
  if (!ready) throw new Error('Server pubcraftgui non pronto su porta ' + port);

  const browser = await puppeteer.connect({
    browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
  });

  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  page.on('dialog', (d) => d.accept());
  page.on('console', msg => console.log('I18N PAGE LOG:', msg.text()));
  page.on('pageerror', err => console.log('I18N PAGE ERROR:', err));

  try {
    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md');

    console.log('\n--- TEST A: Anteprima reale del manoscritto nel Modal di Stampa ---');
    // Clicca sul pulsante stampa nel menu esporta per aprire il modal
    await page.click('#btnExportMenu');
    await page.waitForSelector('#exportList:not(.hidden)');
    await page.click('#btnPrint');
    await page.waitForSelector('#printModal:not(.hidden)');

    // Leggi il contenuto dell'iframe di anteprima (#styleSample)
    const previewContent = await page.evaluate(async () => {
      const iframe = document.getElementById('styleSample');
      // attende che l'iframe riceva srcdoc
      await new Promise(r => setTimeout(r, 400));
      const doc = iframe.contentDocument || (iframe.contentWindow && iframe.contentWindow.document);
      return {
        bodyText: doc ? doc.body.innerText : '',
        html: doc ? doc.body.innerHTML : '',
        dantePresent: doc ? doc.body.innerText.includes('Nel mezzo del cammin') : false,
        customTextPresent: doc ? doc.body.innerText.includes('primo capitolo personalizzato') : false,
        chapterSelectCount: document.getElementById('printPreviewChapter') ? document.getElementById('printPreviewChapter').options.length : 0,
        selectedChapter: document.getElementById('printPreviewChapter') ? document.getElementById('printPreviewChapter').value : '',
      };
    });

    console.log('Verifica anteprima:', previewContent);

    if (previewContent.dantePresent) {
      throw new Error('FALLITO: Il modal mostra ancora il testo di esempio statico di Dante!');
    }
    if (!previewContent.customTextPresent) {
      throw new Error('FALLITO: Il modal NON mostra il testo del capitolo reale del manoscritto!');
    }
    if (previewContent.chapterSelectCount !== 2) {
      throw new Error(`FALLITO: Il selettore capitoli per l'anteprima ha ${previewContent.chapterSelectCount} opzioni invece di 2!`);
    }
    console.log('✔ Anteprima capitolo reale visualizzata correttamente (nessun testo finto di Dante).');

    // Cambia capitolo nel selettore di anteprima
    console.log('\n--- Cambio capitolo nel selettore di anteprima stampa ---');
    await page.select('#printPreviewChapter', '02.md');
    await new Promise(r => setTimeout(r, 500));

    const previewCh2 = await page.evaluate(() => {
      const iframe = document.getElementById('styleSample');
      const doc = iframe.contentDocument || (iframe.contentWindow && iframe.contentWindow.document);
      return {
        bodyText: doc ? doc.body.innerText : '',
        hasCh2Text: doc ? doc.body.innerText.includes('Second chapter with specific English words') : false,
      };
    });
    console.log('Verifica cambio capitolo in anteprima:', previewCh2);
    if (!previewCh2.hasCh2Text) {
      throw new Error('FALLITO: Il cambio capitolo nel selettore non ha aggiornato l\'anteprima con il testo del capitolo 2!');
    }
    console.log('✔ Cambio capitolo in anteprima funzionante.');

    // Chiudi modal stampa
    await page.$eval('#btnPrintCancel', el => el.click());
    await page.waitForFunction(() => document.querySelector('#printModal').classList.contains('hidden'), { polling: 100 });

    // -------------------------------------------------------------------------
    // TEST B: Internazionalizzazione (i18n) e selettore lingua
    // -------------------------------------------------------------------------
    console.log('\n--- TEST B: Internazionalizzazione (10 lingue e selettore) ---');
    const languagesToTest = [
      { code: 'en', newBookText: '✨ New Book…', printBtn: 'Print / PDF…' },
      { code: 'it', newBookText: '✨ Nuovo libro…', printBtn: 'PDF stampa…' },
      { code: 'es', newBookText: '✨ Nuevo libro…', printBtn: 'Imprimir / PDF…' },
      { code: 'fr', newBookText: '✨ Nouveau livre…', printBtn: 'Imprimer / PDF…' },
      { code: 'de', newBookText: '✨ Neues Buch…', printBtn: 'Drucken / PDF…' },
      { code: 'pt', newBookText: '✨ Novo livro…', printBtn: 'Imprimir / PDF…' },
      { code: 'zh', newBookText: '✨ 新建图书…', printBtn: '打印 / PDF…' },
      { code: 'ja', newBookText: '✨ 新規書籍…', printBtn: '印刷 / PDF…' },
      { code: 'ru', newBookText: '✨ Новая книга…', printBtn: 'Печать / PDF…' },
      { code: 'ar', newBookText: '✨ كتاب جديد…', printBtn: 'طباعة / PDF…' },
    ];

    for (const lang of languagesToTest) {
      console.log(`Test lingua: ${lang.code}...`);
      await page.evaluate((code) => {
        setLanguage(code);
      }, lang.code);
      await new Promise(r => setTimeout(r, 200));

      const texts = await page.evaluate(() => ({
        btnNewProject: document.getElementById('btnNewProject').textContent.trim(),
        btnPrint: (document.querySelector('#btnPrint strong') || document.getElementById('btnPrint')).textContent.trim(),
        htmlLang: document.documentElement.lang,
        storedLang: localStorage.getItem('pubcraft_lang'),
      }));

      if (texts.btnNewProject !== lang.newBookText) {
        throw new Error(`FALLITO per ${lang.code}: atteso '${lang.newBookText}', ottenuto '${texts.btnNewProject}'`);
      }
      if (texts.btnPrint !== lang.printBtn) {
        throw new Error(`FALLITO per ${lang.code}: atteso '${lang.printBtn}', ottenuto '${texts.btnPrint}'`);
      }
      if (texts.htmlLang !== lang.code || texts.storedLang !== lang.code) {
        throw new Error(`FALLITO per ${lang.code}: attributo html lang o localStorage non sincronizzati!`);
      }
      console.log(`  ✔ [${lang.code}] '${texts.btnNewProject}' & '${texts.btnPrint}' validati`);
    }

    console.log('\n--- Test auto-rilevamento lingua ---');
    const autoDetectResult = await page.evaluate(() => {
      localStorage.removeItem('pubcraft_lang');
      const detected = detectLanguage();
      return detected;
    });
    console.log('Lingua rilevata automaticamente:', autoDetectResult);
    if (!autoDetectResult || typeof autoDetectResult !== 'string') {
      throw new Error('FALLITO: detectLanguage() non ha restituito una stringa valida!');
    }
    console.log('✔ Auto-rilevamento della lingua dal sistema superato.');

    console.log('\n==========================================================');
    console.log(' TUTTI I TEST ANTEPRIMA REALE E I18N SUPERATI CON SUCCESSO! ');
    console.log('==========================================================');
  } finally {
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    if (browser) await browser.disconnect().catch(() => {});
    if (guiProc) {
      await new Promise(resolve => {
        guiProc.on('exit', resolve);
        guiProc.kill();
        setTimeout(resolve, 2000);
      });
    }
  }
  process.exit(0);
})();

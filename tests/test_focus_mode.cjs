const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-focus-'));
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Libro Prova Focus"
author: "Autore Test"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Uno"
  - file: "02.md"
    title: "Capitolo Due"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Uno\n\nNel mezzo del cammin di nostra vita mi ritrovai per una selva oscura.\n');
  fs.writeFileSync(path.join(vault, '02.md'), '# Capitolo Due\n\nQuesta e la continuazione del viaggio nel secondo capitolo.\n');

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

    try {
      browser = await puppeteer.connect({
        browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
      });
      context = await browser.createBrowserContext();
      page = await context.newPage();
    } catch {
      browser = await puppeteer.launch({
        executablePath: '/Applications/Brave Browser.app/Contents/MacOS/Brave Browser',
        headless: 'new',
        args: ['--no-sandbox', '--disable-setuid-sandbox'],
      });
      page = await browser.newPage();
    }
    page.on('console', msg => console.log('PAGE LOG:', msg.text()));
    page.on('pageerror', err => console.log('PAGE ERROR:', err));

    await page.setViewport({ width: 1280, height: 800 });
    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md' && !isProjectSwitching, { polling: 100 });

    console.log('[Test 1] Verifica presenza del pulsante Focus Mode (#btnFocusMode)...');
    await page.waitForSelector('#btnFocusMode');

    console.log('[Test 2] Attivazione modalità Non Disturbo (#btnFocusMode)...');
    await page.click('#btnFocusMode');
    await wait(200);

    const isFocus = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(isFocus, 'body.focus-mode deve essere presente');

    const topbarHidden = await page.$eval('#topbar', el => getComputedStyle(el).display === 'none');
    const sidebarHidden = await page.$eval('#sidebar', el => getComputedStyle(el).display === 'none');
    const toolbarFloating = await page.$eval('#editorToolbar', el => {
      const s = getComputedStyle(el);
      return s.display === 'flex' && s.position === 'fixed';
    });
    assert(topbarHidden, 'Topbar deve essere nascosta');
    assert(sidebarHidden, 'Sidebar deve essere nascosta');
    assert(toolbarFloating, 'Editor toolbar deve essere fluttuante in basso in focus mode');

    console.log('[Test 3] Verifica focusHud centrato in alto e metadati capitolo...');
    const hudVisible = await page.$eval('#focusHud', el => !el.classList.contains('hidden') && getComputedStyle(el).display !== 'none');
    assert(hudVisible, '#focusHud deve essere visibile');

    const hudPos = await page.$eval('#focusHud', el => {
      const r = el.getBoundingClientRect();
      return { top: r.top, left: r.left, width: r.width, centerX: r.left + r.width / 2 };
    });
    const viewportWidth = await page.evaluate(() => window.innerWidth);
    assert(Math.abs(hudPos.centerX - viewportWidth / 2) < 20, `HUD deve essere centrato orizzontalmente (centerX=${hudPos.centerX}, expected ~${viewportWidth / 2})`);

    const hudTitle = await page.$eval('#focusChapterTitle', el => el.textContent.trim());
    assert(hudTitle.includes('Capitolo Uno'), `Titolo in focusHud errato: ${hudTitle}`);

    console.log('[Test 3b] Selettore capitoli nel HUD di Focus Mode (#focusChapterSelect)...');
    const chSelectOptions = await page.$$eval('#focusChapterSelect option', opts => opts.map(o => ({ val: o.value, text: o.textContent })));
    assert.equal(chSelectOptions.length, 2, 'Devono esserci 2 capitoli selezionabili nel HUD');
    assert.equal(chSelectOptions[0].val, '01.md');
    assert.equal(chSelectOptions[1].val, '02.md');

    // Cambia capitolo a 02.md senza uscire da Focus Mode
    await page.select('#focusChapterSelect', '02.md');
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '02.md', { polling: 100 });
    const stillInFocus = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(stillInFocus, 'Il cambio capitolo non deve far uscire da focus-mode');
    const hudTitle2 = await page.$eval('#focusChapterTitle', el => el.textContent.trim());
    assert(hudTitle2.includes('Capitolo Due'), `Titolo in focusHud errato dopo switch: ${hudTitle2}`);

    // Torna a 01.md
    await page.select('#focusChapterSelect', '01.md');
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md', { polling: 100 });

    console.log('[Test 4] Alternanza font Macchina da Scrivere / Serif (#btnFocusFont)...');
    await page.click('#btnFocusFont');
    await wait(150);
    const isTypewriter = await page.evaluate(() => document.body.classList.contains('focus-typewriter'));
    assert(isTypewriter, 'body.focus-typewriter deve essere applicato');

    await page.click('#btnFocusFont');
    await wait(150);
    const isSerif = await page.evaluate(() => !document.body.classList.contains('focus-typewriter'));
    assert(isSerif, 'body.focus-typewriter deve essere rimosso tornando a serif');

    console.log('[Test 4b] Attivazione Super Focus (centratura riga e font calibrato)...');
    await page.click('#btnSuperFocus');
    await wait(150);
    const isSuperFocus = await page.evaluate(() => document.body.classList.contains('focus-super-focus'));
    assert(isSuperFocus, 'body.focus-super-focus deve essere applicato');

    const superBtnActive = await page.$eval('#btnSuperFocus', el => el.classList.contains('active'));
    assert(superBtnActive, '#btnSuperFocus deve avere la classe .active');

    // Verifica larghezza colonna e assenza scrollbar stile iA Writer
    const taMaxWidth = await page.$eval('#ta', el => getComputedStyle(el).maxWidth);
    assert.equal(taMaxWidth, '860px', 'Larghezza massima colonna focus deve essere 860px come iA Writer');

    const taScrollbarWidth = await page.$eval('#ta', el => getComputedStyle(el).scrollbarWidth);
    assert.equal(taScrollbarWidth, 'none', 'La barra di scorrimento deve essere completamente nascosta (scrollbar-width: none)');

    const caretColor = await page.$eval('#ta', el => getComputedStyle(el).caretColor);
    assert(caretColor.includes('0, 122, 255') || caretColor.includes('#007aff'), `Caret color deve essere ciano (#007aff), trovato: ${caretColor}`);

    console.log('[Test 4c] Verifica modalità Focus Frase e Paragrafo (#btnFocusDim)...');
    await page.waitForSelector('#btnFocusDim');

    // Default: Spento
    let dimLabel = await page.$eval('#focusDimLabel', el => el.textContent.trim());
    assert(dimLabel.includes('Spento') || dimLabel.includes('Off'), `Etichetta iniziale deve essere Spento/Off, trovato: ${dimLabel}`);

    // Click 1: Attiva modalità Frase
    await page.click('#btnFocusDim');
    await wait(150);
    const isDimSentence = await page.evaluate(() => document.body.classList.contains('focus-dim-active'));
    assert(isDimSentence, 'body.focus-dim-active deve essere presente in modalità Frase');
    dimLabel = await page.$eval('#focusDimLabel', el => el.textContent.trim());
    assert(dimLabel.includes('Frase') || dimLabel.includes('Sentence'), `Etichetta deve essere Frase, trovato: ${dimLabel}`);

    // Verifica presenza e contenuto di #focusBackdrop
    const hasCurrentSpan = await page.$eval('#focusBackdrop .focus-current', el => el.textContent.length > 0);
    assert(hasCurrentSpan, '#focusBackdrop deve contenere un elemento .focus-current per la frase attiva');

    // Click 2: Attiva modalità Paragrafo
    await page.click('#btnFocusDim');
    await wait(150);
    const isDimPara = await page.evaluate(() => document.body.classList.contains('focus-dim-active'));
    assert(isDimPara, 'body.focus-dim-active deve essere presente in modalità Paragrafo');
    dimLabel = await page.$eval('#focusDimLabel', el => el.textContent.trim());
    assert(dimLabel.includes('Paragrafo') || dimLabel.includes('Paragraph'), `Etichetta deve essere Paragrafo, trovato: ${dimLabel}`);

    // Click 3: Torna a Spento
    await page.click('#btnFocusDim');
    await wait(150);
    const isDimOff = await page.evaluate(() => !document.body.classList.contains('focus-dim-active'));
    assert(isDimOff, 'body.focus-dim-active non deve essere presente dopo terzo click');
    dimLabel = await page.$eval('#focusDimLabel', el => el.textContent.trim());
    assert(dimLabel.includes('Spento') || dimLabel.includes('Off'), `Etichetta deve tornare a Spento, trovato: ${dimLabel}`);

    // In Serif con Super Focus: font calibrato ~19px
    const serifFontSize = await page.$eval('#ta', el => parseFloat(getComputedStyle(el).fontSize));
    assert(serifFontSize >= 18 && serifFontSize <= 20, `Font size in Serif Super Focus deve essere ~19px, trovato ${serifFontSize}`);

    // Attiva font Macchina da Scrivere con Super Focus: font ~18px stile iA Writer
    await page.click('#btnFocusFont');
    await wait(150);
    const typewriterFontSize = await page.$eval('#ta', el => parseFloat(getComputedStyle(el).fontSize));
    assert(typewriterFontSize >= 17 && typewriterFontSize <= 19, `Font size in Typewriter Super Focus deve essere ~18px, trovato ${typewriterFontSize}`);

    // Disattiva Super Focus
    await page.click('#btnSuperFocus');
    await wait(150);
    const isSuperFocusOff = await page.evaluate(() => !document.body.classList.contains('focus-super-focus'));
    assert(isSuperFocusOff, 'body.focus-super-focus deve essere rimosso dopo secondo click');

    // Riporta font a Serif
    await page.click('#btnFocusFont');
    await wait(150);

    console.log('[Test 5] Uscita con tasto Escape...');
    await page.keyboard.press('Escape');
    await wait(200);
    const isFocusAfterEsc = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(!isFocusAfterEsc, 'Escape deve uscire da focus-mode');

    const topbarRestored = await page.$eval('#topbar', el => getComputedStyle(el).display !== 'none');
    assert(topbarRestored, 'Topbar deve essere nuovamente visibile');

    console.log('[Test 6] Rientro con F11 ed uscita con pulsante Esci...');
    await page.keyboard.press('F11');
    await wait(200);
    const isFocusF11 = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(isFocusF11, 'F11 deve attivare focus-mode');

    await page.click('#btnFocusExit');
    await wait(200);
    const isFocusExited = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(!isFocusExited, '#btnFocusExit deve uscire da focus-mode');

    console.log('✔ TUTTI I TEST DELLA MODALITÀ NON DISTURBO SUPERATI!');
    process.exit(0);
  } finally {
    try { if (page) await page.close(); } catch {}
    try { if (context) await context.close(); } catch {}
    try { if (browser) await browser.disconnect(); } catch {}
    if (server) {
      await new Promise(resolve => {
        server.on('exit', resolve);
        server.kill();
        setTimeout(resolve, 2000);
      });
    }
    try { fs.rmSync(tmp, { recursive: true, force: true }); } catch {}
  }
})().catch(e => {
  console.error('ERRORE TEST FOCUS MODE:', e);
  process.exit(1);
});

const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-dark-'));
  const configDir = path.join(tmp, 'config');
  fs.mkdirSync(configDir, { recursive: true });
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Libro Prova Dark Mode"
author: "Autore Test"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Notturno"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Notturno\n\nScrittura rilassante con sfondo nero e testo bianco.\n');

  const port = await new Promise(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [vault], {
    env: {
      ...process.env,
      HOME: tmp,
      PUBCRAFT_CONFIG_DIR: configDir,
      PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`,
      PUBCRAFTGUI_NO_BROWSER: '1'
    },
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

    await page.setViewport({ width: 1280, height: 800 });
    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md', { polling: 100 });

    console.log('[Test 1] Verifica presenza del pulsante Dark Mode nella topbar (#btnToggleTheme)...');
    await page.waitForSelector('#btnToggleTheme');
    const isInitiallyDark = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(!isInitiallyDark, 'Il tema iniziale di default deve essere chiaro');

    console.log('[Test 2] Attivazione Dark Mode (#btnToggleTheme)...');
    await page.$eval('#btnToggleTheme', el => el.click());
    await wait(200);

    const isDarkAfterClick = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(isDarkAfterClick, 'body.dark-mode deve essere presente');

    const savedTheme = await page.evaluate(() => localStorage.getItem('pubcraft.theme'));
    assert.equal(savedTheme, 'dark', 'localStorage pubcraft.theme deve essere "dark"');

    // Verifica colori scuri dell\'editor normale
    const taBg = await page.$eval('#ta', el => getComputedStyle(el).backgroundColor);
    const taColor = await page.$eval('#ta', el => getComputedStyle(el).color);
    console.log(`  Editor normale in Dark Mode -> background: ${taBg}, color: ${taColor}`);
    assert(taBg.includes('18, 18, 18'), `Background #ta deve essere scuro (#121212 / rgb(18, 18, 18)), trovato: ${taBg}`);

    console.log('[Test 3] Modalità Non Disturbo (Focus Mode) in Dark Mode: sfondo nero e testo bianco...');
    await page.$eval('#btnFocusMode', el => el.click());
    await wait(200);

    const isFocus = await page.evaluate(() => document.body.classList.contains('focus-mode'));
    assert(isFocus, 'Focus mode deve essere attivo');

    // Verifica Focus Mode a schermo nero e testo bianco
    const focusBodyBg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    const focusTaColor = await page.$eval('#ta', el => getComputedStyle(el).color);
    console.log(`  Focus Mode Dark -> body bg: ${focusBodyBg}, text color: ${focusTaColor}`);
    assert(focusBodyBg.includes('0, 0, 0'), `In Focus Mode Dark lo sfondo deve essere nero puro (rgb(0, 0, 0)), trovato: ${focusBodyBg}`);
    assert(focusTaColor.includes('255, 255, 255'), `In Focus Mode Dark il testo deve essere bianco (rgb(255, 255, 255)), trovato: ${focusTaColor}`);

    console.log('[Test 4] Alternanza Dark/Light direttamente dal HUD di Focus Mode (#btnFocusTheme)...');
    await page.waitForSelector('#btnFocusTheme');
    await page.$eval('#btnFocusTheme', el => el.click());
    await wait(200);

    const isDarkInFocusNow = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(!isDarkInFocusNow, 'Il click su #btnFocusTheme deve disattivare la dark mode');

    // Ora lo sfondo di Focus Mode deve essere tornato all\'avorio chiaro
    const focusBodyBgLight = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    console.log(`  Focus Mode Light -> body bg: ${focusBodyBgLight}`);
    assert(!focusBodyBgLight.includes('0, 0, 0'), 'Lo sfondo di Focus Mode in Light non deve essere nero');

    // Riattiva Dark Mode dal HUD
    await page.$eval('#btnFocusTheme', el => el.click());
    await wait(200);
    const isDarkReactivated = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(isDarkReactivated, 'Il secondo click su #btnFocusTheme deve riattivare la dark mode');

    console.log('[Test 4b] Focus Frase e Paragrafo in Dark Mode: testo visibile, attivo bianco candido e sfumato soft...');
    await page.$eval('#btnFocusDim', el => el.click()); // Click -> Frase
    await wait(200);
    const isDimSentenceDark = await page.evaluate(() => document.body.classList.contains('focus-dim-active'));
    assert(isDimSentenceDark, 'focus-dim-active deve essere presente in Dark Mode');

    const darkActiveColor = await page.$eval('#focusBackdrop .focus-current', el => getComputedStyle(el).color);
    const darkActiveOpacity = await page.$eval('#focusBackdrop .focus-current', el => getComputedStyle(el).opacity);
    assert(darkActiveColor.includes('255, 255, 255'), `Frase attiva in Dark Mode deve essere bianca (#ffffff), trovato: ${darkActiveColor}`);
    assert.equal(darkActiveOpacity, '1', 'Frase attiva deve avere opacity 1');

    const taBgInDim = await page.$eval('#ta', el => getComputedStyle(el).backgroundColor);
    assert(taBgInDim.includes('rgba(0, 0, 0, 0)') || taBgInDim === 'transparent', `#ta deve avere background trasparente per non coprire il testo, trovato: ${taBgInDim}`);

    // Torna a Spento (Click 2 -> Paragrafo, Click 3 -> Spento)
    await page.$eval('#btnFocusDim', el => el.click());
    await wait(100);
    await page.$eval('#btnFocusDim', el => el.click());
    await wait(100);

    console.log('[Test 5] Uscita da Focus Mode: persistenza Dark Mode nella finestra principale...');
    await page.$eval('#btnFocusExit', el => el.click());
    await wait(200);

    const isDarkAfterFocusExit = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(isDarkAfterFocusExit, 'La Dark Mode deve persistere anche dopo essere usciti da Focus Mode');

    console.log('[Test 5b] Verifica anteprima a specchio scura in Dark Mode (non deve essere bianca)...');
    await page.$eval('#vwSplit', el => el.click());
    await wait(300);
    const prevWrapBg = await page.$eval('#prevWrap', el => getComputedStyle(el).backgroundColor);
    console.log(`  Anteprima split in Dark Mode -> wrap bg: ${prevWrapBg}`);
    assert(!prevWrapBg.includes('244, 240, 232') && !prevWrapBg.includes('255, 255, 255'), 'Sfondo #prevWrap in Dark Mode non deve essere bianco/chiaro');

    const prevFrameBodyBg = await page.evaluate(() => {
      const frame = document.getElementById('prevFrame');
      try {
        return frame.contentDocument.body ? getComputedStyle(frame.contentDocument.body).backgroundColor : '';
      } catch { return ''; }
    });
    console.log(`  Anteprima iframe body bg: ${prevFrameBodyBg}`);
    assert(!prevFrameBodyBg.includes('255, 253, 248') && !prevFrameBodyBg.includes('255, 255, 255'), 'Il corpo della preview non deve essere bianco (#fffdf8) in dark mode');

    console.log('[Test 6] Disattivazione Dark Mode dalla topbar...');
    await page.$eval('#btnToggleTheme', el => el.click());
    await wait(200);
    const isDarkFinally = await page.evaluate(() => document.body.classList.contains('dark-mode'));
    assert(!isDarkFinally, 'La dark mode deve disattivarsi al click del pulsante topbar');
    const finalSavedTheme = await page.evaluate(() => localStorage.getItem('pubcraft.theme'));
    assert.equal(finalSavedTheme, 'light', 'localStorage pubcraft.theme deve essere "light"');

    console.log('✔ TUTTI I TEST DELLA DARK MODE E FOCUS DARK SUPERATI CON SUCCESSO!');
    process.exit(0);
  } finally {
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    if (browser) {
      try { await browser.disconnect(); } catch {}
    }
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
  console.error('ERRORE TEST DARK MODE:', e);
  process.exit(1);
});

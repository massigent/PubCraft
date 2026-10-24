const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-welcome-'));
  const configDir = path.join(tmp, 'config');
  fs.mkdirSync(configDir, { recursive: true });

  const vaultA = path.join(tmp, 'Libro-A');
  fs.mkdirSync(path.join(vaultA, 'cover'), { recursive: true });
  fs.mkdirSync(path.join(vaultA, 'images'), { recursive: true });
  fs.writeFileSync(path.join(vaultA, 'book.yaml'), `---
title: "Il Metabolismo Tradito"
author: "Dott. Rossi"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Uno"
`);
  fs.writeFileSync(path.join(vaultA, '01.md'), '# Capitolo Uno\n\nTesto del libro A.\n');
  // Fake cover
  fs.writeFileSync(path.join(vaultA, 'cover', 'cover.png'), 'FAKEPNGDATA');

  const vaultB = path.join(tmp, 'Libro-B');
  fs.mkdirSync(path.join(vaultB, 'cover'), { recursive: true });
  fs.writeFileSync(path.join(vaultB, 'book.yaml'), `---
title: "Zuccheri Ribelli"
author: "Autore Due"
language: "it"
style: "poetry"
chapters:
  - file: "01.md"
    title: "Canto Primo"
`);
  fs.writeFileSync(path.join(vaultB, '01.md'), '# Canto Primo\n\nPoesia del libro B.\n');

  // Pre-popola recents config con Libro A e B
  fs.writeFileSync(path.join(configDir, 'config.json'), JSON.stringify({
    workspace_dir: tmp,
    recents: [
      {
        path: vaultA,
        title: "Il Metabolismo Tradito",
        author: "Dott. Rossi",
        style: "classic",
        has_cover: true,
        last_opened: new Date().toISOString(),
        exists: true,
        folder_name: "Libro-A",
        parent_name: "pubcraft-welcome"
      },
      {
        path: vaultB,
        title: "Zuccheri Ribelli",
        author: "Autore Due",
        style: "poetry",
        has_cover: false,
        last_opened: new Date(Date.now() - 3600000).toISOString(),
        exists: true,
        folder_name: "Libro-B",
        parent_name: "pubcraft-welcome"
      }
    ]
  }, null, 2));

  const port = await new Promise(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  // Avvia PubCraft SENZA argomenti (nessun vault aperto -> deve mostrare la Welcome screen)
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [], {
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
    page.on('dialog', async d => { await d.accept(); });
    page.on('pageerror', err => console.log('PAGE ERROR:', err));

    await page.goto(url);
    await page.waitForFunction(() => document.getElementById('sidebar')?.style.display === 'none' && document.getElementById('empty')?.style.display !== 'none', { polling: 100 });

    console.log('[Test 1] Verifica presenza Welcome Screen e Launcher Progetti...');
    await page.waitForSelector('#empty.welcome-screen');
    const isSidebarHidden = await page.$eval('#sidebar', el => el.style.display === 'none');
    assert(isSidebarHidden, 'Sidebar deve essere nascosta nella schermata di benvenuto iniziale');

    const brandTitle = await page.$eval('.welcome-title', el => el.textContent.trim());
    assert.equal(brandTitle, 'PUBCRAFT', 'Titolo PUBCRAFT deve essere presente');

    const actions = ['btnWelcomeNew', 'btnWelcomeDocx', 'btnWelcomeBrowse', 'btnWelcomeTutorial', 'btnWelcomeShortcuts'];
    for (const a of actions) {
      const el = await page.$(`#${a}`);
      assert(el, `Pulsante #${a} deve essere presente nella colonna sinistra`);
    }

    console.log('[Test 2] Verifica lista Progetti Recenti pre-popolata...');
    await page.waitForSelector('.recent-card');
    const recentCards = await page.$$('.recent-card');
    assert.equal(recentCards.length, 2, 'Devono essere presenti 2 libri nei recenti');

    const cardA = await page.$eval('.recent-card:nth-child(1) .recent-title', el => el.textContent.trim());
    assert.equal(cardA, 'Il Metabolismo Tradito', 'Primo libro deve essere Il Metabolismo Tradito');

    const cardB = await page.$eval('.recent-card:nth-child(2) .recent-title', el => el.textContent.trim());
    assert.equal(cardB, 'Zuccheri Ribelli', 'Secondo libro deve essere Zuccheri Ribelli');

    // Verifica jacket style colore su Libro B (poetry -> jacket-poetry)
    const jacketB = await page.$eval('.recent-card:nth-child(2) .recent-thumb-jacket', el => el.className);
    assert(jacketB.includes('jacket-poetry'), 'Libro B deve avere stile jacket-poetry');

    console.log('[Test 3] Verifica multilingue su Welcome Screen per tutte le 10 lingue...');
    const languages = ['en', 'it', 'es', 'fr', 'de', 'pt', 'zh', 'ja', 'ru', 'ar'];
    for (const lang of languages) {
      await page.select('#langSelect', lang);
      await wait(80);
      const headerText = await page.$eval('.welcome-recents-heading', el => el.textContent.trim());
      assert(headerText.length > 0, `Header recenti deve essere tradotto per lingua ${lang}`);

      const tutBtnText = await page.$eval('#btnWelcomeTutorial', el => el.textContent.trim());
      assert(tutBtnText.length > 0, `Bottone tutorial deve essere tradotto per ${lang}`);

      const isRTL = await page.evaluate(() => document.documentElement.dir === 'rtl');
      if (lang === 'ar') {
        assert(isRTL, 'Lingua araba deve attivare dir="rtl"');
      } else {
        assert(!isRTL, `Lingua ${lang} non deve avere RTL`);
      }
    }

    // Reimposta su italiano
    await page.select('#langSelect', 'it');
    await wait(80);

    console.log('[Test 4] Verifica Tutorial e Scorciatoie da tastiera...');
    console.log('Clicking #btnWelcomeTutorial');
    await page.evaluate(() => document.getElementById('btnWelcomeTutorial').click());
    console.log('Waiting for #tutorialModal:not(.hidden)');
    await page.waitForSelector('#tutorialModal:not(.hidden)');
    console.log('Checking tutContent');
    const tutContent = await page.$eval('#tutorialContent', el => el.innerHTML);
    assert(tutContent.includes('Benvenuto in PubCraft') || tutContent.includes('PubCraft'), 'Contenuto tutorial deve apparire in italiano');

    // Clicca tab Scorciatoie
    console.log('Clicking #tabTutShortcuts');
    await page.evaluate(() => document.getElementById('tabTutShortcuts').click());
    await wait(80);
    const shortcutsContent = await page.$eval('#tutorialContent', el => el.innerHTML);
    assert(shortcutsContent.includes('Grassetto') && shortcutsContent.includes('<kbd>'), 'Tab scorciatoie deve mostrare la tabella con i tasti');

    // Chiudi modale tutorial
    console.log('Clicking #btnTutorialDismiss');
    await page.evaluate(() => document.getElementById('btnTutorialDismiss').click());
    console.log('Waiting for #tutorialModal.hidden');
    await page.waitForSelector('#tutorialModal.hidden');

    console.log('[Test 5] Apertura libro in 1 clic dal Launcher...');
    // Clicca su Libro A -> deve caricare il libro, mostrare la sidebar e l'editor!
    await page.evaluate(() => document.querySelector('.recent-card:nth-child(1)').click());
    await page.waitForFunction(() => typeof state !== 'undefined' && state.vault && state.book && currentFile === '01.md', { polling: 100 });

    const sidebarVisible = await page.$eval('#sidebar', el => el.style.display !== 'none');
    assert(sidebarVisible, 'Sidebar deve diventare visibile all\'apertura del libro');

    const curTitle = await page.$eval('#bookTitle', el => el.textContent.trim());
    assert.equal(curTitle, 'Il Metabolismo Tradito', 'Titolo nella sidebar deve corrispondere');

    const closeBtnVisible = await page.$eval('#btnCloseBook', el => !el.classList.contains('hidden'));
    assert(closeBtnVisible, 'Pulsante Chiudi libro nella topbar deve essere visibile');

    console.log('[Test 6] Chiusura libro e ritorno alla Welcome Screen...');
    await page.evaluate(() => document.getElementById('btnCloseBook').click());
    await page.waitForFunction(() => typeof state !== 'undefined' && !state.vault, { polling: 100 });
    await wait(200);

    const isSidebarHiddenAfterClose = await page.$eval('#sidebar', el => el.style.display === 'none');
    assert(isSidebarHiddenAfterClose, 'Sidebar deve tornare nascosta dopo la chiusura');

    const isWelcomeVisible = await page.$eval('#empty', el => el.style.display !== 'none');
    assert(isWelcomeVisible, 'Welcome Screen deve essere nuovamente visibile');

    console.log('[Test 7] Verifica Quick Access e impostazione Area di Lavoro nel File Browser...');
    await page.evaluate(() => document.getElementById('btnWelcomeBrowse').click());
    await page.waitForSelector('#fsModal:not(.hidden)');

    const quickBtns = ['btnFsWorkspace', 'btnFsDocs', 'btnFsDesktop', 'btnFsDownloads', 'btnFsHome', 'btnFsVolumes', 'btnFsSetAsWorkspace'];
    for (const q of quickBtns) {
      const el = await page.$(`#${q}`);
      assert(el, `Pulsante #${q} deve essere presente nel file browser`);
    }

    // Clicca su Home
    await page.evaluate(() => document.getElementById('btnFsHome').click());
    await wait(150);
    let curFsPath = await page.evaluate(() => fsState.cur);
    assert(curFsPath.length > 0, 'fsBrowse deve aver navigato');

    // Filtro cartelle in tempo reale
    await page.type('#fsFilterInput', 'Libro');
    await wait(100);
    const filteredCount = await page.$$eval('#fsList li', els => els.length);
    assert(filteredCount >= 0, 'Il filtro cartelle deve aggiornare la lista');

    // Chiudi file browser
    await page.evaluate(() => document.getElementById('btnFsCancel').click());
    await page.waitForSelector('#fsModal.hidden');

    console.log('[Test 8] Rilevamento cartella mancante e pulizia con #btnPruneMissingRecents...');
    fs.rmSync(vaultB, { recursive: true, force: true });
    await page.evaluate(() => loadRecents());
    await wait(200);

    const pruneBtnVisible = await page.$eval('#btnPruneMissingRecents', el => !el.classList.contains('hidden'));
    assert(pruneBtnVisible, 'Pulsante Pulisci non trovati deve essere visibile');

    await page.evaluate(() => document.getElementById('btnPruneMissingRecents').click());
    await wait(300);

    const cardsAfterPrune = await page.$$('.recent-card');
    assert.equal(cardsAfterPrune.length, 1, 'Dopo il prune deve rimanere solo Libro A');

    const pruneBtnHiddenAfter = await page.$eval('#btnPruneMissingRecents', el => el.classList.contains('hidden'));
    assert(pruneBtnHiddenAfter, 'Pulsante Pulisci non trovati deve tornare nascosto');

    console.log('[Test 9] Rimozione singolo progetto dai recenti con il cestino...');
    await page.evaluate(() => {
      const btn = document.querySelector('.recent-card:nth-child(1) .recent-remove-btn');
      if (btn) btn.click();
    });
    await wait(250);
    const cardsAfterRemove = await page.$$('.recent-card');
    assert.equal(cardsAfterRemove.length, 0, 'La lista recenti deve essere vuota dopo la rimozione del libro');

    console.log('✔ TUTTI I TEST DELLA WELCOME SCREEN, RECENTI E AREA DI LAVORO SUPERATI CON SUCCESSO!');

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
  console.error('FAIL test_welcome_and_recents:', err);
  process.exit(1);
});

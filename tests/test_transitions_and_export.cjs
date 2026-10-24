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
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-transitions-test-'));
  const vaultA = path.join(tmp, 'vault-a');
  const vaultB = path.join(tmp, 'vault-b');
  const emptyDir = path.join(tmp, 'empty-dir');
  const corruptDir = path.join(tmp, 'corrupt-dir');
  const validC = path.join(tmp, 'vault-c');

  // Setup vaultA and vaultB with IDENTICAL 01.md
  for (const [dir, name] of [[vaultA, 'a'], [vaultB, 'b']]) {
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, 'book.yaml'), `title: Book ${name}\nchapters:\n- file: 01.md\n  title: Chapter 1\n`);
    fs.writeFileSync(path.join(dir, '01.md'), 'IDENTICAL_ORIGINAL');
  }

  // Empty folder
  fs.mkdirSync(emptyDir, { recursive: true });

  // Corrupt book.yaml folder
  fs.mkdirSync(corruptDir, { recursive: true });
  fs.writeFileSync(path.join(corruptDir, 'book.yaml'), ':::THIS IS CORRUPT NOT VALID YAML:::');

  // Valid third book
  fs.mkdirSync(validC, { recursive: true });
  fs.writeFileSync(path.join(validC, 'book.yaml'), 'title: Book C\nchapters:\n- file: 01.md\n  title: Chapter C1\n');
  fs.writeFileSync(path.join(validC, '01.md'), 'TEXT_OF_BOOK_C');

  const port = await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const value = server.address().port;
      server.close(() => resolve(value));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(path.join(root, 'build/pubcraftgui'), [vaultA], {
    env: { ...process.env, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser;
  let context;
  let page;

  try {
    let ready = false;
    for (let i = 0; i < 80; i++) {
      try { if ((await fetch(`${url}/api/state`)).ok) { ready = true; break; } } catch {}
      await wait(75);
    }
    assert(ready, 'Server GUI did not start in time');

    browser = await puppeteer.connect({ browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222' });
    context = await browser.createBrowserContext();
    page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', err => pageErrors.push(err.message));

    await page.goto(url);
    await page.waitForFunction(() => currentFile === '01.md');

    console.log('\n--- 1. TEST P0: Cambio progetto con capitoli identici e blocco transizione ---');
    // Test that typing during the switch cannot leak to Vault B
    const switchResult = await page.evaluate(async dest => {
      const originalApi = window.api;
      let entered, release;
      const started = new Promise(r => { entered = r; });
      const gate = new Promise(r => { release = r; });

      window.pickPath = async () => dest;
      window.api = async (path, opts) => {
        if (path === '/api/openat') {
          entered();
          await gate;
        }
        return originalApi(path, opts);
      };

      const switching = openVaultFolder();
      await started;

      const isReadOnlyDuringSwitch = ta.readOnly;
      const isSwitchingFlag = isProjectSwitching;
      // Tentativo di scrittura mentre la transizione è in volo:
      ta.value = 'EDIT_DURING_SWITCH';
      ta.dispatchEvent(new Event('input', { bubbles: true }));

      // Tentativo di invocare saveNow durante il cambio:
      const saveNowDuringSwitch = await saveNow();

      release();
      await switching;
      window.api = originalApi;

      return {
        isReadOnlyDuringSwitch,
        isSwitchingFlag,
        saveNowDuringSwitch,
        finalVault: state.vault,
        finalFile: currentFile,
        finalText: ta.value,
        finalDirty: dirty,
      };
    }, vaultB);

    console.log('Esito switch A -> B:', switchResult);
    assert.equal(switchResult.isReadOnlyDuringSwitch, true, 'Editor was not read-only during project transition');
    assert.equal(switchResult.isSwitchingFlag, true, 'isProjectSwitching flag was not active');
    assert.equal(switchResult.saveNowDuringSwitch, false, 'saveNow was allowed during transition');
    assert.equal(fs.readFileSync(path.join(vaultA, '01.md'), 'utf8'), 'IDENTICAL_ORIGINAL', 'Vault A was unexpectedly modified');
    assert.equal(fs.readFileSync(path.join(vaultB, '01.md'), 'utf8'), 'IDENTICAL_ORIGINAL', 'Vault B was corrupted by edits from Vault A');
    console.log('✔ P0 superato: nessuna perdita o corruzione tra progetti con file identici.');

    console.log('\n--- 2. TEST P0: Salvataggio modifiche pendenti prima del cambio progetto ---');
    await page.evaluate(async dest => {
      window.pickPath = async () => dest;
      ta.value = 'MODIFICA_EFFETTUATA_PRIMA_DELLO_SWITCH';
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      await openVaultFolder();
    }, vaultA);
    await page.waitForFunction(() => state.vault.endsWith('vault-a') && currentFile === '01.md');
    assert.equal(fs.readFileSync(path.join(vaultB, '01.md'), 'utf8'), 'MODIFICA_EFFETTUATA_PRIMA_DELLO_SWITCH');
    console.log('✔ P0 superato: modifiche pendenti correttamente salvate prima di inviare /api/openat.');

    console.log('\n--- 3. TEST P0: Salvataggio fallito blocca la transizione ---');
    const saveFailBlock = await page.evaluate(async dest => {
      const originalApi = window.api;
      window.pickPath = async () => dest;
      window.api = async (path, opts) => {
        if (path === '/api/save') {
          throw new Error('SIMULATED_SAVE_FAILURE_500');
        }
        return originalApi(path, opts);
      };

      ta.value = 'TESTO_NON_SALVABILE';
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      const switched = await openVaultFolder();
      window.api = originalApi;

      return {
        switched,
        vaultAfterFail: state.vault,
        currentFile,
        text: ta.value,
        dirty,
      };
    }, vaultB);

    console.log('Esito salvataggio fallito:', saveFailBlock);
    assert(saveFailBlock.vaultAfterFail.endsWith('vault-a'), 'Vault was changed despite save failure');
    assert.equal(saveFailBlock.dirty, true, 'Dirty state was lost on save failure');
    assert.equal(saveFailBlock.text, 'TESTO_NON_SALVABILE', 'Text was discarded on save failure');
    console.log('✔ P0 superato: salvataggio fallito blocca /api/openat e preserva testo e dirty state.');

    // Ripristina salvataggio su vaultA
    await page.evaluate(async () => {
      await saveNow();
    });

    console.log('\n--- 4. TEST P1: Esportazione attende il flush e genera artefatto reale ---');
    const exportResult = await page.evaluate(async () => {
      const apiCalls = [];
      const origApi = window.api;
      window.api = async (path, opts) => {
        apiCalls.push(path);
        return origApi(path, opts);
      };

      ta.value = '# Capitolo 1\nTESTO_AGGIORNATO_PER_LA_STAMPA';
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      // Avvia subito la build senza attendere il debounce di 800ms
      await runBuild('print', { trim: '6x9', pages: 100 });
      window.api = origApi;
      return { apiCalls, dirty };
    });

    console.log('Chiamate API durante export:', exportResult.apiCalls);
    const saveIdx = exportResult.apiCalls.indexOf('/api/save');
    const buildIdx = exportResult.apiCalls.indexOf('/api/build');
    assert(saveIdx !== -1, '/api/save was not called during runBuild');
    assert(buildIdx !== -1, '/api/build was not called');
    assert(saveIdx < buildIdx, `/api/save (${saveIdx}) was not executed before /api/build (${buildIdx})`);
    assert.equal(exportResult.dirty, false, 'dirty state was not cleared after build flush');

    // Verifica l'artefatto reale su disco
    const printHtmlPath = path.join(vaultA, 'out-stampa', 'print.html');
    assert(fs.existsSync(printHtmlPath), `Artefatto print.html non trovato in ${printHtmlPath}`);
    const printHtmlContent = fs.readFileSync(printHtmlPath, 'utf8');
    assert(printHtmlContent.includes('ESTO_AGGIORNATO_PER_LA_STAMPA'), 'Artefatto reale print.html non contiene il testo aggiornato salvato dalla build');
    console.log('✔ P1 superato: /api/save precede /api/build e l\'artefatto reale include le modifiche fresche.');

    console.log('\n--- 5. TEST P1: Salvataggio fallito blocca l\'esportazione ---');
    const buildSaveFail = await page.evaluate(async () => {
      const apiCalls = [];
      const origApi = window.api;
      window.api = async (path, opts) => {
        apiCalls.push(path);
        if (path === '/api/save') throw new Error('SIMULATED_BUILD_SAVE_ERROR');
        return origApi(path, opts);
      };
      ta.value = 'TESTO_BUILD_SAVE_FAIL';
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      await runBuild('print', { trim: '6x9', pages: 100 });
      window.api = origApi;
      return { apiCalls, dirty, text: ta.value };
    });
    console.log('Chiamate con save fallito prima della build:', buildSaveFail.apiCalls);
    assert.equal(buildSaveFail.apiCalls.includes('/api/build'), false, '/api/build was called despite save failure');
    assert.equal(buildSaveFail.dirty, true, 'dirty state was discarded');
    assert.equal(buildSaveFail.text, 'TESTO_BUILD_SAVE_FAIL', 'text was discarded');
    console.log('✔ P1 superato: salvataggio fallito impedisce la chiamata a /api/build.');

    // Salva per pulire stato
    await page.evaluate(async () => { await saveNow(); });

    console.log('\n--- 6. TEST P1: Apertura cartella vuota o senza book.yaml (Politica A) ---');
    // Tentativo apertura cartella vuota
    const emptyOpenResult = await page.evaluate(async dest => {
      window.pickPath = async () => dest;
      await openVaultFolder();
      return {
        vault: state.vault,
        bookTitle: state.book ? state.book.title : null,
        currentFile,
        text: ta.value,
        readOnly: ta.readOnly,
        docHidden: $('doc').hidden,
      };
    }, emptyDir);

    console.log('Esito apertura cartella vuota:', emptyOpenResult);
    assert(emptyOpenResult.vault.endsWith('vault-a'), 'Vault was switched to empty directory');
    assert.equal(emptyOpenResult.bookTitle, 'Book a', 'Book title was altered');
    assert.equal(emptyOpenResult.currentFile, '01.md', 'Current file was lost');
    assert.equal(emptyOpenResult.readOnly, false, 'Editor remained locked');
    assert.equal(emptyOpenResult.docHidden, false, 'Document became hidden');
    console.log('✔ P1 (Politica A) superato: cartella vuota rifiutata, vecchio progetto preservato al 100%.');

    console.log('\n--- 7. TEST P1: Apertura cartella con book.yaml malformato (Politica A) ---');
    const corruptOpenResult = await page.evaluate(async dest => {
      window.pickPath = async () => dest;
      await openVaultFolder();
      return {
        vault: state.vault,
        bookTitle: state.book ? state.book.title : null,
        currentFile,
        text: ta.value,
        readOnly: ta.readOnly,
        docHidden: $('doc').hidden,
      };
    }, corruptDir);

    console.log('Esito apertura cartella corrotta:', corruptOpenResult);
    assert(corruptOpenResult.vault.endsWith('vault-a'), 'Vault was switched to corrupt directory');
    assert.equal(corruptOpenResult.currentFile, '01.md', 'Current file was lost');
    assert.equal(corruptOpenResult.readOnly, false, 'Editor remained locked');
    console.log('✔ P1 (Politica A) superato: metadati malformati rifiutati, vecchio progetto intatto.');

    console.log('\n--- 8. TEST P1: Apertura libro valido successivo senza ricaricare la pagina ---');
    await page.evaluate(async dest => {
      window.pickPath = async () => dest;
      await openVaultFolder();
    }, validC);
    await page.waitForFunction(() => state.vault.endsWith('vault-c') && currentFile === '01.md');
    const validCState = await page.evaluate(() => ({
      vault: state.vault,
      bookTitle: state.book.title,
      currentFile,
      text: ta.value,
      readOnly: ta.readOnly,
      docHidden: $('doc').hidden,
    }));
    console.log('Esito apertura successiva valida (Vault C):', validCState);
    assert(validCState.vault.endsWith('vault-c'), 'Vault C not opened');
    assert.equal(validCState.bookTitle, 'Book C', 'Wrong book title');
    assert.equal(validCState.text, 'TEXT_OF_BOOK_C', 'Wrong chapter content loaded');
    assert.equal(validCState.readOnly, false, 'Editor is read-only');
    assert.equal(validCState.docHidden, false, 'Doc is hidden');
    console.log('✔ Apertura successiva libro valido funzionante senza ricaricare la pagina.');

    assert.deepEqual(pageErrors, [], 'Nessun errore imprevisto della pagina browser');
    console.log('\n==========================================================');
    console.log(' TUTTI I TEST TRANSIZIONI, BUILD E INVALID FOLDER SUPERATI! ');
    console.log('==========================================================');
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
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  process.exit(0);
})().catch(err => {
  console.error('FATAL TEST ERROR:', err);
  process.exit(1);
});

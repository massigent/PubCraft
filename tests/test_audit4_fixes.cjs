const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const assert = require('assert');
const net = require('net');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmpDir = fs.mkdtempSync('/tmp/pubcraft-e2e-');
  const vaultA = path.join(tmpDir, 'vaultA');
  const vaultB = path.join(tmpDir, 'vaultB');

  fs.mkdirSync(path.join(vaultA, 'cover'), { recursive: true });
  fs.mkdirSync(path.join(vaultA, 'images'), { recursive: true });
  fs.mkdirSync(path.join(vaultB, 'cover'), { recursive: true });
  fs.mkdirSync(path.join(vaultB, 'images'), { recursive: true });

  fs.writeFileSync(path.join(vaultA, 'book.yaml'), 'title: Vault A\nchapters:\n- file: 00.md\n  title: Intro A\n- file: 01.md\n  title: Cap A1\n- file: 02.md\n  title: Cap A2\n');
  fs.writeFileSync(path.join(vaultA, '00.md'), '# Intro A\nTesto iniziale A0');
  fs.writeFileSync(path.join(vaultA, '01.md'), '# Cap A1\nTesto iniziale A1');
  fs.writeFileSync(path.join(vaultA, '02.md'), '# Cap A2\nTesto iniziale A2');

  fs.writeFileSync(path.join(vaultB, 'book.yaml'), 'title: Vault B\nchapters:\n- file: 00.md\n  title: Intro B\n- file: 01.md\n  title: Cap B1\n- file: 02.md\n  title: Cap B2\n');
  fs.writeFileSync(path.join(vaultB, '00.md'), '# Intro B\nTesto iniziale B0');
  fs.writeFileSync(path.join(vaultB, '01.md'), '# Cap B1\nCONTENUTO_vaultB_01');
  fs.writeFileSync(path.join(vaultB, '02.md'), '# Cap B2\nTesto iniziale B2');

  // Avvia pubcraftgui su porta dinamica
  const port = await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => { const port = server.address().port; server.close(() => resolve(port)); });
  });
  const guiProc = spawn(path.join(root, 'build/pubcraftgui'), [vaultA], {
    env: { ...process.env, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  const url = `http://127.0.0.1:${port}`;

  // Attendi che il server risponda
  let serverOk = false;
  for (let i = 0; i < 50; i++) {
    try {
      const res = await fetch(`${url}/api/state`);
      if (res.ok) { serverOk = true; break; }
    } catch (_) {}
    await wait(100);
  }
  if (!serverOk) {
    guiProc.kill();
    throw new Error('Server pubcraftgui non avviato su porta ' + port);
  }

  console.log('✔ Server avviato su ' + url);

  const api = async (p, body) => {
    const r = await fetch(url + p, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return r.json();
  };

  const browser = await puppeteer.connect({
    browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
    defaultViewport: null,
  });

  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport({ width: 1280, height: 800 });
  page.on('dialog', (d) => d.accept());

  let exportedReportPath = null;
  try {
    console.log('Before page.goto to', url);
    await page.goto(url);
    console.log('After page.goto');
    await page.waitForFunction(() => currentFile === '00.md');

    let mode = 'save';
    let pending = [];
    await page.setRequestInterception(true);
    const handler = (r) => {
      if (mode === 'save' && r.url().endsWith('/api/save')) {
        pending.push(r);
      } else if (mode === 'chapter' && r.url().includes('/api/chapter?file=')) {
        pending.push(r);
      } else {
        r.continue();
      }
    };
    page.on('request', handler);

    const nextReq = async () => {
      for (let i = 0; i < 150 && pending.length === 0; i++) await wait(20);
      if (!pending.length) throw Error('Nessuna richiesta intercettata');
      return pending.shift();
    };

    const replayReq = async (r) => {
      const response = await fetch(r.url(), {
        method: r.method(),
        headers: { 'Content-Type': 'application/json' },
        ...(r.postData() ? { body: r.postData() } : {}),
      });
      await r.respond({
        status: response.status,
        contentType: 'application/json',
        body: await response.text(),
      });
    };

    // -------------------------------------------------------------------------
    // TEST 1: Salvataggi concorrenti / serializzazione
    // -------------------------------------------------------------------------
    console.log('\n--- Test 1: Salvataggi concorrenti ---');
    // Avvia saveNow per OLD_REVISION
    await page.evaluate(() => {
      ta.value = 'OLD_REVISION';
      queueSave();
      clearTimeout(saveTimer);
      window.__save1 = saveNow();
    });
    const s1 = await nextReq();

    // Utente digita NEW_REVISION e preme Cmd+S mentre il primo salvataggio è ancora in volo
    await page.evaluate(() => {
      ta.value = 'NEW_REVISION';
      queueSave();
      clearTimeout(saveTimer);
      window.__save2 = saveNow();
    });

    // Con la nuova coda seriale: s2 non viene inviata finché s1 non si conclude!
    // Completiamo s1
    await replayReq(s1);
    await page.evaluate(() => window.__save1);

    // Ora la coda seriale invia s2 con la nuova revisione
    const s2 = await nextReq();
    await replayReq(s2);
    await page.evaluate(() => window.__save2);

    const diskA0 = fs.readFileSync(path.join(vaultA, '00.md'), 'utf8');
    const editorState = await page.evaluate(() => ({
      editor: ta.value,
      dirty,
      saveRevision,
      lastSavedRevision,
    }));
    console.log('Stato editor:', editorState);
    console.log('Contenuto disco:', diskA0);

    if (diskA0 !== 'NEW_REVISION') {
      throw new Error(`FALLITO: Il disco contiene '${diskA0}', atteso 'NEW_REVISION'!`);
    }
    console.log('✔ Test 1 superato: nessuna perdita di modifiche concorrenti, il disco ha NEW_REVISION.');

    // -------------------------------------------------------------------------
    // TEST 2: Navigazione capitoli rapida fuori ordine
    // -------------------------------------------------------------------------
    console.log('\n--- Test 2: Navigazione capitoli rapida ---');
    mode = 'pass';
    await page.evaluate(async () => {
      dirty = false;
      clearTimeout(saveTimer);
      currentFile = '';
      await openChapter('00.md');
    });

    mode = 'chapter';
    // Utente apre cap 01.md (lento)
    await page.evaluate(() => { window.__open1 = openChapter('01.md'); });
    const c1 = await nextReq();

    // Subito dopo apre cap 02.md (veloce)
    await page.evaluate(() => { window.__open2 = openChapter('02.md'); });
    const c2 = await nextReq();

    // La risposta di 02 arriva prima
    await replayReq(c2);
    await page.evaluate(() => window.__open2);

    // La risposta lenta di 01 arriva dopo: DEVE essere ignorata!
    await replayReq(c1);
    await page.evaluate(() => window.__open1);

    const chapterState = await page.evaluate(() => ({
      currentFile,
      text: ta.value,
      currentTitle,
    }));
    console.log('Stato navigazione:', chapterState);

    if (chapterState.currentFile !== '02.md' || !chapterState.text.includes('A2')) {
      throw new Error(`FALLITO: Risposta obsoleta di 01.md ha sovrascritto 02.md!`);
    }

    // Modifica e salva 02.md
    mode = 'pass';
    await page.evaluate(async () => {
      ta.value += '\nMODIFICA SU 02';
      queueSave();
      clearTimeout(saveTimer);
      await saveNow();
    });

    const disk02 = fs.readFileSync(path.join(vaultA, '02.md'), 'utf8');
    const disk01 = fs.readFileSync(path.join(vaultA, '01.md'), 'utf8');
    console.log('Contenuto 02.md:', disk02);
    console.log('Contenuto 01.md:', disk01);

    if (disk02.includes('A1') || !disk02.includes('MODIFICA SU 02')) {
      throw new Error(`FALLITO: Il capitolo 02.md è stato corrotto con i dati di 01.md!`);
    }
    console.log('✔ Test 2 superato: risposte obsolete ignorate con successo.');

    // Reopening a previously saved chapter must use the server version, not a reset counter.
    mode = 'pass';
    await page.evaluate(async () => {
      await openChapter('00.md');
      ta.value = 'EDIT_AFTER_REOPEN'; queueSave(); clearTimeout(saveTimer);
      if (!(await saveNow())) throw new Error('Save after reopen failed');
    });
    if (fs.readFileSync(path.join(vaultA, '00.md'), 'utf8') !== 'EDIT_AFTER_REOPEN') throw new Error('Reopen lost edit');
    console.log('✔ Reopen and save regression passed.');

    // Actual keyboard input must be prevented while the chapter GET is pending.
    mode = 'chapter';
    await page.evaluate(() => { window.__loading = openChapter('01.md'); });
    const loadingReq = await nextReq();
    if (!(await page.$eval('#ta', el => el.readOnly))) throw new Error('Editor still writable during load');
    const beforeTyping = await page.$eval('#ta', el => el.value);
    await page.type('#ta', 'MUST_NOT_BE_INSERTED');
    if ((await page.$eval('#ta', el => el.value)) !== beforeTyping) throw new Error('Typing during load was accepted');
    await replayReq(loadingReq); await page.evaluate(() => window.__loading);
    if (await page.$eval('#ta', el => el.readOnly)) throw new Error('Editor remained locked after load');
    console.log('✔ Read-only loading regression passed.');

    // -------------------------------------------------------------------------
    // TEST 3: Isolamento build cross-vault
    // -------------------------------------------------------------------------
    console.log('\n--- Test 3: Isolamento build cross-vault ---');
    mode = 'pass';
    while (pending.length > 0) {
      try { pending.shift().continue(); } catch (_) {}
    }
    page.off('request', handler);
    await page.setRequestInterception(false);

    // Crea out-stampa in vaultA con un file print.html
    const outA = path.join(vaultA, 'out-stampa');
    fs.mkdirSync(outA, { recursive: true });
    fs.writeFileSync(path.join(outA, 'print.html'), '<html><body>CONTENUTO_vaultA_01</body></html>');

    // Simula che mentre vaultA era aperto viene memorizzato l'artefatto di A
    await fetch(`${url}/api/openat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: vaultA }),
    });

    // Ora passiamo a vaultB
    await fetch(`${url}/api/openat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: vaultB }),
    });

    // Richiediamo /api/viewprint/: NON deve restituire il contenuto di vaultA!
    const resView = await fetch(`${url}/api/viewprint/`);
    const text = await resView.text();
    console.log('Status /api/viewprint/ con vaultB attivo:', resView.status);

    if (text.includes('CONTENUTO_vaultA_01')) {
      throw new Error('FALLITO: Fuga di dati cross-vault! Anteprima di vaultA servita con vaultB attivo!');
    }
    console.log('✔ Test 3 superato: isolamento rigoroso tra progetti, nessun leak cross-vault.');

    console.log('\n--- Test 4: Esportazione report diagnostico e conferma sovrascrittura ---');
    await page.goto(url);
    await page.waitForFunction(() => typeof openReportIssueModal === 'function' && typeof exportIssueReport === 'function', { polling: 100 });
    await page.evaluate(() => openReportIssueModal());
    await page.waitForFunction(() => !document.querySelector('#reportIssueModal').classList.contains('hidden'), { polling: 100 });
    await page.type('#reportDesc', 'Descrizione errore per test esportazione');
    await page.evaluate(() => prepareIssueReport());
    await page.waitForFunction(() => typeof currentReportId !== 'undefined' && currentReportId !== '', { polling: 100 });
    const reportId = await page.evaluate(() => currentReportId);
    exportedReportPath = path.join(require('os').homedir(), `pubcraft-report-${reportId}.md`);

    // Prima esportazione: il file non esiste ancora, deve esportare con successo
    await page.evaluate(async () => { await exportIssueReport(false); });
    await page.waitForFunction(() => !document.querySelector('#modal').classList.contains('hidden'), { polling: 100 });
    const modalText1 = await page.$eval('#modalOut', el => el.textContent);
    assert(modalText1.includes('salvato con successo') || modalText1.includes('Rapporto Esportato'), 'Prima esportazione deve riuscire');
    await page.evaluate(() => hideModal());
    await page.waitForFunction(() => document.querySelector('#modal').classList.contains('hidden'), { polling: 100 });

    // Seconda esportazione: il file ESISTE già. Non deve saltare la conferma!
    await page.evaluate(async () => { await exportIssueReport(false); });
    await page.waitForFunction(() => !document.querySelector('#modal').classList.contains('hidden'), { polling: 100 });
    const modalTitle2 = await page.$eval('#modalTitle', el => el.textContent);
    const modalMsg2 = await page.$eval('#modalOut', el => el.textContent);
    assert.equal(modalTitle2, 'File già esistente', 'Deve comparire il modal di conferma sovrascrittura');
    assert(modalMsg2.includes('Vuoi sovrascrivere il file esistente?'), 'Il testo deve chiedere la conferma di sovrascrittura');
    console.log('✔ Test 4 superato: conferma sovrascrittura richiesta correttamente (nessun bypass involontario).');
    await page.evaluate(() => hideModal());
    await page.evaluate(() => hideReportIssueModal());
    if (exportedReportPath && fs.existsSync(exportedReportPath)) {
      try { fs.unlinkSync(exportedReportPath); } catch (_) {}
    }

    if (process.env.CHROMIUM_BIN) {
      await api('/api/openat', { path: vaultA });
      const exported = await api('/api/build', { kind: 'print', trim: '5x8', bleed: false, outdir: path.join(tmpDir, 'external-output # proof') });
      if (!exported.ok || !exported.pdf_created) throw new Error('PDF export failed: ' + JSON.stringify(exported));
      if ((await fetch(url + '/api/viewprint/')).status !== 200) throw new Error('External preview rejected');
      const geometry = () => page.evaluate(async () => {
        const response = await fetch('/api/watch/pdf');
        if (!response.ok) throw new Error(await response.text());
        const pdf = await pdfjsLib.getDocument({data: await response.arrayBuffer()}).promise;
        const viewport = (await pdf.getPage(1)).getViewport({scale: 1});
        const size = [viewport.width, viewport.height]; await pdf.destroy(); return size;
      });
      const first = await geometry();
      fs.appendFileSync(path.join(vaultA, '01.md'), '\nWATCH_UPDATED_TEXT');
      const second = await geometry();
      for (const size of [first, second]) if (Math.abs(size[0]-360)>0.2 || Math.abs(size[1]-576)>0.2) throw new Error('Watch format drift: '+JSON.stringify(size));
      console.log('✔ External preview and Watch retain real 5x8 PDF dimensions.');
    } else {
      console.log('SKIP real PDF E2E: set CHROMIUM_BIN (see tests/README.md).');
    }

    console.log('\n==========================================================');
    console.log(' TUTTI I TEST END-TO-END BROWSER SUPERATI CON SUCCESSO! ');
    console.log('==========================================================\n');
  } finally {
    if (exportedReportPath && fs.existsSync(exportedReportPath)) {
      try { fs.unlinkSync(exportedReportPath); } catch (_) {}
    }
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    await browser.disconnect().catch(() => {});
    if (guiProc) {
      await new Promise(resolve => {
        guiProc.on('exit', resolve);
        guiProc.kill();
        setTimeout(resolve, 2000);
      });
    }
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});

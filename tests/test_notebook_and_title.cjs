const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-nb-'));
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Titolo Originale del Libro"
author: "Autore Test"
language: "it"
style: "classic"
chapters:
  - file: "01.md"
    title: "Capitolo Uno"
`);

  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo Uno\n\nTesto base del capitolo iniziale.\n');

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
    page.on('console', msg => console.log('PAGE LOG:', msg.text()));
    page.on('pageerror', err => console.log('PAGE ERROR:', err));
    await page.setViewport({ width: 1280, height: 800 });

    await page.goto(url);
    await page.waitForSelector('#topbarBookTitle');

    console.log('[Test 1] Modifica titolo in-place nella topbar...');
    const initialTitle = await page.$eval('#topbarBookTitle', el => el.textContent.trim());
    assert.strictEqual(initialTitle, 'Titolo Originale del Libro');

    // Clicca per attivare l'editing
    await page.$eval('#topbarBookTitle', el => el.click());
    const isInputVisible = await page.$eval('#topbarBookTitleInput', el => !el.classList.contains('hidden'));
    assert(isInputVisible, 'Input titolo dovrebbe essere visibile dopo il click');

    // Cambia il titolo e premi Invio
    await page.click('#topbarBookTitleInput', { clickCount: 3 });
    await page.type('#topbarBookTitleInput', 'Nuovo Romanzo Segreto');
    await page.keyboard.press('Enter');
    await wait(300);

    const updatedTopbarTitle = await page.$eval('#topbarBookTitle', el => el.textContent.trim());
    const updatedSidebarTitle = await page.$eval('#bookTitle', el => el.textContent.trim());
    assert.strictEqual(updatedTopbarTitle, 'Nuovo Romanzo Segreto');
    assert.strictEqual(updatedSidebarTitle, 'Nuovo Romanzo Segreto');

    const yamlContent = fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8');
    assert(yamlContent.includes('Nuovo Romanzo Segreto'), 'book.yaml su disco deve contenere il nuovo titolo');
    console.log('  ✔ Titolo rinominato con successo in-place e salvato su book.yaml.');

    console.log('[Test 2] Apertura taccuino e creazione nota...');
    await page.$eval('#btnToggleNotebook', el => el.click());
    await wait(300);

    const isDrawerOpen = await page.$eval('#notebookDrawer', el => !el.classList.contains('hidden'));
    assert(isDrawerOpen, 'Il cassetto del taccuino deve aprirsi');

    await page.focus('#noteTitleInput');
    await page.type('#noteTitleInput', 'Pista Indizi');
    await page.focus('#noteContentInput');
    await page.type('#noteContentInput', 'La busta sigillata conteneva la mappa del tesoro.');

    const values = await page.evaluate(() => ({
      title: document.getElementById('noteTitleInput')?.value,
      content: document.getElementById('noteContentInput')?.value,
      currentNoteFile: typeof currentNoteFile !== 'undefined' ? currentNoteFile : null,
      noteDirty: typeof noteDirty !== 'undefined' ? noteDirty : null,
      noteRevision: typeof noteRevision !== 'undefined' ? noteRevision : null,
    }));
    console.log('NOTE VALUES AFTER TYPE:', values);

    // Salva esplicitamente o attendi
    await page.evaluate(async () => {
      await saveCurrentNote();
    });
    await wait(300);

    const notesDir = path.join(vault, 'notes');
    assert(fs.existsSync(notesDir), 'La cartella notes/ deve esistere');
    const noteFiles = fs.readdirSync(notesDir).filter(f => f.endsWith('.md'));
    assert.strictEqual(noteFiles.length, 1, 'Deve essere stata salvata esattamente 1 nota su disco');
    const savedNoteText = fs.readFileSync(path.join(notesDir, noteFiles[0]), 'utf8');
    assert(savedNoteText.includes('Pista Indizi'));
    assert(savedNoteText.includes('La busta sigillata conteneva la mappa del tesoro.'));
    console.log('  ✔ Nota creata, digitata e salvata su disco in notes/*.md.');

    console.log('[Test 3] Inserimento nota nel manoscritto...');
    // Clicca "Inserisci nel manoscritto"
    await page.$eval('#btnInsertNoteToChapter', el => el.click());
    await wait(500);

    const editorValue = await page.$eval('#ta', el => el.value);
    assert(editorValue.includes('La busta sigillata conteneva la mappa del tesoro.'), 'Il contenuto della nota deve essere inserito nell\'editor');
    console.log('  ✔ Testo della nota inserito correttamente nel capitolo corrente.');

    console.log('[Test 4] Conversione nota in capitolo ufficiale...');
    await page.$eval('#btnConvertNoteToChapter', el => el.click());
    await wait(200);

    // Clicca conferma nella modale
    const modalButtons = await page.$$('#modalActions button');
    let convertBtn = null;
    for (const b of modalButtons) {
      const text = await page.evaluate(el => el.textContent, b);
      if (text.includes('Convert') || text.includes('Converti')) {
        convertBtn = b;
        break;
      }
    }
    assert(convertBtn, 'Pulsante di conferma conversione deve essere presente');
    await page.evaluate(el => el.click(), convertBtn);
    await wait(600);

    // Verifica che ora ci siano 2 capitoli
    const chaptersCount = await page.$$eval('#chapterList li', els => els.length);
    assert.strictEqual(chaptersCount, 2, 'Devono esserci 2 capitoli dopo la conversione della nota');

    const updatedYaml = fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8');
    assert(updatedYaml.includes('Pista Indizi'), 'Il nuovo capitolo deve essere inserito in book.yaml');
    console.log('  ✔ Nota convertita in capitolo ufficiale con successo!');

    console.log('[Test 5] Creazione ed eliminazione nota...');
    await page.$eval('#btnToggleNotebook', el => el.click());
    await wait(200);
    await page.$eval('#btnDrawerNewNote', el => el.click());
    await wait(300);
    await page.focus('#noteTitleInput');
    await page.evaluate(() => { const el = document.getElementById('noteTitleInput'); el.value = ''; });
    await page.type('#noteTitleInput', 'Appunto Temporaneo');
    await wait(800);

    // Clicca elimina
    await page.$eval('#btnDeleteNote', el => el.click());
    await wait(200);
    const deleteModalButtons = await page.$$('#modalActions button');
    let deleteConfirmBtn = null;
    for (const b of deleteModalButtons) {
      const text = await page.evaluate(el => el.textContent, b);
      if (text.includes('Delete') || text.includes('Elimina')) {
        deleteConfirmBtn = b;
        break;
      }
    }
    assert(deleteConfirmBtn, 'Pulsante conferma eliminazione deve essere presente');
    await page.evaluate(el => el.click(), deleteConfirmBtn);
    await wait(500);

    const remainingNotes = fs.readdirSync(notesDir).filter(f => f.endsWith('.md'));
    console.log('Remaining notes files:', remainingNotes);
    assert.strictEqual(remainingNotes.length, 0, 'La nota eliminata non deve più esistere su disco');
    console.log('  ✔ Nota eliminata con successo dal taccuino e dal disco.');

    console.log('[Test 6a] Verifica pulsante "Inserisci nel manoscritto" in Dark Mode...');
    await page.evaluate(() => {
      openNotebookDrawer();
      document.body.classList.add('dark-mode');
    });
    await wait(200);
    const insertBtnStyle = await page.$eval('#btnInsertNoteToChapter', el => {
      const cs = window.getComputedStyle(el);
      return { bg: cs.backgroundColor, color: cs.color };
    });
    console.log('Insert note btn style in Dark Mode:', insertBtnStyle);
    // Non deve essere sfondo bianco né testo bianco su bianco
    assert.notEqual(insertBtnStyle.bg, 'rgb(255, 255, 255)', 'Sfondo non deve essere bianco');
    assert.notEqual(insertBtnStyle.bg, 'rgb(242, 240, 236)', 'Sfondo non deve essere var(--ink) chiaro');
    assert.notEqual(insertBtnStyle.color, 'rgb(255, 255, 255)', 'Testo non deve essere bianco su sfondo chiaro');
    assert.equal(insertBtnStyle.bg, 'rgb(216, 168, 90)', 'Sfondo deve essere oro accent #d8a85a in tono con l\'app');
    assert.equal(insertBtnStyle.color, 'rgb(18, 18, 18)', 'Testo deve essere scuro #121212 per massima leggibilità');
    console.log('  ✔ Pulsante Inserisci nel manoscritto in dark mode ha contrasto e colore oro corretti.');
    await page.evaluate(() => document.body.classList.remove('dark-mode'));

    console.log('[Test 6b] Verifica che "Nuova nota" preserva la bozza se il salvataggio fallisce...');
    await page.focus('#noteTitleInput');
    await page.evaluate(() => { const el = document.getElementById('noteTitleInput'); el.value = ''; });
    await page.type('#noteTitleInput', 'Bozza Preziosa Da Non Perdere');
    await page.focus('#noteContentInput');
    await page.evaluate(() => { const el = document.getElementById('noteContentInput'); el.value = ''; });
    await page.type('#noteContentInput', 'Contenuto vitale della bozza...');
    // Simula fallimento salvataggio note
    await page.evaluate(() => {
      window._origApi = window.api;
      window.api = async (path, opts) => {
        if (path === '/api/note') throw new Error('SIMULATED_NOTE_SAVE_FAILURE');
        return window._origApi(path, opts);
      };
    });
    // Invoca newNote()
    await page.evaluate(() => newNote());
    await wait(400);

    // Deve comparire il modal di salvataggio fallito
    const modalVisible = await page.$eval('#modal', el => !el.classList.contains('hidden'));
    assert.equal(modalVisible, true, 'Il modal di errore salvataggio deve apparire');
    // Chiudi il modal
    await page.evaluate(() => hideModal());

    // Verifica che il testo della bozza NON sia andato perso
    const preservedTitle = await page.$eval('#noteTitleInput', el => el.value);
    const preservedContent = await page.$eval('#noteContentInput', el => el.value);
    assert.equal(preservedTitle, 'Bozza Preziosa Da Non Perdere', 'Il titolo della bozza non deve essere perso');
    assert.equal(preservedContent, 'Contenuto vitale della bozza...', 'Il contenuto della bozza non deve essere perso');
    console.log('  ✔ Bozza preservata intatta dopo fallimento salvataggio su Nuova nota.');

    // Ripristina api
    await page.evaluate(() => {
      window.api = window._origApi;
      delete window._origApi;
    });

    console.log('[Test 6c] Verifica che il caricamento lento di una nota blocca gli input e non sovrascrive il file di destinazione (Bug 1)...');
    // Prepara due note distinte
    fs.writeFileSync(path.join(notesDir, 'one.md'), '---\ntitle: Nota Uno\n---\n\nORIGINAL_ONE\n');
    fs.writeFileSync(path.join(notesDir, 'two.md'), '---\ntitle: Nota Due\n---\n\nORIGINAL_TWO\n');
    await page.evaluate(() => loadNotes());
    await wait(200);

    // Apri prima la nota one.md
    await page.evaluate(() => openNote('one.md'));
    await wait(300);
    const contentOne = await page.$eval('#noteContentInput', el => el.value);
    assert.equal(contentOne, 'ORIGINAL_ONE', 'one.md deve essere caricata');

    // Ora simula GET lenta su two.md
    await page.evaluate(() => {
      window._origApi = window.api;
      window.api = async (url, opts) => {
        if (url.includes('file=two.md')) {
          await new Promise(r => setTimeout(r, 600));
        }
        return window._origApi(url, opts);
      };
    });

    // Avvia apertura di two.md
    const openPromise = page.evaluate(() => openNote('two.md'));
    await wait(100);

    // Durante il caricamento, gli input devono essere readOnly=true
    const isLocked = await page.$eval('#noteContentInput', el => el.readOnly);
    assert.strictEqual(isLocked, true, 'L input della nota deve essere bloccato in sola lettura durante il caricamento');

    // Prova a forzare un salvataggio mentre carica: deve essere inibito
    const saveBlocked = await page.evaluate(() => saveCurrentNote());
    assert.strictEqual(saveBlocked, false, 'saveCurrentNote deve rifiutare il salvataggio durante il caricamento');

    await openPromise;
    await wait(200);

    // Ripristina api
    await page.evaluate(() => {
      window.api = window._origApi;
      delete window._origApi;
    });

    // Verifica che two.md contenga il suo contenuto e che su disco non sia stato corrotto
    const diskContentTwo = fs.readFileSync(path.join(notesDir, 'two.md'), 'utf8');
    assert(diskContentTwo.includes('ORIGINAL_TWO'), 'Il file two.md su disco NON deve essere stato corrotto');
    assert(!diskContentTwo.includes('ORIGINAL_ONE'), 'Il file two.md NON deve contenere il testo di one.md');
    console.log('  ✔ Caricamento lento gestito correttamente con blocco input e nessuna corruzione disco.');

    try { fs.unlinkSync(path.join(notesDir, 'one.md')); } catch {}
    try { fs.unlinkSync(path.join(notesDir, 'two.md')); } catch {}

    console.log('[Test 6] Verifica salvataggio nota alla chiusura del libro (A01)...');
    await page.evaluate(() => openNotebookDrawer());
    await wait(200);
    await page.$eval('#btnDrawerNewNote', el => el.click());
    await wait(300);
    await page.focus('#noteTitleInput');
    await page.evaluate(() => { const el = document.getElementById('noteTitleInput'); el.value = ''; });
    await page.type('#noteTitleInput', 'Nota Prima Di Chiudere');
    await page.focus('#noteContentInput');
    await page.evaluate(() => { const el = document.getElementById('noteContentInput'); el.value = ''; });
    await page.type('#noteContentInput', 'Testo nota che DEVE essere salvato prima della chiusura!');
    // Chiudi il libro IMMEDIATAMENTE senza attendere il timer di autosave
    await page.evaluate(async () => {
      await closeCurrentBook();
    });
    await wait(800);

    const docState = await page.evaluate(() => ({
      vault: (typeof state !== 'undefined' && state && state.vault) || '',
      docHidden: $('doc') ? $('doc').hidden : null,
      docDisplay: $('doc') ? $('doc').style.display : null,
      docClasses: $('doc') ? $('doc').className : null,
    }));
    console.log('Doc state after closeCurrentBook:', docState);

    // Verifica che #doc sia nascosto e launcher visibile (A09)
    const isDocHidden = await page.$eval('#doc', el => el.hidden || el.classList.contains('hidden') || el.style.display === 'none');
    assert.strictEqual(isDocHidden, true, '#doc deve essere nascosto dopo la chiusura del libro');

    // Verifica che la nota sia stata effettivamente salvata e persistita su disco (A01)
    const savedNotes = fs.readdirSync(notesDir).filter(f => f.endsWith('.md'));
    assert(savedNotes.length >= 1, 'La nota deve essere salvata su disco prima di chiudere il libro');
    const noteFound = savedNotes.some(f => fs.readFileSync(path.join(notesDir, f), 'utf8').includes('Testo nota che DEVE essere salvato'));
    assert(noteFound, 'Il contenuto della nota deve essere salvato su disco');
    console.log('  ✔ Nota persistita correttamente su disco prima della chiusura del libro (A01 superato)!');

    console.log('==========================================================');
    console.log(' TUTTI I TEST DEL TACCUINO E DEL TITOLO SUPERATI CON SUCCESSO! ');
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
    try {
      fs.rmSync(tmp, { recursive: true, force: true });
    } catch (_) {}
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});

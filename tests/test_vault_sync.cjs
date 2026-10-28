// Sincronizzazione cartella ↔ book.yaml (politica non distruttiva):
// i .md aggiunti o rinominati fuori da PubCraft devono comparire da soli,
// i file mancanti devono essere segnalati senza sparire dall'elenco.
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const assert = require('assert');

const wait = ms => new Promise(r => setTimeout(r, ms));
const SETTLE = 2600; // poll 400ms + debounce 700ms + margine

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join('/tmp', 'pubcraft-sync-'));
  const vault = path.join(tmp, 'testbook');
  fs.mkdirSync(vault);

  const chapter = (id, title) => `---\nid: ${id}\ntitle: ${title}\n---\n\n# ${title}\n\nTesto del capitolo ${title}.\n`;
  fs.writeFileSync(path.join(vault, 'book.yaml'), `---
title: "Libro Sincronizzato"
author: "Autore"
language: "it"
style: "classico"
chapters:
  - file: "01.md"
    id: "id-01"
    title: "Primo"
  - file: "02.md"
    id: "id-02"
    title: "Secondo"
`);
  fs.writeFileSync(path.join(vault, '01.md'), chapter('id-01', 'Primo'));
  fs.writeFileSync(path.join(vault, '02.md'), chapter('id-02', 'Secondo'));

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

  const listFiles = page => page.$$eval('#chapterList li', els => els.map(e => e.dataset.file));
  const yamlFiles = () => (fs.readFileSync(path.join(vault, 'book.yaml'), 'utf8').match(/file:\s*"?([^"\n]+)"?/g) || [])
    .map(s => s.replace(/file:\s*"?/, '').replace(/"$/, '').trim());

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
      browser = await puppeteer.launch({ executablePath: bravePath, headless: true, args: ['--no-sandbox', '--disable-gpu'] });
      page = await browser.newPage();
    } else {
      throw new Error('No browser available for test');
    }

    page.on('pageerror', err => console.log('PAGE ERROR:', err));
    await page.setViewport({ width: 1280, height: 900 });
    await page.goto(url, { waitUntil: 'networkidle0' });
    await wait(600);

    console.log('[Test 1] Stato iniziale: solo i capitoli di book.yaml...');
    assert.deepStrictEqual(await listFiles(page), ['01.md', '02.md'], 'Devono esserci i 2 capitoli iniziali');
    console.log('  ✔ 2 capitoli.');

    console.log('[Test 2] Aggiungo un file .md nella cartella mentre PubCraft è aperto...');
    fs.writeFileSync(path.join(vault, '03.md'), chapter('id-03', 'Terzo aggiunto a mano'));
    await wait(SETTLE);

    assert.deepStrictEqual(await listFiles(page), ['01.md', '02.md', '03.md'],
      'Il nuovo file deve comparire in fondo senza ricaricare la pagina');
    assert.deepStrictEqual(yamlFiles(), ['01.md', '02.md', '03.md'], 'book.yaml deve elencare il nuovo capitolo');
    const title = await page.$eval('#chapterList li[data-file="03.md"] .t', el => el.textContent.trim());
    assert(title.includes('Terzo aggiunto a mano'), `Titolo letto dal file: "${title}"`);
    console.log('  ✔ Nuovo file rilevato automaticamente, titolo dal front-matter.');

    console.log('[Test 3] Rinomino 02.md fuori da PubCraft (stesso id front-matter)...');
    fs.renameSync(path.join(vault, '02.md'), path.join(vault, '02-capitolo-secondo.md'));
    await wait(SETTLE);

    const afterRename = await listFiles(page);
    assert.deepStrictEqual(afterRename, ['01.md', '02-capitolo-secondo.md', '03.md'],
      'La rinomina deve mantenere la posizione e non creare un doppione');
    assert.deepStrictEqual(yamlFiles(), ['01.md', '02-capitolo-secondo.md', '03.md'],
      'book.yaml deve puntare al nuovo nome');
    const dupCount = (await listFiles(page)).length;
    assert.strictEqual(dupCount, 3, `Nessun capitolo duplicato: ${afterRename.join(',')}`);
    console.log('  ✔ Rinomina riconosciuta: posizione mantenuta, nessun file doppio.');

    console.log('[Test 4] Cancello 01.md fuori da PubCraft...');
    fs.unlinkSync(path.join(vault, '01.md'));
    await wait(SETTLE);

    const afterDelete = await listFiles(page);
    assert.deepStrictEqual(afterDelete, ['01.md', '02-capitolo-secondo.md', '03.md'],
      'La voce del file cancellato non deve sparire da sola');
    const badge = await page.$eval('#chapterList li[data-file="01.md"] .ch-missing', el => el.textContent.trim());
    assert(badge.includes('file mancante'), `Badge atteso "file mancante", trovato "${badge}"`);
    assert.deepStrictEqual(yamlFiles(), ['01.md', '02-capitolo-secondo.md', '03.md'],
      'book.yaml non deve perdere la voce mancante');
    console.log('  ✔ File mancante segnalato, voce conservata.');

    console.log('[Test 5] Rimozione esplicita della voce orfana (✕)...');
    await page.click('#chapterList li[data-file="01.md"] .x');
    await wait(900);

    assert.deepStrictEqual(await listFiles(page), ['02-capitolo-secondo.md', '03.md'],
      'La voce orfana deve sparire dopo la rimozione esplicita');
    assert.deepStrictEqual(yamlFiles(), ['02-capitolo-secondo.md', '03.md'], 'book.yaml deve essere aggiornato');
    console.log('  ✔ Voce orfana rimossa solo su richiesta.');

    console.log('[Test 6] Aggiungo un file senza front-matter (titolo dal primo heading)...');
    fs.writeFileSync(path.join(vault, '04-ultimo.md'), '# Ultimo capitolo\n\nSolo corpo, niente front-matter.\n');
    await wait(SETTLE);

    assert.deepStrictEqual(await listFiles(page), ['02-capitolo-secondo.md', '03.md', '04-ultimo.md'],
      'Anche i file senza front-matter devono essere accodati');
    const plainTitle = await page.$eval('#chapterList li[data-file="04-ultimo.md"] .t', el => el.textContent.trim());
    assert(plainTitle.includes('Ultimo capitolo'), `Titolo dal primo heading: "${plainTitle}"`);
    console.log('  ✔ File senza front-matter gestito.');

    console.log('[Test 7] Modifico dall\'esterno il capitolo aperto nell\'editor...');
    await page.click('#chapterList li[data-file="03.md"]');
    await wait(700);
    const beforeExternal = await page.$eval('#ta', el => el.value);
    assert(beforeExternal.includes('Testo del capitolo Terzo aggiunto a mano'), 'Il capitolo deve essere aperto nell\'editor');

    fs.writeFileSync(path.join(vault, '03.md'), chapter('id-03', 'Terzo aggiunto a mano') + '\nRiga aggiunta con un editor esterno.\n');
    await wait(SETTLE);

    const afterExternal = await page.$eval('#ta', el => el.value);
    assert(afterExternal.includes('Riga aggiunta con un editor esterno.'),
      'L\'editor deve ricaricare il capitolo modificato dall\'esterno quando non ci sono modifiche locali');
    console.log('  ✔ Capitolo aperto ricaricato dal disco senza perdere il lavoro locale.');

    console.log('\n=================================================');
    console.log(' TUTTI I TEST DI SINCRONIZZAZIONE SUPERATI! ');
    console.log('=================================================');

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

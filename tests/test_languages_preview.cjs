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
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-locale-preview-'));
  const vaultA = path.join(tmp, 'vault-a');
  const vaultB = path.join(tmp, 'vault-b');
  for (const [dir, title] of [[vaultA, 'Book A'], [vaultB, 'Book B']]) {
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, 'book.yaml'), `title: ${title}\nchapters:\n- file: 01.md\n  title: First chapter\n- file: 02.md\n  title: Second chapter\n`);
    fs.writeFileSync(path.join(dir, '01.md'), `# First chapter\nOriginal ${title} text.\n`);
    fs.writeFileSync(path.join(dir, '02.md'), `# Second chapter\nSecond ${title} text.\n`);
  }

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
    assert(ready, 'GUI server did not start');

    browser = await puppeteer.connect({ browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222' });
    context = await browser.createBrowserContext();
    page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    await page.goto(url);
    await page.waitForFunction(() => currentFile === '01.md');

    const localeAudit = await page.evaluate(() => {
      const locales = Object.keys(I18N);
      const markupKeys = [...document.querySelectorAll('[data-i18n], [data-i18n-title], [data-i18n-placeholder]')]
        .flatMap(el => ['data-i18n', 'data-i18n-title', 'data-i18n-placeholder']
          .map(attr => el.getAttribute(attr)).filter(Boolean));
      const missingMarkupKeys = [...new Set(markupKeys.filter(key => !(key in I18N.en)))];
      const report = locales.map(lang => {
        setLanguage(lang);
        return {
          lang,
          htmlLang: document.documentElement.lang,
          dir: document.documentElement.dir,
          print: $('btnPrint').textContent.trim(),
          wordsShort: t('words_short'),
          emptyPreview: t('preview_empty'),
          missingEnglishKeys: Object.keys(I18N.en).filter(key => !(key in I18N[lang])),
        };
      });
      setLanguage('it');
      return { report, missingMarkupKeys };
    });
    assert.deepEqual(localeAudit.missingMarkupKeys, [], 'HTML has untranslated keys');
    const staticTranslationKeys = await page.evaluate(async () => {
      const source = await (await fetch('/static/app.js')).text();
      const calls = [...source.matchAll(/\bt\("([A-Za-z0-9_]+)"\)/g)].map(match => match[1]);
      return [...new Set(calls.filter(key => !(key in I18N.en)))];
    });
    assert.deepEqual(staticTranslationKeys, [], 'application references missing translation keys');
    for (const locale of localeAudit.report) {
      assert.deepEqual(locale.missingEnglishKeys, [], `${locale.lang} is missing translation keys`);
      assert.notEqual(locale.wordsShort, 'words_short', `${locale.lang}: words_short fallback leaked`);
      assert.notEqual(locale.emptyPreview, 'preview_empty', `${locale.lang}: preview_empty fallback leaked`);
      assert.equal(locale.dir, locale.lang === 'ar' ? 'rtl' : 'ltr', `${locale.lang}: wrong text direction`);
    }

    await page.evaluate(() => {
      $('newChapterType').value = 'dedication';
      $('newChapterType').dispatchEvent(new Event('change', { bubbles: true }));
    });
    assert.equal(await page.$eval('#newChapterTitle', el => el.value), 'Dedica');
    assert.deepEqual(pageErrors, [], 'section translation handler threw a JS error');

    // Dirty edits are saved in the original vault before the server switches vaults.
    await page.evaluate(async dest => {
      window.pickPath = async () => dest;
      ta.value = '# First chapter\nEDIT_SAVED_BEFORE_SWITCH';
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      await openVaultFolder();
    }, vaultB);
    await page.waitForFunction(() => state.vault.endsWith('vault-b') && currentFile === '01.md');
    assert.match(fs.readFileSync(path.join(vaultA, '01.md'), 'utf8'), /EDIT_SAVED_BEFORE_SWITCH/);
    assert.equal(await page.evaluate(() => dirty), false, 'dirty state leaked across vault switch');

    // Choosing an output directory must update the label and the build request.
    const externalDir = path.join(tmp, 'external export #1');
    const printBuild = await page.evaluate(async dir => {
      await initPrintModal();
      window.pickPath = async () => dir;
      await pickPrintDir();
      let captured;
      window.runBuild = (kind, params) => { captured = { kind, params }; };
      await printModalGo();
      return { captured, label: $('lblPrintOutDir').textContent };
    }, externalDir);
    assert.equal(printBuild.captured.kind, 'print');
    assert.equal(printBuild.captured.params.outdir, externalDir);
    assert(printBuild.label.includes(externalDir), 'selected output path is not displayed');

    // Hold an old chapter response, render the newly selected chapter, then release the old response.
    const previewRace = await page.evaluate(async () => {
      const originalApi = window.api;
      let release, signal;
      const held = new Promise(resolve => { signal = resolve; });
      window.api = (endpoint, options) => endpoint.includes('/api/chapter?file=02.md')
        ? new Promise(resolve => { release = () => resolve({ content: 'STALE_SECOND_CHAPTER' }); signal(); })
        : originalApi(endpoint, options);
      $('printPreviewChapter').value = '02.md';
      const stale = updateStyleSample();
      await held;
      $('printPreviewChapter').value = '01.md';
      await updateStyleSample();
      release();
      await stale;
      window.api = originalApi;
      const doc = new DOMParser().parseFromString($('styleSample').srcdoc, 'text/html');
      return { first: doc.body.textContent.includes('Original Book B text'), stale: doc.body.textContent.includes('STALE_SECOND_CHAPTER') };
    });
    assert.equal(previewRace.first, true, 'preview did not retain the latest selected chapter');
    assert.equal(previewRace.stale, false, 'stale chapter response replaced the current preview');

    const watch = await context.newPage();
    await watch.goto(url);
    await watch.evaluate(() => localStorage.setItem('pubcraft_lang', 'ar'));
    await watch.goto(`${url}/watch`);
    await watch.waitForSelector('#btnPrint');
    const watchLocaleAudit = await watch.evaluate(() => {
      const markupKeys = [...document.querySelectorAll('[data-i18n]')].map(el => el.getAttribute('data-i18n'));
      return Object.entries(WATCH_I18N).map(([lang, dict]) => ({
        lang,
        missingMarkup: markupKeys.filter(key => !(key in dict)),
        missingBase: Object.keys(WATCH_I18N.en).filter(key => !(key in dict)),
      }));
    });
    for (const locale of watchLocaleAudit) {
      assert.deepEqual(locale.missingMarkup, [], `${locale.lang} Watch markup is untranslated`);
      assert.deepEqual(locale.missingBase, [], `${locale.lang} Watch strings are missing`);
    }
    assert.deepEqual(await watch.evaluate(() => ({
      lang: document.documentElement.lang,
      dir: document.documentElement.dir,
      print: $('btnPrint').textContent.trim(),
      continuous: $('viewMode').options[1].textContent,
    })), { lang: 'ar', dir: 'rtl', print: 'طباعة / فتح PDF', continuous: '📜 نص متواصل' });
    await page.evaluate(() => setLanguage('fr'));
    await watch.waitForFunction(() => document.documentElement.lang === 'fr');
    assert.equal(await watch.$eval('#btnPrint', el => el.textContent.trim()), 'Imprimer / ouvrir le PDF');

    const newProjectResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/newproject' && response.request().method() === 'POST',
    { timeout: 10000 });
    await page.evaluate(parent => {
      showNewProjectModal();
      $('npParentDir').value = parent;
      $('npTitle').value = 'Projet Démo';
      $('npTitle').dispatchEvent(new Event('input', { bubbles: true }));
      $('btnNpCreate').click();
    }, tmp);
    const createResponse = await newProjectResponse;
    assert.equal(createResponse.status(), 200, `new-project API returned ${createResponse.status()}`);
    const created = await createResponse.json();
    assert(created.vault.endsWith('projet-demo'), `unexpected new project path: ${created.vault}`);
    let opened;
    for (let attempt = 0; attempt < 100; attempt++) {
      opened = await page.evaluate(() => ({
        title: document.querySelector('#curName').textContent,
        visible: !document.querySelector('#doc').hidden,
      }));
      if (opened.visible && opened.title === 'Capitolo 1') break;
      await wait(100);
    }
    assert.deepEqual(opened, { title: 'Capitolo 1', visible: true }, 'new project was created but not opened in the editor');
    assert(fs.existsSync(path.join(tmp, 'projet-demo', 'book.yaml')), 'new-project API did not create the book folder');

    assert.deepEqual(pageErrors, [], 'uncaught browser errors occurred');
    console.log('PASS: ten locales, RTL, new-project flow, safe vault switching, print settings/output, preview race, and live Watch locale sync.');
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
})().catch(error => { console.error(error); process.exit(1); });

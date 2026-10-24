const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const { spawn, execFileSync } = require('node:child_process');
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

(async () => {
  const root = path.resolve(__dirname, '..');
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-project-safety-'));
  const docs = path.join(tmp, 'Documents');
  fs.mkdirSync(docs);
  const a = path.join(tmp, 'a'), b = path.join(tmp, 'b');
  for (const dir of [a, b]) {
    fs.mkdirSync(dir);
    fs.writeFileSync(path.join(dir, 'book.yaml'), '---\ntitle: Synthetic book\nchapters:\n- file: 01.md\n  title: First\n');
    fs.writeFileSync(path.join(dir, '01.md'), 'IDENTICAL_ORIGINAL');
  }
  const bad = path.join(tmp, 'bad');
  fs.mkdirSync(bad);
  fs.writeFileSync(path.join(bad, 'book.yaml'), 'title: Invalid\nchapters: [\n');
  const port = await new Promise(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });
  const url = `http://127.0.0.1:${port}`;
  const server = spawn(process.env.PUBCRAFT_GUI_BIN || path.join(root, 'build/pubcraftgui'), [], {
    env: { ...process.env, HOME: tmp, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });
  let browser, context;
  try {
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch(`${url}/api/state`)).ok) { ready = true; break; } } catch {}
      await wait(50);
    }
    assert(ready, 'server did not start');
    browser = await puppeteer.connect({ browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222' });
    context = await browser.createBrowserContext();
    const page = await context.newPage();
    const errors = [];
    page.on('console', msg => console.log('PAGE LOG:', msg.text()));
    page.on('pageerror', err => {
      console.log('PAGE ERROR:', err.message);
      errors.push(err.message);
    });
    page.on('dialog', dialog => dialog.accept());
    await page.goto(url);
    await page.waitForFunction(() => defaultParentDir !== '');

    // Real clicks and typing, without calling show/create functions or injecting a path.
    // Check all entry points, including the empty-state button on first launch.
    for (const [index, entry] of ['#btnWelcomeNew', '#btnNewProject', '#btnOpenNewProject'].entries()) {
      const closeBtn = await page.$('#btnCloseBook:not(.hidden)');
      if (closeBtn) {
        await page.evaluate(async () => {
          await closeCurrentBook();
        });
        await page.waitForSelector('#empty:not([style*="display: none"])');
      }
      if (entry === '#btnOpenNewProject') {
        await page.$eval('#btnOpen', el => el.click());
      }
      await page.$eval(entry, el => el.click());
      await page.waitForSelector('#newProjectModal:not(.hidden)');
      assert.equal(await page.$eval('#npParentDir', el => el.value), docs);
      const title = `Book ${index}: l'autore "Prova"`;
      await page.type('#npTitle', title);
      const response = page.waitForResponse(r => r.url().endsWith('/api/newproject'));
      await page.$eval('#btnNpCreate', el => el.click());
      const created = await response;
      assert.equal(created.status(), 200, await created.text());
      const result = await created.json();
      await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01-capitolo-1.md' && !isProjectSwitching, { polling: 100, timeout: 15000 });
      assert.equal(await page.$eval('#bookTitle', el => el.textContent), title);
      assert.equal(await page.$eval('#ta', el => el.readOnly), false);
      assert(fs.existsSync(path.join(result.vault, 'book.yaml')));
      // Reload must reopen the newly written metadata, including punctuation.
      await page.reload({ waitUntil: 'networkidle0' }).catch(() => {});
      await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01-capitolo-1.md', { polling: 100, timeout: 15000 });
      assert.equal(await page.$eval('#bookTitle', el => el.textContent), title);
    }

    const exported = await page.evaluate(async outdir => {
      await runBuild('kindle', { outdir });
      const title = state.book.title;
      hideModal();
      return { title, status: $('saveState').textContent };
    }, docs);
    const epub = fs.readdirSync(docs).find(name => name.endsWith('.epub'));
    assert(epub, `EPUB was not produced: ${exported.status}`);
    const archive = path.join(docs, epub);
    const files = execFileSync('unzip', ['-Z1', archive], { encoding: 'utf8' });
    const opfPath = files.split('\n').find(file => file.endsWith('.opf'));
    const opf = execFileSync('unzip', ['-p', archive, opfPath], { encoding: 'utf8' });
    assert(opf.includes(`Book 2: l'autore &quot;Prova&quot;`) || opf.includes(`Book 2: l&apos;autore &quot;Prova&quot;`) || opf.includes(exported.title), 'CLI changed the new book title in EPUB metadata');

    await page.evaluate(async dir => {
      window.pickPath = async () => dir;
      await openVaultFolder();
    }, a);
    assert.equal(await page.evaluate(() => state.vault), a, 'valid YAML document marker rejected');

    // Hold the response AFTER the backend changes vault, where unguarded delete
    // used to operate on B while the user was still seeing A.
    await page.evaluate(dest => {
      const originalApi = window.api;
      window.releaseSwitch = null;
      const gate = new Promise(resolve => { window.releaseSwitch = resolve; });
      window.switchReached = false;
      window.api = async (endpoint, opts) => {
        const response = await originalApi(endpoint, opts);
        if (endpoint === '/api/openat') {
          window.switchReached = true;
          await gate;
        }
        return response;
      };
      window.restoreApi = () => { window.api = originalApi; };
      window.pickPath = async () => dest;
      window.pendingSwitch = openVaultFolder();
    }, b);
    await page.waitForFunction(() => window.switchReached, { polling: 100 });
    const safety = await page.evaluate(async oldVault => {
      const inert = $('app').inert && $('topbar').inert;
      await deleteChapter(state.book.chapters[0]);
      const response = await fetch('/api/deletechapter', {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'X-PubCraft-Vault': oldVault },
        body: JSON.stringify({ file: '01.md' }),
      });
      return { inert, status: response.status };
    }, a);
    assert.deepEqual(safety, { inert: true, status: 409 });
    await page.evaluate(async () => { window.releaseSwitch(); await window.pendingSwitch; window.restoreApi(); });
    assert.equal(fs.readFileSync(path.join(b, '01.md'), 'utf8'), 'IDENTICAL_ORIGINAL');
    assert.equal(fs.readFileSync(path.join(a, '01.md'), 'utf8'), 'IDENTICAL_ORIGINAL');
    assert.equal(await page.evaluate(() => state.book.chapters.length), 1);

    const invalid = await page.evaluate(async dir => {
      window.pickPath = async () => dir;
      await openVaultFolder();
      return { vault: state.vault, readOnly: ta.readOnly };
    }, bad);
    assert.deepEqual(invalid, { vault: b, readOnly: false });
    assert.deepEqual(errors, []);
    console.log('PASS: real new-book buttons, quoted titles/reload, YAML validation, locked structural actions and stale-vault rejection.');
  } finally {
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
})().catch(err => { console.error(err); process.exitCode = 1; });

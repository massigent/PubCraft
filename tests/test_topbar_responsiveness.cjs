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
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'pubcraft-topbar-test-'));
  const vault = path.join(tmp, 'vault');

  fs.mkdirSync(vault, { recursive: true });
  fs.writeFileSync(path.join(vault, 'book.yaml'), 'title: Il Mio Libro di Prova Lungo per Topbar\nchapters:\n- file: 01.md\n  title: Capitolo 1\n');
  fs.writeFileSync(path.join(vault, '01.md'), '# Capitolo 1\nTesto');

  const port = await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const value = server.address().port;
      server.close(() => resolve(value));
    });
  });

  const url = `http://127.0.0.1:${port}`;
  const server = spawn(path.join(root, 'build/pubcraftgui'), [vault], {
    env: { ...process.env, PUBCRAFTGUI_LISTEN: `127.0.0.1:${port}`, PUBCRAFTGUI_NO_BROWSER: '1' },
    stdio: 'ignore',
  });

  let browser;
  let context;
  let page;

  try {
    let ready = false;
    for (let i = 0; i < 80; i++) {
      try {
        const res = await fetch(`${url}/api/state`);
        if (res.ok) { ready = true; break; }
      } catch (_) {}
      await wait(100);
    }
    assert.equal(ready, true, 'Server must start');

    browser = await puppeteer.connect({
      browserURL: process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222',
      defaultViewport: null,
    });
    context = await browser.createBrowserContext();
    page = await context.newPage();

    await page.goto(url);
    await page.waitForFunction(() => typeof currentFile !== 'undefined' && currentFile === '01.md', { polling: 100 });

    const viewports = [768, 800, 900, 1024, 1100, 1200, 1280, 1366, 1440, 1600];

    const failures = [];
    for (const w of viewports) {
      await page.setViewport({ width: w, height: 800 });
      await wait(150);

      const metrics = await page.evaluate(() => {
        const docScrollW = document.documentElement.scrollWidth;
        const winInnerW = window.innerWidth;
        const topbar = document.getElementById('topbar');
        const tbRect = topbar.getBoundingClientRect();

        const btnTheme = document.getElementById('btnToggleTheme').getBoundingClientRect();
        const btnReport = document.getElementById('btnReportIssue').getBoundingClientRect();
        const styleList = document.getElementById('styleList').getBoundingClientRect();
        const btnWatch = document.getElementById('btnWatch').getBoundingClientRect();

        return {
          docScrollW,
          winInnerW,
          topbarHeight: tbRect.height,
          topbarRight: tbRect.right,
          btnThemeRight: btnTheme.right,
          btnThemeVisible: btnTheme.width > 0 && btnTheme.height > 0,
          btnReportRight: btnReport.right,
          btnReportVisible: btnReport.width > 0 && btnReport.height > 0,
          styleListVisible: styleList.width > 0 && styleList.height > 0,
          btnWatchVisible: btnWatch.width > 0 && btnWatch.height > 0,
        };
      });

      console.log(`Viewport ${w}px: docScrollW=${metrics.docScrollW}, tbRight=${metrics.topbarRight}, btnReportRight=${metrics.btnReportRight}, tbHeight=${metrics.topbarHeight}`);

      if (w === 1440) {
        const itemInfo = await page.evaluate(() => {
          const items = Array.from(document.querySelectorAll('#topbar, #topbar > *, .tools > *, .tb-meta-group > *, .tb-group > *, .tb-export-cluster > *'));
          return items.map(el => {
            const r = el.getBoundingClientRect();
            return { tag: el.tagName, id: el.id, cls: el.className, w: Math.round(r.width), l: Math.round(r.left), r: Math.round(r.right) };
          });
        });
        console.log('DOM rects at 1440px:', itemInfo);
      }
      if (metrics.docScrollW > w + 1 || metrics.btnReportRight > w + 2 || metrics.topbarHeight > 50) {
        failures.push({ w, metrics });
      }
    }

    if (failures.length > 0) {
      console.error('Failures at viewports:', failures);
      assert.fail(`Responsive failures at ${failures.length} viewports: ${failures.map(f => f.w).join(', ')}`);
    }

    console.log('✔ All viewports passed responsive checks seamlessly!');
  } finally {
    if (page) await page.close().catch(() => {});
    if (context) await context.close().catch(() => {});
    if (browser) await browser.disconnect().catch(() => {});
    if (server) {
      server.kill();
      await wait(500);
    }
    fs.rmSync(tmp, { recursive: true, force: true });
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});

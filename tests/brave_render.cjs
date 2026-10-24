// Test-only adapter: use an already running Chromium/Brave CDP session without
// touching its existing tabs. Production headless startup is tested separately.
const puppeteer = require(process.env.PUPPETEER_MODULE || 'puppeteer-core');
const fs = require('fs');
const { pathToFileURL } = require('url');
(async () => {
 const args=process.argv.slice(2);
 const output=args.find(a=>a.startsWith('--print-to-pdf='))?.slice('--print-to-pdf='.length);
 if(!output) throw new Error('Missing --print-to-pdf');
 let url=args.at(-1); if(!url.startsWith('file:'))url=pathToFileURL(url).href;
 const browser=await puppeteer.connect({browserURL:process.env.BROWSER_CDP_URL||'http://127.0.0.1:9222',defaultViewport:null});
 const page=await browser.newPage();
 try {
  await page.goto(url,{waitUntil:'networkidle0',timeout:30000});
  await page.evaluate(()=>document.fonts.ready);
  const client=await page.createCDPSession();
  const pdf=await client.send('Page.printToPDF',{preferCSSPageSize:true,printBackground:true,displayHeaderFooter:false});
  fs.writeFileSync(output,Buffer.from(pdf.data,'base64'));
 } finally { await page.close(); await browser.disconnect(); }
})().catch(e=>{console.error(e);process.exit(1)});

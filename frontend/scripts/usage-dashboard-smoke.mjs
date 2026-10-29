import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

// The production usage dashboard with deterministic traffic and partial pricing.
// Set ACCOUNT_SCREENSHOT_DIR to keep screenshots of each layout.
const work = await mkdtemp(join(tmpdir(), 'sessions-usage-dashboard-'));
let server, browser;
try {
  await build({ entryPoints: ['scripts/usage-dashboard-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg', '/openai-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"><div id="root"></div><script type="module" src="/app.js"></script>');
  server = createServer(async (req, res) => {
    const path = req.url.split('?')[0];
    const name = path === '/' ? 'index.html' : path.slice(1);
    const icon = ['claude-icon.svg', 'openai-icon.svg'].includes(name);
    if (!icon && !['index.html', 'app.js', 'app.css'].includes(name)) { res.writeHead(404); res.end(); return; }
    res.writeHead(200, { 'Content-Type': icon ? 'image/svg+xml' : name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
    res.end(await readFile(icon ? join('public', name) : join(work, name)));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const [theme, width] of [['dark', 1280], ['dark', 375], ['light', 1280], ['light', 375]]) {
    await page.setViewport({ width, height: 1100, isMobile: width === 375, hasTouch: width === 375 });
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}`, { waitUntil: 'networkidle0' });
    await page.waitForFunction(() => document.querySelector('.usage-kpi')?.textContent.includes('Tokens processed'));
    assert.match(await page.$eval('main', el => el.textContent), /Partial cost estimate/);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    await page.click('.usage-row-actions button');
    assert.equal(await page.$$eval('.usage-row-detail > div', nodes => nodes.length), 7);
    const hitAreas = await page.$$eval('.ui-button, .ui-control', nodes => nodes.map(n => n.getBoundingClientRect().height));
    if (width === 375) assert.ok(hitAreas.every(height => height >= 44), `touch targets: ${hitAreas}`);
    const contentHeight = await page.$eval('.usage-shell', el => Math.ceil(el.getBoundingClientRect().height) + 100);
    await page.setViewport({ width, height: Math.max(1100, contentHeight), isMobile: width === 375, hasTouch: width === 375 });
    await page.evaluate(() => document.querySelectorAll('html, body, #root, main, .usage-view').forEach(el => { el.scrollTop = 0; }));
    if (process.env.ACCOUNT_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, `usage-dashboard-${theme}-${width}.png`), fullPage: true });
    await page.select('select', '');
    await page.type('input', 'My usage');
    await page.$$eval('button', nodes => nodes.find(n => n.textContent === 'Save current').click());
    assert.match(await page.$eval('select', el => el.textContent), /My usage/);
  }
  assert.deepEqual(errors, []);
  console.log('usage dashboard: real shared controls, fake traffic and partial costs, responsive themes, detail and saved views passed');
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-ui-'));
let server;
let browser;
try {
  await build({ entryPoints: ['scripts/history-search-fixture.tsx'], bundle: true, outdir: work,
    plugins: process.env.SEARCH_BASELINE ? [{ name: 'baseline', setup(builder) { builder.onLoad({ filter: /components\/SearchView\.tsx$/ }, async (args) => ({ contents: await readFile(process.env.SEARCH_BASELINE, 'utf8'), loader: 'tsx', resolveDir: args.path.slice(0, args.path.lastIndexOf('/')) })); } }] : [],
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' },
    loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root" style="height:100dvh"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (req, res) => {
    const pathname = new URL(req.url, 'http://fixture.invalid').pathname;
    const name = pathname === '/' ? 'index.html' : pathname.slice(1);
    if (!['index.html', 'app.js', 'app.css', 'claude-icon.svg', 'openai-icon.svg', 'somewhere-logo.svg'].includes(name)) {
      res.writeHead(404); res.end(); return;
    }
    try {
      const file = await readFile(join(name.endsWith('.svg') ? 'public' : work, name));
      res.writeHead(200, { 'Content-Type': name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : name.endsWith('.svg') ? 'image/svg+xml' : 'text/html' });
      res.end(file);
    } catch { res.writeHead(404); res.end(); }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => { errors.push(error.message); console.error(error.message); });
  const browseStarted = Date.now();
  await page.goto(`http://127.0.0.1:${server.address().port}/?screen=search&state=latency`);
  await page.waitForSelector('[data-history-key]');
  console.log(JSON.stringify({ scenario: '288 recent conversations with unavailable remote', firstBrowseMS: Date.now() - browseStarted }));
  await page.evaluate(() => { window.searchHarness.failEnrichment = true; });
  await page.type('[aria-label="Search conversations"]', 'migration');
  const started = Date.now();
  await page.click('.search-ai-submit');
  await page.waitForSelector('.search-result-main');
  const firstResultMS = Date.now() - started;
  console.log(JSON.stringify({ scenario: 'local keyword, delayed enrichment and unavailable remote', firstResultMS }));
  if (!process.env.SEARCH_BASELINE) assert.ok(firstResultMS < 1500, 'indexed matches must not wait for enrichment');
  if (!process.env.SEARCH_BASELINE) {
    await page.waitForFunction(() => document.body.textContent.includes('Additional title sources did not finish'), { timeout: 10000 });
    await page.click('[aria-label="Clear search"]');
    await page.type('[aria-label="Search conversations"]', 'old');
    await page.click('.search-ai-submit');
    await page.waitForSelector('.search-progress');
    await page.click('[aria-label="Clear search"]');
    await page.type('[aria-label="Search conversations"]', 'migration');
    await page.click('.search-ai-submit');
    await page.waitForSelector('.search-result-main');
    await page.evaluate(async () => { window.searchHarness.releaseOld(); await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); });
    await page.waitForFunction(() => !document.body.textContent.includes('Obsolete result'));
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=search&state=cold`);
    await page.type('[aria-label="Search conversations"]', 'migration');
    const coldStarted = Date.now();
    await page.click('.search-ai-submit');
    await page.waitForSelector('.search-result-main');
    const titleMS = Date.now() - coldStarted;
    console.log(JSON.stringify({ scenario: 'title navigation during 2400ms cold index read', firstTitleMS: titleMS }));
    assert.ok(titleMS < 1500, 'title metadata must not wait for content indexing');
    assert.ok(await page.$('.search-progress'), 'content search remains visibly pending');
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=search&state=remote-first`);
    await page.type('[aria-label="Search conversations"]', 'migration');
    const remoteStarted = Date.now();
    await page.click('.search-ai-submit');
    await page.waitForSelector('.search-result-main');
    const remoteMS = Date.now() - remoteStarted;
    console.log(JSON.stringify({ scenario: 'remote answers while local transport never settles', firstRemoteMS: remoteMS }));
    assert.ok(remoteMS < 1500, 'a ready remote must not wait for the local transport');
    assert.match(await page.$eval('.search-shell', (node) => node.textContent), /Delayed fixture/);
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=search&state=smart`);
    await page.waitForSelector('.search-mode-row');
    await page.evaluate(() => Array.from(document.querySelectorAll('.search-mode-row button')).find((node) => node.textContent === 'Smart').click());
    await page.type('[aria-label="Search conversations"]', 'Where was the migration?');
    await page.click('.search-ai-submit');
    await page.waitForSelector('.search-result-main');
    assert.match(await page.$eval('.search-plan', (node) => node.textContent), /migration/);
  }
  await page.goto(`http://127.0.0.1:${server.address().port}/?screen=search&state=timeout`);
  await page.type('[aria-label="Search conversations"]', 'migration');
  await page.click('.search-ai-submit');
  await page.waitForSelector('.search-progress');
  const timed = Date.now();
  await page.waitForFunction(() => !document.querySelector('.search-progress'), { timeout: process.env.SEARCH_BASELINE ? 10000 : 33000 });
  console.log(JSON.stringify({ scenario: 'unavailable local and remote', settledMS: Date.now() - timed }));
  assert.match(await page.$eval('.search-shell', (node) => node.textContent), /did not answer in time/);
  assert.deepEqual(errors, []);
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

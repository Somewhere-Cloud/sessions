import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-storage-ui-'));
let server, browser;
try {
  await build({ entryPoints: ['scripts/storage-fixture.tsx'], bundle: true, outdir: work, entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg', '/openai-icon.svg'], define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"><div id="root"></div><script type="module" src="/app.js"></script>');
  server = createServer(async (req, res) => {
    if (req.url === '/favicon.ico') { res.writeHead(204); res.end(); return; }
    const name = req.url === '/' || req.url.startsWith('/?') ? 'index.html' : req.url.slice(1);
    if (!['index.html', 'app.js', 'app.css'].includes(name)) { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html');
    res.end(await readFile(join(work, name)));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('requestfailed', (request) => errors.push(`${request.url()}: ${request.failure()?.errorText}`));
  for (const [theme, width] of [['dark', 1280], ['light', 1280], ['dark', 375], ['light', 375]]) {
    await page.setViewport({ width, height: 900 });
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}`, { waitUntil: 'networkidle0' });
    assert.equal(await page.evaluate(() => window.storageFixture.requests.length), 0, 'no automatic scan');
    await page.click('.storage-controls .btn-primary');
    await page.waitForSelector('[aria-label="Storage summary"]');
    const text = await page.$eval('.storage-view', (node) => node.innerText);
    for (const value of ['Remote fixture workstation', 'At least 2.5 GiB', '4 repositories', 'Some measurements incomplete', 'parser-fix', 'Partly measured', 'Owner not recorded', 'Review available', 'Not measured', 'Could not be measured']) assert.ok(text.includes(value), `missing ${value}`);
    for (const value of ['Machine remote-fixture', '/fixture/projects', 'Git changes:', 'logical', 'allocated', 'hardlinks', 'Restore suggestion:', 'permission denied']) assert.ok(!text.includes(value), `technical detail visible before expansion: ${value}`);
    assert.deepEqual(await page.$$eval('.storage-repository-name', (nodes) => nodes.map((node) => node.textContent)), ['sessions', 'parser-fix', 'docs-preview', 'unavailable'], 'measured size order; unavailable last');
    assert.equal(await page.$eval('.storage-summary-size', (node) => node.textContent), 'At least 2.5 GiB', 'use host total, not shared row sum');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `no overflow in ${theme} at ${width}`);
    assert.deepEqual(await page.evaluate(() => window.storageFixture.requests), ['GET http://relay.fixture.test:8787/api/fleet/remote-fixture/api/storage/preview']);
    if (process.env.STORAGE_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.STORAGE_SCREENSHOT_DIR, `storage-compact-${theme}-${width}.png`), fullPage: true });
    await page.select('.storage-list-heading select', 'name');
    assert.deepEqual(await page.$$eval('.storage-repository-name', (nodes) => nodes.map((node) => node.textContent)), ['docs-preview', 'parser-fix', 'sessions', 'unavailable'], 'name sorting is reversible');
    const parser = await page.$('.storage-repository:nth-child(2) > summary');
    await parser.focus();
    await page.keyboard.press('Enter');
    await page.click('.storage-scan-details > summary');
    const expanded = await page.$eval('.storage-view', (node) => node.innerText);
    for (const value of ['Machine remote-fixture', '/fixture/projects', 'Ownership unknown', 'Partial reading', 'Unknown allocated', 'shared hardlinks', 'runner references', 'permission denied', 'Restoration has not been tested']) assert.ok(expanded.includes(value), `missing expanded detail: ${value}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'details fit the viewport too');
    assert.deepEqual(await page.$$eval('.storage-view button', (nodes) => nodes.map((node) => node.textContent)), ['Scan storage'], 'no apply/cleanup control');
    if (process.env.STORAGE_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.STORAGE_SCREENSHOT_DIR, `storage-details-${theme}-${width}.png`), fullPage: true });

  }
  await page.evaluate(() => { window.storageFixture.setMode('wait'); });
  await page.click('.storage-controls .btn-primary');
  await page.waitForFunction(() => document.body.textContent.includes('Cancel scan'));
  await page.evaluate(() => window.storageFixture.select('local'));
  await page.waitForFunction(() => !document.querySelector('[aria-label="Storage summary"]'));
  await page.evaluate(() => window.storageFixture.setMode('normal'));
  await page.click('.storage-controls .btn-primary');
  await page.waitForFunction(() => document.querySelector('[aria-label="Storage summary"]')?.textContent.includes('Machine local-fixture'));
  assert.ok((await page.evaluate(() => window.storageFixture.requests)).at(-1).includes('http://local.fixture.test:8787/api/storage/preview'), 'switch uses local route');
  await page.evaluate(() => window.storageFixture.setMode('wait'));
  await page.click('.storage-controls .btn-primary');
  await page.waitForFunction(() => document.body.textContent.includes('Cancel scan'));
  await page.$eval('.storage-controls button:last-child', (button) => button.click());
  await page.waitForFunction(() => document.body.textContent.includes('Scan canceled'));
  assert.equal(await page.$('[aria-label="Storage summary"]'), null, 'cancel cannot show old reading');
  for (const [mode, expected] of [['empty', 'No known Git repositories'], ['error', 'wait and retry'], ['older', 'not available']]) {
    await page.evaluate((value) => window.storageFixture.setMode(value), mode);
    await page.click('.storage-controls .btn-primary');
    await page.waitForFunction((value) => document.body.textContent.includes(value), {}, expected);
  }
  assert.deepEqual(errors, []);
  console.log('storage: compact rows and expandable detail in dark/light at 1280/375px; measured-size/name sorting, host total without row summation, unknown/partial states, keyboard expansion, read-only controls, selected-machine routing/cancellation, empty/busy/older hosts; no page/console/request errors or overflow');
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

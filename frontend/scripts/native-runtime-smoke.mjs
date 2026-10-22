import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-native-status-'));
let server, browser;
try {
  await build({ entryPoints: ['scripts/native-runtime-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic',
    external: ['/claude-icon.svg'], logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><link rel="stylesheet" href="/app.css"><div id="root"></div><script type="module" src="/app.js"></script>');
  server = createServer(async (req, res) => {
    const name = req.url === '/' ? 'index.html' : req.url.slice(1);
    if (!['index.html', 'app.js', 'app.css'].includes(name)) { res.writeHead(404).end(); return; }
    res.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html');
    res.end(await readFile(join(work, name)));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true });
  const page = await browser.newPage();
  await page.evaluateOnNewDocument(() => {
    window.nativeCalls = [];
    window.nativeStatus = { state: 'error', detail: 'Update rejected; previous service restored', runtimeVersion: null };
    window.__TAURI_INTERNALS__ = { invoke: async command => {
      window.nativeCalls.push(command);
      if (command !== 'runtime_status') throw new Error('Unexpected mutation');
      if (window.nativeFailure) throw new Error('Native status unreachable');
      return window.nativeStatus;
    } };
  });
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.waitForFunction(() => document.body.textContent.includes('previous service restored'));
  await page.evaluate(() => { window.nativeStatus = { state: 'ready', detail: 'updated safely', runtimeVersion: 'candidate' }; });
  await page.waitForFunction(() => document.body.textContent.includes('candidate'), { timeout: 10000 });
  await page.evaluate(() => { window.nativeFailure = true; });
  await page.waitForFunction(() => document.body.textContent.includes('Installation status unavailable'), { timeout: 10000 });
  assert.deepEqual(await page.evaluate(() => [...new Set(window.nativeCalls)]), ['runtime_status']);
  const plain = await browser.newPage();
  await plain.goto(`http://127.0.0.1:${server.address().port}`);
  assert.equal(await plain.$('[aria-label="Local background service"]'), null);
  console.log('native runtime: rollback visible without daemon failure; updates, read errors, browser absence and no mutations verified');
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

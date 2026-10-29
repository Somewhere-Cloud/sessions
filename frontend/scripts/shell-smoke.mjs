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
  await build({ entryPoints: ['scripts/shell-fixture.tsx'], bundle: true, outdir: work,
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
  for (const theme of ['dark', 'light']) {
    for (const [width, height] of [[1440, 1000], [1100, 900], [375, 812]]) {
      await page.setViewport({ width, height, isMobile: width === 375, hasTouch: width === 375 });
      for (const screen of ['home', 'tabs', 'launcher', 'today']) {
        for (const empty of [false, true]) {
          await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}&empty=${empty ? 1 : 0}`, { waitUntil: 'networkidle0' });
          await page.waitForSelector('.operations-content');
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen}/${theme}/${width} fits`);
          if (width === 1100) {
            for (const selector of ['.product-new-session', '.product-command-button', '.product-nav-item', '.theme-toggle']) {
              const centered = await page.$eval(selector, (el) => {
                const icon = el.querySelector('svg') ?? el.querySelector('span');
                const box = el.getBoundingClientRect(), mark = icon.getBoundingClientRect();
                return Math.abs((box.left + box.width / 2) - (mark.left + mark.width / 2)) < 2;
              });
              assert.equal(centered, true, `${selector} centers in compact rail`);
            }
            assert.equal(await page.$('.product-nav [aria-label="Home"]'), null);
            assert.ok(await page.$('.product-sidebar-footer [aria-label="Usage"]'));
          }
          if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}${empty ? '-empty' : ''}.png`), fullPage: true });
          if (screen === 'tabs' && !empty) {
            await page.waitForFunction(() => document.body.textContent.includes('Sessions Studio'));
            assert.ok(await page.$('[aria-label="Filter sessions"]'));
            if (width === 1440) {
              await page.click('.product-command-button');
              await page.waitForFunction(() => document.activeElement?.getAttribute('role') === 'combobox');
              await page.keyboard.type('jump to inbox');
              await page.keyboard.press('Enter');
              await page.waitForFunction(() => document.activeElement?.getAttribute('role') === 'treeitem');
            }
            await page.focus('[aria-label="Filter sessions"]');
            await page.keyboard.type('no matching project');
            assert.equal(await page.$$eval('.session-nav-row', (rows) => rows.length), 0);
          }
          if (width === 1440 && screen === 'home' && !empty) {
            await page.click('.product-command-button');
            await page.waitForSelector('[role="combobox"]');
            await page.waitForFunction(() => document.activeElement?.getAttribute('role') === 'combobox');
            for (let index = 0; index < 15; index++) await page.keyboard.press('ArrowDown');
            assert.equal(await page.evaluate(() => {
              const selected = document.querySelector('[role="option"][aria-selected="true"]');
              const list = document.querySelector('[role="listbox"]');
              const row = selected.getBoundingClientRect(); const bounds = list.getBoundingClientRect();
              return row.top >= bounds.top && row.bottom <= bounds.bottom;
            }), true, 'keyboard-selected result scrolls into view');
            await page.keyboard.type('workspace');
            await page.keyboard.press('ArrowDown');
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `palette-${theme}.png`) });
            await page.keyboard.press('Escape');
            assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('product-command-button')), true, 'palette returns focus');
          }
          if (width === 375 && screen === 'home' && !empty) {
            await page.click('[aria-label="Open More"]');
            await page.waitForSelector('[data-modal-dialog]');
            await page.keyboard.press('Tab');
            assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true);
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `mobile-more-${theme}.png`) });
            await page.keyboard.press('Escape');
            assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), 'Open More');
          }
          const mutations = await page.evaluate(() => window.shellDaemon.requests.filter((r) => r.method === 'POST'));
          assert.deepEqual(mutations, [], 'shell navigation makes no operational mutations');
        }
      }
    }
  }
  for (const screen of ['home', 'tabs', 'launcher']) {
    await page.setViewport({ width: 375, height: 812, isMobile: true, hasTouch: true });
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=${screen}&theme=light&size=l`, { waitUntil: 'networkidle0' });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen} fits large text`);
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-light-375-large.png`) });
  }
  await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }]);
  assert.equal(await page.$eval('.ui-button', (el) => getComputedStyle(el).transitionDuration), '0s');
  for (const state of ['loading', 'error']) {
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=today&state=${state}`, { waitUntil: 'networkidle0' });
    await page.waitForSelector(state === 'loading' ? '.today-skeleton' : '.ui-status');
    if (state === 'error') assert.equal(await page.$('.today-skeleton'), null, 'failed read does not keep showing a loading skeleton');
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `daily-${state}.png`) });
  }
  assert.deepEqual(errors, [], 'no React/browser errors');
  console.log('shell compositions: populated/empty Home, projects, launcher, Daily in dark/light desktop/375px; modal focus return; filter; no mutations');
} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

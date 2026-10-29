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
    for (const [width, height] of [[1440, 1000], [375, 812]]) {
      await page.setViewport({ width, height, isMobile: width === 375, hasTouch: width === 375 });
      for (const screen of ['search', 'history', 'resume']) {
        for (const state of ['populated', 'empty', 'error', 'loading']) {
          if (screen === 'history' && state === 'empty') continue;
          const base = `http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}&state=${state}`;
          await page.goto(base, { waitUntil: 'networkidle0' });
          if (screen === 'resume') await page.click('main > .ui-button');
          if (screen === 'resume' && state === 'populated') {
            await page.waitForSelector('.resume-card'); await page.click('.resume-card');
            await page.waitForSelector('.resume-preview-message');
            await page.waitForFunction(() => !document.querySelector('.continuation-start').disabled);
          }
          if (screen === 'search' && state === 'populated') await page.waitForSelector('.conversation-row');
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen}/${theme}/${width}/${state} fits`);
          if (screen === 'history') {
            const archiveButtons = await page.$$eval('button', (nodes) => nodes.filter((el) => /Archive/.test(el.textContent)).length);
            assert.equal(archiveButtons, 1, 'ended banner owns the single archive action');
            assert.equal(await page.$('.session-active-header .session-runtime-badge'), null, 'mode lives in secondary details');
            assert.equal(await page.$eval('.session-actions-menu', (el) => el.open), false);
            await page.focus('.session-actions-menu > summary');
            await page.keyboard.press('Enter');
            assert.equal(await page.$eval('.session-actions-menu', (el) => el.open), true);
            await page.keyboard.press('Escape');
            assert.equal(await page.$eval('.session-actions-menu', (el) => el.open), false);
            assert.equal(await page.$eval('.session-actions-menu > summary', (el) => el === document.activeElement), true);
          }
          if (width === 375) {
            const shortTargets = await page.$$eval('.ui-button, .ui-control', (nodes) => nodes.filter((node) => {
              const rect = node.getBoundingClientRect();
              return rect.width > 0 && rect.height > 0 && getComputedStyle(node).visibility !== 'hidden' && rect.height < 43;
            }).map((node) => ({ label: node.textContent || node.getAttribute('aria-label'), height: node.getBoundingClientRect().height })));
            assert.deepEqual(shortTargets, [], `${screen} shared controls retain coarse-pointer targets`);
            if (screen === 'history') assert.equal(await page.$eval('.session-toolbar', (toolbar) => {
              const bounds = toolbar.getBoundingClientRect();
              const body = document.querySelector('.session-history-body').getBoundingClientRect();
              if (bounds.bottom > body.top + 1) return false;
              return Array.from(toolbar.querySelectorAll('.ui-button')).every((button) => {
                const rect = button.getBoundingClientRect(); return rect.top >= bounds.top - 1 && rect.bottom <= bounds.bottom + 1;
              });
            }), true, 'toolbar contains its touch targets without clipping');
          }
          if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}-${state}.png`), fullPage: true });
          if (screen === 'search' && state === 'populated') {
            await page.type('[aria-label="Search conversations"]', 'migration');
            await page.click('.search-ai-submit'); await page.waitForSelector('.search-result-main');
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `results-${theme}-${width}.png`), fullPage: true });
            await page.click('.search-result-main'); await page.waitForSelector('.search-transcript-message');
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `transcript-${theme}-${width}.png`), fullPage: true });
          }
          if (screen === 'search' && state === 'populated') {
            await page.click('.search-back');
            await page.waitForFunction(() => document.activeElement?.classList.contains('search-result-main'));
          }
          if (screen === 'resume' && state === 'populated') {
            await page.click('.resume-settings summary');
            await page.waitForSelector('[aria-label="Resume permissions"]');
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `review-${theme}-${width}.png`), fullPage: true });
            await page.$eval('.continuation-start', (button) => button.scrollIntoView({ block: 'nearest' }));
            if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `review-actions-${theme}-${width}.png`), fullPage: true });
            await page.keyboard.press('Tab');
            assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true);
          }
          assert.deepEqual(await page.evaluate(() => window.historySearchDaemon.requests.filter((r) => r.method !== 'GET')), [], 'browsing and review do not mutate');
          if (screen === 'resume') {
            await page.keyboard.press('Escape');
            assert.equal(await page.$('[data-modal-dialog]'), null);
            assert.equal(await page.evaluate(() => document.activeElement?.textContent), 'Review saved conversation', 'returns focus');
          }
        }
      }
    }
  }
  await page.setViewport({ width: 375, height: 812, isMobile: true, hasTouch: true });
  for (const screen of ['search', 'resume', 'history']) {
    await page.goto(`http://127.0.0.1:${server.address().port}/?screen=${screen}&theme=light&scale=l`, { waitUntil: 'networkidle0' });
    if (screen === 'resume') { await page.click('main > .ui-button'); await page.waitForSelector('.resume-card'); await page.click('.resume-card'); await page.waitForSelector('.resume-preview-message'); }
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen} fits large text`);
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-light-375-large.png`), fullPage: true });
  }
  assert.deepEqual(errors, [], 'no React/browser errors');
  console.log('history/search: themes, narrow, populated/empty/error/loading, transcript, resume review, focus return, no writes');
} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

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
  await build({ entryPoints: ['scripts/provider-handoff-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' },
    loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root" style="height:100dvh"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (req, res) => {
    const pathname = new URL(req.url, 'http://fixture.invalid').pathname;
    const name = pathname === '/' ? 'index.html' : pathname.slice(1);
    if (!['index.html', 'app.js', 'app.css', 'claude-icon.svg', 'openai-icon.svg'].includes(name)) {
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
    for (const [width, height] of [[1280, 1000], [375, 812]]) {
      await page.setViewport({ width, height, isMobile: width === 375, hasTouch: width === 375 });
      for (const screen of ['review', 'staged', 'uncertain', 'refused', 'accepted']) {
        await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}`, { waitUntil: 'networkidle0' });
        await page.waitForSelector('.handoff-destination');
        if (screen === 'review') {
          await page.locator('.handoff-dialog .ui-actions button').click();
          await page.waitForSelector('.handoff-coverage');
        } else {
          await page.locator('.handoff-saved summary').click();
          await page.locator('.handoff-saved button').click();
          await page.waitForSelector('.handoff-progress');
        }
        const bounds = await page.evaluate(() => {
          const el = document.querySelector('[data-modal-dialog]'); const r = el.getBoundingClientRect();
          return { left: r.left, right: r.right, top: r.top, bottom: r.bottom,
            overflow: el.scrollWidth > el.clientWidth, bg: getComputedStyle(el).backgroundColor };
        });
        assert.ok(bounds.left >= 0 && bounds.right <= width && bounds.top >= 0 && bounds.bottom <= height, JSON.stringify(bounds));
        assert.equal(bounds.overflow, false, 'no horizontal dialog overflow');
        assert.equal(bounds.bg, theme === 'light' ? 'rgb(255, 255, 255)' : 'rgb(20, 20, 20)');
        await page.keyboard.press('Tab');
        assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true);
        if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}.png`) });
        if (width === 375) {
          await page.$eval('[data-modal-dialog]', el => { el.scrollTop = el.scrollHeight; });
          if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}-bottom.png`) });
        }
        const mutations = await page.evaluate(() => window.handoffRequests.filter(r => r.method === 'POST' && !r.path.endsWith('/preview')));
        assert.deepEqual(mutations, [], 'viewing and checking never sends or creates');
        await page.keyboard.press('Escape');
        assert.equal(await page.$('[data-modal-dialog]'), null);
        await page.locator('#opener').click();
        await page.waitForSelector('[data-modal-dialog]');
        await page.keyboard.press('Escape');
        assert.equal(await page.evaluate(() => document.activeElement.id), 'opener', 'focus returns to the switch control');
      }
    }
  }
  for (const size of ['m', 'l']) {
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=dark&screen=review&size=${size}`, { waitUntil: 'networkidle0' });
    await page.evaluate(() => document.querySelector('.operations-shell').setAttribute('data-theme', 'light'));
    await page.waitForFunction(() => getComputedStyle(document.querySelector('[data-modal-dialog]')).backgroundColor === 'rgb(255, 255, 255)');
    const scaled = await page.$eval('[data-modal-dialog]', el => {
      const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, overflow: el.scrollWidth > el.clientWidth };
    });
    assert.ok(scaled.left >= 0 && scaled.right <= 375 && scaled.top >= 0 && scaled.bottom <= 812 && !scaled.overflow, `${size} fits narrow viewport`);
    await page.waitForFunction(() => [...document.querySelectorAll('.handoff-dialog button')].some(el => el.textContent === 'Review switch' && !el.disabled));
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true);
    assert.ok(await page.evaluate(() => parseFloat(getComputedStyle(document.activeElement).outlineWidth) >= 1.5));
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `review-light-375-${size}.png`) });
  }
  await page.keyboard.down('Shift');
  await page.keyboard.press('Tab');
  await page.keyboard.up('Shift');
  assert.equal(await page.evaluate(() => document.activeElement.textContent), 'Close', 'reverse Tab wraps to the final control');
  await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }]);
  assert.equal(await page.$eval('.ui-button', el => getComputedStyle(el).transitionDuration), '0s');
  assert.deepEqual(errors, []);
  console.log('provider handoff: production dialog review/staged/uncertain/refused/accepted dark/light desktop/375px, theme/scale/focus, no sends');

} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

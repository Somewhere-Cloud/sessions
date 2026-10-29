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
  await build({ entryPoints: ['scripts/settings-machines-fixture.tsx'], bundle: true, outdir: work,
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
      for (const screen of (process.env.UI_ONLY_REMAINING ? ['fleet', 'remaining-cards'] : ['remaining-cards', 'accounts', 'accounts-empty', 'general', 'agents', 'support', 'notifications', 'storage', 'connections', 'connect', 'connect-mobile', 'fleet', 'network', 'dialog'])) {
        await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}`, { waitUntil: 'networkidle0' });
        if (screen !== 'dialog') await page.waitForSelector('.ui-header');
        if (screen === 'remaining-cards') {
          await page.waitForFunction(() => document.body.textContent.includes('previous service restored'));
          assert.ok(await page.$('[aria-label="Local background service"]'));
          assert.match(await page.$eval('body', el => el.textContent), /Delivery is unverified/);
        }
        if (screen === 'dialog') {
          await page.click('button');
          await page.waitForSelector('[data-modal-dialog]');
          assert.equal(await page.$eval('[data-modal-dialog]', (el) => el.contains(document.activeElement)), true);
          await page.keyboard.press('Tab');
          assert.equal(await page.$eval('[data-modal-dialog]', (el) => el.contains(document.activeElement)), true);
          assert.ok(await page.evaluate(() => parseFloat(getComputedStyle(document.activeElement).outlineWidth) >= 1.5));
        }
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen} fits ${theme} ${width}`);
        const clipped = await page.$$eval('.ui-button, .ui-control', (els) => els.filter((el) => {
          const r = el.getBoundingClientRect(); return r.width && r.height && (r.left < -1 || r.right > innerWidth + 1);
        }).map((el) => el.outerHTML));
        assert.deepEqual(clipped, [], `${screen} controls stay visible`);
        if (width === 375) {
          const small = await page.$$eval('.ui-button, .ui-control', (els) => els.filter((el) => {
            const r = el.getBoundingClientRect(); return r.width && r.height && r.height < 44;
          }).map((el) => el.outerHTML));
          assert.deepEqual(small, [], `${screen} coarse targets`);
        }
        if (process.env.UI_SCREENSHOT_DIR && (!process.env.UI_SCREENSHOT_SCREEN || screen === process.env.UI_SCREENSHOT_SCREEN)) {
          const contentHeight = await page.evaluate(() => Math.max(document.documentElement.scrollHeight, document.querySelector('.operations-shell').scrollHeight));
          await page.setViewport({ width, height: Math.min(contentHeight, 6000), isMobile: width === 375, hasTouch: width === 375 });
          await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}.png`), fullPage: true });
          await page.setViewport({ width, height, isMobile: width === 375, hasTouch: width === 375 });
        }
        if (screen === 'fleet') {
          await page.focus('.fleet-account-offers summary');
          await page.keyboard.press('Enter');
          assert.equal(await page.$eval('.fleet-account-offers', el => el.open), true);
          assert.ok(await page.$('.fleet-account.is-missing .ui-button'));
          await page.keyboard.press('Enter');
          assert.equal(await page.$eval('.fleet-account-offers', el => el.open), false);
        }
        if (screen === 'dialog') {
          await page.keyboard.press('Escape');
          assert.equal(await page.$('[data-modal-dialog]'), null);
          assert.equal(await page.evaluate(() => document.activeElement.textContent), 'Add fixture account', 'focus returns to opener');
          await page.click('button');
          await page.click('[aria-label="ChatGPT"]');
          await page.click('.account-picker-footer button');
          await page.keyboard.press('Escape');
          await page.mouse.click(2, 2);
          assert.ok(await page.$('[data-modal-dialog]'), 'busy dialog cannot dismiss by Escape or backdrop');
          await page.keyboard.press('Tab');
          assert.equal(await page.$eval('[data-modal-dialog]', (el) => el.contains(document.activeElement)), true, 'all-disabled busy dialog contains focus');
        }
        const mutations = await page.evaluate(() => window.accountDaemon.requests.filter((r) => r.method !== 'GET'));
        assert.deepEqual(mutations, [], 'fake visual inspection never changes account/provider/network state');
      }
    }
  }
  for (const screen of ['fleet', 'remaining-cards']) {
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=light&screen=${screen}&size=l`, {waitUntil:'networkidle0'});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen} large text fits`);
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-light-375-large.png`), fullPage: true });
  }
  // Large shell scaling follows portaled dialog, preserving keyboard containment.
  for (const size of ['m', 'l']) {
    await page.goto(`http://127.0.0.1:${server.address().port}/?theme=light&screen=dialog&size=${size}`, {waitUntil:'networkidle0'});
    await page.click('button');
    const bounds = await page.$eval('[data-modal-dialog]', (el) => { const r=el.getBoundingClientRect(); return {left:r.left,right:r.right,top:r.top,bottom:r.bottom}; });
    assert.ok(bounds.left >= 0 && bounds.right <= 375 && bounds.top >= 0 && bounds.bottom <= 812, `${size} scaled picker fits`);
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path:join(process.env.UI_SCREENSHOT_DIR, `dialog-light-375-${size}.png`), fullPage:true });
  }
  assert.deepEqual(errors, []);
  console.log('settings/machines real components: themes, desktop/375px, coarse controls, focus, busy dismissal and scaled portals pass');

} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

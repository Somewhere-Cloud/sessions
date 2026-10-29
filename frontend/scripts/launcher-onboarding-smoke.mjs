import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-polish-'));
let server;
let browser;
try {
  await build({ entryPoints: ['scripts/launcher-onboarding-fixture.tsx'], bundle: true, outdir: work,
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
    for (const [width, height, scale] of [[1280, 900, 'm'], [375, 812, 'm'], [375, 812, 'l']]) {
      await page.setViewport({ width, height, isMobile: width === 375, hasTouch: width === 375 });
      for (const screen of ['launcher', 'onboarding', 'paid', 'menus']) {
        await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}&scale=${scale}`, { waitUntil: 'networkidle0' });
        await page.waitForSelector('main');
        if (screen === 'launcher') {
          await page.click('main > button');
          await page.waitForSelector('[role="dialog"]');
          await page.keyboard.press('Tab');
          assert.equal(await page.evaluate(() => !!document.activeElement.closest('[role="dialog"]')), true);
          await page.keyboard.press('Escape');
          assert.equal(await page.$eval('main > button', el => el === document.activeElement), true, 'launcher returns focus');
          await page.click('main > button');
        }
        if (screen === 'paid') {
          await page.waitForSelector('input[type="radio"]');
          await page.focus('input[type="radio"]');
          await page.keyboard.press('ArrowRight');
          assert.equal(await page.$eval('.paid-start-segments input:checked + span', el => el.textContent), 'Codex', 'native arrow-key choice');
        }
        if (screen === 'menus') {
          await page.focus('.session-actions-menu summary');
          await page.keyboard.press('Enter');
          await page.keyboard.press('Tab');
          assert.ok(await page.$('.session-actions-popover .ui-button.view-toggle-btn'), 'provider switch uses shared Button');
          if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `actions-${theme}-${width}-${scale}.png`), fullPage: true });
          await page.keyboard.press('Escape');
          assert.equal(await page.$eval('.session-actions-menu summary', el => el === document.activeElement), true, 'conversation actions return focus');
          await page.click('[aria-label="Settings"]');
          await page.keyboard.press('Tab');
          await page.keyboard.press('Escape');
          assert.equal(await page.$eval('[aria-label="Settings"]', el => el === document.activeElement), true);
          await page.click('[aria-label="Settings"]');
          assert.equal(await page.$eval('.settings-menu-popover', el => { const r = el.getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth; }), true, 'Settings fits enlarged viewport');
        }
        const capture = async (suffix = '') => {
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${screen}/${theme}/${width}/${scale} fits`);
          if (width === 375) assert.deepEqual(await page.$$eval('.ui-button, .ui-control, .paid-start-segments label', nodes => nodes.filter(el => {
            const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.height < 43;
          }).map(el => el.textContent || el.getAttribute('aria-label'))), [], `${screen} coarse controls stay 44px`);
          if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}-${scale}${suffix}.png`), fullPage: true });
        };
        await capture();
        if (screen === 'onboarding') {
          await page.keyboard.press('Escape');
          assert.ok(await page.$('[role="dialog"]'), 'onboarding cannot be dismissed');
          for (const [text, step] of [['Continue', 2], ['Keep sessions local', 3], ['Not now', 4]]) {
            await page.evaluate(text => [...document.querySelectorAll('button')].find(el => el.textContent === text).click(), text);
            await page.waitForSelector(`[aria-label="Step ${step} of 5"]`);
            await capture(`-step${step}`);
            if (step === 4) {
              await page.$eval('input[type="email"]', el => el.scrollIntoView({ block: 'center' }));
              await capture('-step4-fields');
            }
          }
          assert.equal(await page.$eval('input[type="email"]', el => el.labels[0].textContent.includes('Email address')), true);
          await page.evaluate(() => [...document.querySelectorAll('button')].find(el => el.textContent.startsWith('Skip')).click());
          await page.waitForSelector('[aria-label="Step 5 of 5"]');
          await capture('-step5');
          assert.deepEqual(await page.evaluate(() => window.polishChoices), [], 'browsing never grants consent');
          await page.evaluate(() => [...document.querySelectorAll('button')].find(el => el.textContent === 'Make them inherit my permissions').click());
          assert.deepEqual(await page.evaluate(() => window.polishChoices), [['local-only', 'inherit']], 'explicit exact consent choice');
        }
        assert.deepEqual(await page.evaluate(() => window.polishDaemon.requests.filter(r => r.method !== 'GET')), [], 'review never mutates or starts paid work');
      }
    }
  }
  assert.deepEqual(errors, [], 'no React/browser errors');
  console.log('launcher/onboarding: themes, desktop/375px/large text, keyboard and explicit consent pass');
} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

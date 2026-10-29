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
  await build({ entryPoints: ['scripts/ui-foundation-fixture.tsx'], bundle: true, outdir: work,
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
      for (const screen of ['gallery', 'fork', 'restart']) {
        await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&screen=${screen}`, { waitUntil: 'networkidle0' });
        await page.waitForSelector('.ui-header');
        const bounds = await page.evaluate(() => ({
          overflow: document.documentElement.scrollWidth > innerWidth,
          controls: [...document.querySelectorAll('[data-modal-dialog] .ui-button, [data-modal-dialog] .ui-control' + (document.querySelector('[data-modal-dialog]') ? '' : ', .ui-button, .ui-control'))].map((el) => {
            const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, height: r.height };
          }),
          foreground: getComputedStyle(document.querySelector('[data-modal-dialog]') ?? document.querySelector('.ui-surface')).color,
          background: getComputedStyle(document.querySelector('[data-modal-dialog]') ?? document.querySelector('.ui-surface')).backgroundColor
        }));
        if (screen === 'gallery') {
          const contrast = await page.evaluate(() => {
            const style = getComputedStyle(document.querySelector('.operations-shell'));
            const luminance = (hex) => {
              const rgb = hex.trim().slice(1).match(/../g).map((v) => parseInt(v, 16) / 255);
              return rgb.map((v) => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4)
                .reduce((sum, v, i) => sum + v * [.2126, .7152, .0722][i], 0);
            };
            return ['--fg', '--fg-dim', '--fg-faint'].flatMap((fg) =>
              ['--bg-base', '--bg-elev-1', '--bg-elev-2', '--bg-elev-3'].map((bg) => {
                const a = luminance(style.getPropertyValue(fg)), b = luminance(style.getPropertyValue(bg));
                return { fg, bg, ratio: (Math.max(a, b) + .05) / (Math.min(a, b) + .05) };
              }));
          });
          assert.ok(contrast.every((pair) => pair.ratio >= 4.5), `${theme} text tokens meet AA on every surface: ${JSON.stringify(contrast)}`);
          assert.ok(await page.$eval('.ui-field .field-help', (el) => parseFloat(getComputedStyle(el).fontSize) >= 13), 'instructions stay at least 13px');
        }
        assert.equal(bounds.overflow, false, `${screen} has no horizontal overflow at ${width}px`);
        assert.ok(bounds.controls.every((r) => r.left >= 0 && r.right <= width), `${screen} controls fit at ${width}px`);
        if (width === 375) assert.ok(bounds.controls.every((r) => r.height >= 44), 'coarse pointer controls, including compact, have 44px targets');
        assert.equal(bounds.foreground, theme === 'light' ? 'rgb(23, 26, 31)' : 'rgb(232, 232, 232)', 'theme foreground follows portaled surfaces');
        assert.equal(bounds.background, theme === 'light' ? 'rgb(255, 255, 255)' : 'rgb(25, 29, 32)', `${screen} follows ${theme} theme including body portals`);
        if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-${theme}-${width}.png`), fullPage: true });
        if (screen === 'gallery' && process.env.UI_SCREENSHOT_DIR) {
          await page.$eval('.operations-shell', (el) => { el.scrollTop = el.scrollHeight; });
          await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `states-${theme}-${width}.png`), fullPage: true });
        }
        if (screen !== 'gallery') {
          await page.keyboard.press('Escape');
          assert.equal(await page.$('[data-modal-dialog]'), null, 'Escape cancels the review');
        }
        const mutations = await page.evaluate(() => window.foundationDaemon.requests.filter((r) => r.method === 'POST' && !r.path.endsWith('/preview')));
        assert.deepEqual(mutations, [], 'rendering and cancelling never starts or ends an agent');
      }
    }
  }
  // Both inline and body portals scale once and follow a mounted shell's theme.
  for (const size of ['m', 'l']) {
    for (const screen of ['fork', 'restart']) {
      await page.goto(`http://127.0.0.1:${server.address().port}/?theme=dark&screen=${screen}&size=${size}`, { waitUntil: 'networkidle0' });
      await page.evaluate(() => document.querySelector('.operations-shell').setAttribute('data-theme', 'light'));
      await page.waitForFunction(() => getComputedStyle(document.querySelector('[data-modal-dialog]')).backgroundColor === 'rgb(255, 255, 255)');
      await page.evaluate(() => Promise.all(document.getAnimations().filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity).map((animation) => animation.finished.catch(() => {}))));
      const dialog = await page.$eval('[data-modal-dialog]', (el) => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; });
      assert.ok(dialog.left >= 0 && dialog.right <= 375 && dialog.top >= 0 && dialog.bottom <= 812, `${size} ${screen} stays inside narrow viewport`);
      await page.keyboard.press('Tab');
      assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true, 'keyboard focus remains in review');
      assert.ok(await page.evaluate(() => parseFloat(getComputedStyle(document.activeElement).outlineWidth) >= 1.5), 'keyboard focus remains visible under zoom');
      if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${screen}-light-375-${size}.png`), fullPage: true });
    }
  }
  await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }]);
  assert.equal(await page.$eval('.ui-button', (el) => getComputedStyle(el).transitionDuration), '0s');
  assert.deepEqual(errors, [], 'no React/browser errors');
  console.log('shared foundation: gallery/Add agent/Restart pass light/dark desktop/375px, large portal, reduced motion, cancellation');

} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

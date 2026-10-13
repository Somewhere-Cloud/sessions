import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, extname } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { smoke, closeBrowser, closeServer } from './lib/smoke.mjs';

const t = smoke('resume-layout');
const work = await mkdtemp(join(tmpdir(), 'sessions-resume-layout-'));
let browser;
let server;
try {
  t.scenario('bundle the real resume picker over a fake daemon');
  await build({ entryPoints: ['scripts/resume-layout-fixture.tsx'], outdir: work, bundle: true,
    format: 'esm', platform: 'browser', entryNames: 'app', jsx: 'automatic', external: ['/claude-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (request, response) => {
    const name = new URL(request.url, 'http://fixture').pathname.slice(1) || 'index.html';
    if (!['index.html', 'app.js', 'app.css', 'claude-icon.svg', 'openai-icon.svg'].includes(name)) {
      response.writeHead(404); response.end(); return;
    }
    try {
      const body = await readFile(join(name.endsWith('.svg') ? 'public' : work, name));
      response.writeHead(200, { 'content-type': { '.css': 'text/css', '.js': 'text/javascript', '.svg': 'image/svg+xml' }[extname(name)] || 'text/html' });
      response.end(body);
    } catch { response.writeHead(404); response.end(); }
  });
  await t.bounded(new Promise((resolve) => server.listen(0, '127.0.0.1', resolve)), 'fixture to bind', 15_000);
  browser = await t.bounded(puppeteer.launch({ headless: true, args: ['--no-sandbox'] }), 'Chromium to launch', 60_000);
  const page = await browser.newPage();
  t.watch(page);
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const [width, height] of [[1280, 900], [390, 844], [375, 667]]) {
    for (const theme of ['dark', 'light']) {
      for (const scale of ['m', 'l']) {
        t.scenario(`${width}px ${theme} ${scale}: history preview and explicit start remain accessible`);
        await page.setViewport({ width, height });
        await page.goto(`http://127.0.0.1:${server.address().port}/?theme=${theme}&scale=${scale}`, { waitUntil: 'domcontentloaded' });
        await page.waitForSelector('.resume-card');
        assert.equal(await page.$$eval('.resume-card', (nodes) => nodes.length), 20);
        await page.click('.resume-card');
        await page.waitForSelector('.resume-preview-message');
        await page.waitForFunction(() => !document.querySelector('.continuation-start').disabled);
        const layout = await page.evaluate(() => {
          const rect = (selector) => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, height: r.height }; };
          return { dialog: rect('.resume-dialog'), body: rect('.resume-dialog-body'), preview: rect('.resume-preview'), start: rect('.continuation-start'),
            settingsClosed: !document.querySelector('.resume-settings').open, writes: window.resumeLayoutDaemon.requests.filter((request) => request.method !== 'GET') };
        });
        assert.ok(layout.dialog.left >= -1 && layout.dialog.right <= width + 1, 'scaled picker fits horizontally');
        assert.ok(layout.body.height >= 125, `settings must not crowd the reader out: ${width}px/${scale} has ${layout.body.height}px`);
        assert.ok(layout.preview.top >= layout.body.top - 1 && layout.preview.top < layout.body.bottom, `preview begins inside the reader: ${width}px/${scale} ${JSON.stringify(layout)}`);
        assert.equal(layout.settingsClosed, true);
        assert.deepEqual(layout.writes, [], 'preview must never launch, resume, or write');
        await page.$eval('.continuation-start', (button) => button.scrollIntoView({ block: 'nearest' }));
        const start = await page.$eval('.continuation-start', (button) => { const r = button.getBoundingClientRect(); return { top: r.top, bottom: r.bottom }; });
        assert.ok(start.bottom <= height && start.top >= 0, 'explicit Start is reachable without hiding the preview');
        await page.click('.resume-settings summary');
        await page.waitForFunction(() => document.querySelector('.resume-settings').open);
        assert.equal(await page.$eval('[aria-label="Resume permissions"]', (node) => node.value), 'full');
        await page.click('.resume-settings summary');
      }
    }
  }
  assert.deepEqual(errors, []);
  console.log('Resume layout passed: bounded previews, visible Start, medium/large type, dark/light, desktop/390px/375px; no writes');
} finally {
  await closeBrowser(browser);
  if (server) await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

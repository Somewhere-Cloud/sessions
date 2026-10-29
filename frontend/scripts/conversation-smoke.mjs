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
const networkTrace = [];
const trace = (...entry) => { networkTrace.push([Date.now(), ...entry]); if (networkTrace.length > 80) networkTrace.shift(); };
try {
  await build({ entryPoints: ['scripts/conversation-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' },
    loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root" style="height:100dvh"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (req, res) => {
    trace('http-request', req.url);
    res.on('finish', () => trace('http-finish', req.url));
    res.on('close', () => trace('http-close', req.url));
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
  await page.evaluateOnNewDocument(() => localStorage.clear());
  const errors = [];
  page.on('pageerror', (error) => { errors.push(error.message); console.error(error.message); });
  const root = `http://127.0.0.1:${server.address().port}/`;
  // All production IO is replaced in-process, and browser traffic is fixture-only.
  await page.setRequestInterception(true);
  const pendingRequests = new Set();
  const network = await page.createCDPSession();
  await network.send('Network.enable');
  await network.send('Page.enable');
  await network.send('Page.setLifecycleEventsEnabled', { enabled: true });
  network.on('Network.responseReceived', (event) => trace('cdp-response', event.requestId, event.response.url, event.response.status));
  network.on('Network.loadingFinished', (event) => trace('cdp-finished', event.requestId));
  network.on('Page.lifecycleEvent', (event) => trace('cdp-lifecycle', event.name));
  page.on('request', (request) => {
    pendingRequests.add(request);
    trace('intercept', request.url());
    void (request.url().startsWith(root) || request.url().startsWith('data:') ? request.continue() : request.abort())
      .then(() => trace('intercept-settled', request.url()), (error) => { errors.push(error.message); trace('intercept-error', request.url(), error.message); });
  });
  page.on('requestfinished', (request) => pendingRequests.delete(request));
  page.on('requestfailed', (request) => pendingRequests.delete(request));
  const capture = async (name) => {
    if (process.env.UI_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.UI_SCREENSHOT_DIR, `${name}.png`), fullPage: true });
  };
  const fits = async (name) => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${name} fits viewport`);
  for (const theme of ['dark', 'light']) {
    for (const width of [1280, 375]) {
      await page.setViewport({ width, height: width === 375 ? 812 : 1000, isMobile: width === 375, hasTouch: width === 375 });
      for (const state of ['reading', 'streaming', 'queue', 'approval', 'fault', 'recovery', 'empty', 'loading', 'error', 'delivery', 'sending', 'steering', 'saved', 'terminal', 'session', 'details']) {
        const name = `conversation-${state}-${theme}-${width}`;
        console.log('Conversation scenario', name);
        try {
          await page.goto(`${root}?theme=${theme}&state=${state}`, { waitUntil: 'networkidle0' });
        } catch (error) {
          console.error('Navigation diagnostic', name, { url: page.url(), pending: [...pendingRequests].map((request) => request.url()), networkTrace });
          throw error;
        }
        await page.waitForSelector('.operations-main');
        if (state === 'session') await page.waitForSelector('.session-active-header');
        if (await page.$('.remote-scroll')) {
          await page.$eval('.remote-scroll', (el) => { el.scrollTop = ['reading', 'session'].includes(new URLSearchParams(location.search).get('state')) ? 0 : el.scrollHeight; });
          await new Promise((resolve) => setTimeout(resolve, 150));
        }
        if (state === 'error') {
          await page.type('textarea', 'Keep this draft if the send fails.');
          await page.click('[aria-label="Send"]');
          await page.waitForSelector('[role="alert"]');
          assert.equal(await page.$eval('textarea', (el) => el.value), 'Keep this draft if the send fails.');
        }
        if (['delivery', 'sending', 'steering'].includes(state)) {
          await page.type('textarea', 'Please keep this focused.');
          await page.click(state === 'steering' ? '[aria-label="Send follow-up"]' : '[aria-label="Send"]');
          await page.waitForSelector(state === 'delivery' ? '.delivery-review' : state === 'sending' ? '.input-delivery-pending' : '.input-send');
          if (state === 'delivery') assert.equal(await page.$eval('textarea', (el) => el.value), 'Please keep this focused.');
          if (state === 'steering') assert.deepEqual(await page.evaluate(() => window.conversationFixture.calls), [['steer', 'Please keep this focused.']]);
        }
        if (state === 'saved') assert.ok(await page.$eval('.saved-recovery-resume', (el) => el.getBoundingClientRect().height >= 44), 'saved recovery keeps its 44px target');
        await fits(name);
        if (width === 375 && await page.$('.input-send')) assert.ok(await page.$eval('.input-send', (el) => el.getBoundingClientRect().height >= 44), 'coarse send target is at least 44px');
        await capture(name);
        if (state === 'reading') {
          // Wide code/table scroll locally, never pushing the message column out.
          assert.equal(await page.$eval('.md-content table', (el) => el.scrollWidth > el.clientWidth), true);
          await page.$eval('.remote-scroll', (el) => { el.scrollTop = 0; });
          await new Promise((resolve) => setTimeout(resolve, 100));
          const before = await page.$eval('.remote-scroll', (el) => el.scrollTop);
          await page.evaluate(() => window.appendConversation());
          await new Promise((resolve) => setTimeout(resolve, 120));
          assert.ok(Math.abs(await page.$eval('.remote-scroll', (el) => el.scrollTop) - before) < 3, 'new history preserves reading position');
          await capture(`${name}-code-table`);
          await page.waitForSelector('.work-log-summary');
          await page.click('.work-log-summary');
          await page.click('.work-log-row-head');
          await capture(`${name}-activity`);
          await page.click('[aria-label="Scroll to latest"]');
          assert.equal(await page.$eval('.remote-scroll', (el) => el.scrollHeight - el.scrollTop - el.clientHeight < 5), true, 'latest restores bottom anchor');
          await page.type('textarea', 'A multiline draft');
          await page.keyboard.down('Shift'); await page.keyboard.press('Enter'); await page.keyboard.up('Shift');
          await page.type('textarea', 'with room to think.');
          await page.click('.composer-model-trigger');
          await page.waitForSelector('[data-modal-dialog]');
          await capture(`${name}-model`);
          await page.click('.model-picker-trigger');
          await page.waitForSelector('[role="combobox"]');
          await page.waitForFunction(() => document.activeElement?.getAttribute('role') === 'combobox');
          assert.equal(await page.evaluate(() => document.activeElement.getAttribute('role')), 'combobox', 'owned model search retains focus');
          assert.equal(await page.$eval('.model-picker-popover', (el) => getComputedStyle(el).backgroundColor), await page.$eval('[data-modal-dialog]', (el) => getComputedStyle(el).backgroundColor), 'owned model search shares dialog palette');
          await capture(`${name}-model-search`);
          await page.keyboard.press('Escape');
          assert.ok(await page.$('[data-modal-dialog]'), 'nested Escape keeps model review open');
          await page.keyboard.press('Tab');
          assert.equal(await page.evaluate(() => document.querySelector('[data-modal-dialog]').contains(document.activeElement)), true);
          await page.keyboard.press('Escape');
          assert.equal(await page.evaluate(() => document.activeElement.classList.contains('composer-model-trigger')), true, 'model review returns focus');
          assert.equal(await page.$eval('textarea', (el) => el.value), 'A multiline draft\nwith room to think.');
          // The native file picker flow inserts an attachment without submitting it.
          await page.$eval('input[type="file"]', (el) => {
            const transfer = new DataTransfer(); transfer.items.add(new File(['fixture'], 'screen shot.png', { type: 'image/png' }));
            el.files = transfer.files; el.dispatchEvent(new Event('change', { bubbles: true }));
          });
          await page.waitForFunction(() => document.querySelector('textarea').value.includes('screen shot.png'));
          assert.equal(await page.evaluate(() => window.conversationFixture.calls.length), 0, 'draft, model review and attachment never send');
          await capture(`${name}-attachment`);
        }
        if (state === 'session' || state === 'terminal') assert.ok(await page.$eval('.session-toolbar', (toolbar) => {
          const bounds = toolbar.getBoundingClientRect();
          return [...toolbar.querySelectorAll('button')].every((button) => { const r = button.getBoundingClientRect(); return r.top >= bounds.top - 1 && r.bottom <= bounds.bottom + 1; });
        }), `${name}: conversation toolbar contains shared control targets`);
        if (state === 'terminal') {
          await page.click('.terminal-drawer-toggle');
          await page.waitForSelector('.terminal-drawer-header');
          assert.ok(await page.$eval('.terminal-drawer-header', (header) => {
            const bounds = header.getBoundingClientRect();
            const buttonsFit = [...header.querySelectorAll('button')].every((button) => { const r = button.getBoundingClientRect(); return r.top >= bounds.top - 1 && r.bottom <= bounds.bottom + 1; });
            return buttonsFit && header.parentElement.querySelector('.terminal-host').getBoundingClientRect().top >= bounds.bottom - 1;
          }), 'terminal toolbar reserves space for shared touch targets');
          await capture(`${name}-drawer`);
          await page.click('[aria-label="Close terminal drawer"]');
        }
        if (state === 'session') {
          await page.type('textarea', 'Keep this draft while I inspect details.');
          await page.click('.session-actions-menu > summary');
          await page.waitForSelector('.details-inspector-button');
          await capture(`${name}-actions`);
          await page.click('.details-inspector-button');
          await page.waitForSelector('.session-details-view');
          await page.keyboard.press('Escape');
          assert.equal(await page.evaluate(() => document.activeElement.matches('.session-actions-menu > summary')), true, 'actions Escape returns to summary');
          await capture(`${name}-details`);
          await page.click('.session-actions-menu > summary');
          await page.click('.details-inspector-button');
          await page.keyboard.press('Escape');
          assert.equal(await page.$eval('textarea', (el) => el.value), 'Keep this draft while I inspect details.', 'details preserves mounted composer draft');
          assert.deepEqual(await page.evaluate(() => window.conversationFixture.daemon.requests.filter((request) => request.method === 'POST')), [], 'details navigation is read-only');
        }
        if (state === 'approval') {
          await page.click('.provider-control-card-choices button');
          assert.deepEqual(await page.evaluate(() => window.conversationFixture.calls), [['approval', 'allow']]);
        }
      }
    }
  }
  for (const state of ['reading', 'approval', 'empty', 'details']) {
    await page.setViewport({ width: 375, height: 812, isMobile: true, hasTouch: true });
    console.log('Conversation scenario', `${state}-light-375-large`);
    await page.goto(`${root}?state=${state}&theme=light&size=l`, { waitUntil: 'networkidle0' });
    await fits(`${state} large text`);
    if (await page.$('.remote-scroll')) await page.$eval('.remote-scroll', (el) => { el.scrollTop = 0; });
    await capture(`conversation-${state}-light-375-large`);
    if (state === 'reading') {
      await page.click('.composer-model-trigger');
      await page.waitForSelector('[data-modal-dialog]');
      assert.equal(await page.$eval('[data-modal-dialog]', (el) => { const r = el.getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight; }), true, 'large text portal fits');
      await capture('conversation-model-light-375-large');
    }
  }
  assert.deepEqual(errors, []);
  console.log('conversation UI: themes, desktop/narrow/large text, production messages/code/tables/activity, draft/focus/scroll, approval and attachment checks passed');
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

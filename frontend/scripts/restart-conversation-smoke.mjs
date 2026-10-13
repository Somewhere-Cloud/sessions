import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-restart-ui-'));
const laneId = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb';
let browser; let server; let partial = false; let previewAnswer = {};
const requests = []; const failed = []; const errors = [];
try {
  await build({ entryPoints: [fileURLToPath(new URL('./restart-conversation-fixture.tsx', import.meta.url))], outdir: work,
    bundle: true, platform: 'browser', format: 'esm', entryNames: 'app',
    define: { 'import.meta.env.BASE_URL': '"/"' }, external: ['/claude-icon.svg'], logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (request, response) => {
    if (request.url === '/api/recovery/restart/preview') {
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ sourceSessionId: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', accountChanged: true, savedLoginEmail: 'fixture@example.test', warning: 'Saved login differs from the recorded Remote Control owner.', ...previewAnswer })); return;
    }
    if (request.url === '/api/sessions?include_exited=1') {
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ sessions: [{ id: laneId, args: [], tool: 'claude-code', cwd: '/fixture', createdAt: Date.now(), exited: false }] })); return;
    }
    if (request.url === '/api/recovery/restart') {
      let body = ''; for await (const chunk of request) body += chunk;
      requests.push(JSON.parse(body)); const needsRepair = partial && requests.length === 1;
      response.writeHead(needsRepair ? 202 : 200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ ok: !needsRepair, partial: needsRepair, sourceEnded: true, sourceSessionId: requests.at(-1).sourceSessionId,
        operationId: 'stable-recorded-operation', laneId, adoption: needsRepair ? { warning: 'Replacement is running; its history link needs repair.' } : undefined })); return;
    }
    const name = request.url === '/' ? 'index.html' : request.url.slice(1);
    if (!['index.html', 'app.js', 'app.css', 'favicon.ico'].includes(name)) { response.writeHead(404); response.end(); return; }
    if (name === 'favicon.ico') { response.writeHead(204); response.end(); return; }
    try { response.writeHead(200, { 'content-type': name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' }); response.end(await readFile(join(work, name))); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage(); page.setDefaultTimeout(10000);
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('requestfailed', (request) => failed.push(request.url()));
  page.on('response', (response) => { if (response.status() >= 400) failed.push(`${response.status()} ${response.url()}`); });
  await page.setViewport({ width: 1100, height: 850 });
  const url = `http://127.0.0.1:${server.address().port}`;
  const clickText = async (text) => {
    await page.waitForFunction((label) => [...document.querySelectorAll('button')].some((item) => item.textContent === label && !item.disabled), {}, text);
    await page.evaluate((label) => { const button = [...document.querySelectorAll('button')].find((item) => item.textContent === label && !item.disabled); if (!button) throw new Error(`missing enabled button ${label}`); button.click(); }, text);
  };
  await page.goto(url); await page.waitForSelector('textarea');
  await clickText('Restart / change permissions…'); await page.waitForSelector('[role=dialog]');
  const review = await page.$eval('[role=dialog]', (element) => element.textContent);
  assert.match(review, /aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee/); assert.match(review, /PID 4242/); assert.match(review, /fixture-work/); assert.match(review, /fixture-model/);
  assert.match(review, /On This computer/); assert.match(review, /not necessarily the account/);
  await clickText('Cancel'); assert.equal(requests.length, 0);
  await page.type('textarea', 'My exact unsent draft');
  await clickText('Restart / change permissions…');
  await page.select('[aria-label="Restart permissions"]', 'full'); await page.click('input[type=checkbox]');
  if (process.env.RESTART_UI_SCREENSHOT) await page.screenshot({ path: process.env.RESTART_UI_SCREENSHOT });
  await clickText('End this runtime and reopen'); await page.waitForSelector('[data-opened]');
  assert.equal(await page.$eval('[data-opened]', (element) => element.textContent), laneId);
  assert.equal(await page.$eval('textarea', (element) => element.value), 'My exact unsent draft');
  assert.deepEqual(requests[0], { sourceSessionId: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', confirmSessionId: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', permissions: 'full', remoteControl: true, runtimeMode: 'terminal' });
  partial = true; requests.length = 0;
  await page.reload(); await page.waitForSelector('textarea');
  await clickText('Restart / change permissions…'); await clickText('End this runtime and reopen'); await page.waitForSelector('[role=alert]');
  await page.click('[data-hide-source]');
  assert.match(await page.$eval('[role=dialog]', (element) => element.textContent), /history link needs repair/);
  assert.equal(await page.$eval('[aria-label="Restart permissions"]', (element) => element.disabled), true);
  await clickText('Retry same restart'); await page.waitForSelector('[data-opened]');
  assert.deepEqual(requests[0], requests[1]); assert.equal(requests.length, 2);
  assert.equal(await page.$eval('textarea', (element) => element.value), 'My exact unsent draft');

  // The daemon's preview decides what the dialog offers. A record it would
  // resume or open gets that action, never a button that ends a runtime.
  const source = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee';
  const buttons = () => page.$$eval('[role=dialog] button', (items) => items.map((item) => item.textContent));
  const reviewWith = async (answer) => {
    previewAnswer = answer; partial = false; requests.length = 0;
    await page.reload(); await page.waitForSelector('textarea');
    await clickText('Restart / change permissions…'); await page.waitForSelector('[role=dialog]');
  };
  await reviewWith({ state: 'lost', restartable: false, action: 'resume', command: `sessions resume ${source}`, targetSessionId: source,
    reason: 'This runtime is no longer running, so restart has nothing to end. Resume reopens the same saved conversation with its recorded profile.' });
  await page.waitForFunction(() => [...document.querySelectorAll('[role=dialog] button')].some((item) => item.textContent === 'Resume conversation'));
  assert.deepEqual(await buttons(), ['Close', 'Resume conversation']);
  if (process.env.RESTART_UI_SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.RESTART_UI_SCREENSHOT_DIR}/restart-preview-resume.png` });
  await clickText('Resume conversation'); await page.waitForSelector('[data-resumed]');
  assert.equal(await page.$eval('[data-resumed]', (element) => element.textContent), source);
  assert.deepEqual(requests, [], 'a resume answer never posts a restart');

  await reviewWith({ state: 'exited', restartable: false, action: 'open', command: `sessions attach ${laneId}`, targetSessionId: laneId,
    reason: 'aaaaaaaa was already continued, and bbbbbbbb is running now; open it instead of starting a second runner on the same conversation' });
  await page.waitForFunction(() => [...document.querySelectorAll('[role=dialog] button')].some((item) => item.textContent === 'Open running copy'));
  assert.deepEqual(await buttons(), ['Close', 'Open running copy']);
  if (process.env.RESTART_UI_SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.RESTART_UI_SCREENSHOT_DIR}/restart-preview-open.png` });
  await clickText('Open running copy'); await page.waitForSelector('[data-opened]');
  assert.equal(await page.$eval('[data-opened]', (element) => element.textContent), laneId);
  assert.deepEqual(requests, [], 'opening the running copy never posts a restart');

  await reviewWith({ state: 'moved', restartable: false, action: 'none',
    reason: 'This conversation moved to https://mini.example.test:8787. Open it there; resuming or restarting it here would fork it.' });
  await page.waitForFunction(() => document.querySelector('[role=dialog] [role=status]')?.textContent?.includes('would fork it'));
  assert.deepEqual(await buttons(), ['Close']);
  if (process.env.RESTART_UI_SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.RESTART_UI_SCREENSHOT_DIR}/restart-preview-none.png` });
  await clickText('Close');
  assert.deepEqual(requests, []);

  const recorded = { permissions: 'constrained', remoteControl: false, runtimeMode: 'terminal' };
  await reviewWith({ state: 'running', restartable: true, action: 'retry-restart', recordedRestart: recorded,
    command: `sessions restart ${source} --confirm ${source} --permissions constrained --terminal`,
    reason: 'A restart of this runtime is already recorded. Repeating exactly these choices finishes it or returns its replacement without starting another runtime; different choices are refused.' });
  await page.waitForFunction(() => [...document.querySelectorAll('[role=dialog] button')].some((item) => item.textContent === 'Retry recorded restart'));
  assert.equal(await page.$eval('[aria-label="Restart permissions"]', (element) => element.value), 'constrained');
  assert.equal(await page.$eval('[aria-label="Restart permissions"]', (element) => element.disabled), true);
  if (process.env.RESTART_UI_SCREENSHOT_DIR) await page.screenshot({ path: `${process.env.RESTART_UI_SCREENSHOT_DIR}/restart-preview-recorded.png` });
  await clickText('Retry recorded restart'); await page.waitForSelector('[data-opened]');
  assert.deepEqual(requests, [{ sourceSessionId: source, confirmSessionId: source, ...recorded }]);

  assert.deepEqual(errors, []); assert.deepEqual(failed, []);
  console.log('restart UI passed: exact runtime review, cancel, YOLO + Remote Control, automatic reopen, draft copy, source retirement, partial retry, preview resume/open/none without a restart, recorded retry with exact choices, no failed requests');
} finally { await closeBrowser(browser); await closeServer(server); await rm(work, { recursive: true, force: true }); }

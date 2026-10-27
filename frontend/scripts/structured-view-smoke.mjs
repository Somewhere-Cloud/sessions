import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { extname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { smoke, closeBrowser, closeServer } from './lib/smoke.mjs';

const t = smoke('structured-view');
const work = await mkdtemp(join(tmpdir(), 'sessions-structured-view-'));
const publicDir = fileURLToPath(new URL('../public/', import.meta.url));
const screenshot = process.env.STRUCTURED_VIEW_SCREENSHOT || join(work, 'structured-view.png');
let browser;
let server;

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function withTimeout(promise, label, milliseconds = 10_000) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${label} timed out after ${milliseconds}ms`)), milliseconds);
      })
    ]);
  } finally {
    clearTimeout(timer);
  }
}

try {
  await build({
    entryPoints: [fileURLToPath(new URL('./structured-view-fixture.tsx', import.meta.url))],
    outdir: work,
    bundle: true,
    platform: 'browser',
    format: 'esm',
    define: { 'import.meta.env.BASE_URL': '"/"' },
    entryNames: 'app',
    assetNames: 'asset-[hash]',
    external: ['/claude-icon.svg'],
    loader: { '.svg': 'dataurl', '.png': 'dataurl' },
    logLevel: 'silent'
  });
  await writeFile(join(work, 'index.html'), `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<link rel="stylesheet" href="/app.css"></head><body><div id="root"></div>
<script>localStorage.setItem('sessions:servers', JSON.stringify([{id:'fixture',name:'Fixture',host:'127.0.0.1',port:8787,isDefault:true}]));localStorage.setItem('sessions:active-server','fixture');</script>
<script type="module" src="/app.js"></script></body></html>`);

  server = createServer(async (request, response) => {
    const pathname = new URL(request.url, 'http://fixture').pathname;
    const name = pathname === '/' ? 'index.html' : pathname.slice(1);
    try {
      const source = name === 'openai-icon.svg' || name === 'claude-icon.svg'
        ? join(publicDir, name)
        : join(work, name);
      const body = await readFile(source);
      const type = extname(name) === '.css'
        ? 'text/css'
        : extname(name) === '.js'
          ? 'text/javascript'
          : extname(name) === '.svg'
            ? 'image/svg+xml'
            : extname(name) === '.png'
              ? 'image/png'
              : 'text/html';
      response.writeHead(200, { 'content-type': type });
      response.end(body);
    } catch {
      response.writeHead(404);
      response.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('fixture server did not bind');

  browser = await puppeteer.launch({ headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage();
  page.setDefaultTimeout(10_000);
  page.setDefaultNavigationTimeout(10_000);
  await page.setViewport({ width: 1440, height: 960, deviceScaleFactor: 1 });
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  t.watch(page);
  await page.goto(`http://127.0.0.1:${address.port}`, { waitUntil: 'domcontentloaded' });
  t.scenario('the structured assistant bubble renders its plan and an ordered work log');
  await t.waitForSelector(page, '.remote-bubble-plan', 'the assistant bubble to render its plan block');
  const foldedRows = await page.$$eval('.work-log-row', (rows) => rows.length);
  await page.$eval('.work-log-summary', (element) => element.click());
  await page.$eval('.work-log-row.is-tool .work-log-row-head', (element) => element.click());

  const report = await page.evaluate(() => ({
    planSteps: document.querySelectorAll('.remote-bubble-plan-step').length,
    rowKinds: Array.from(document.querySelectorAll('.work-log-row')).map((row) =>
      ['is-thinking', 'is-tool', 'is-update'].find((kind) => row.classList.contains(kind)) ?? ''),
    activitySummary: document.querySelector('.work-log-summary')?.textContent?.replace(/\s+/g, ' ').trim() ?? '',
    activityLabels: Array.from(document.querySelectorAll('.work-log-row.is-tool .work-log-row-label')).map((element) => element.textContent?.trim() ?? ''),
    toolDetail: document.querySelector('.work-log-detail-output')?.textContent ?? '',
    thinking: document.querySelector('.work-log-row.is-thinking .work-log-row-label')?.textContent ?? '',
    updateTexts: Array.from(document.querySelectorAll('.work-log-row.is-update .work-log-row-label'))
      .map((element) => element.textContent?.trim() ?? ''),
    oldChips: document.querySelectorAll('.remote-bubble-tools, .remote-bubble-reasoning, .remote-bubble-updates').length,
    logBeforeAnswer: Boolean(document.querySelector('.work-log')?.compareDocumentPosition(
      document.querySelector('.remote-bubble-assistant > .remote-bubble-content')
    ) & Node.DOCUMENT_POSITION_FOLLOWING),
    attachControl: document.querySelector('.input-attach')?.textContent ?? '',
    quickKeyCount: document.querySelectorAll('.qk-btn').length,
    timestampCount: document.querySelectorAll('.remote-message-meta time').length,
    messageAuthor: document.querySelector('.remote-message-author')?.textContent ?? '',
    userCardRadius: getComputedStyle(document.querySelector('.remote-bubble-user')).borderRadius,
    assistantBackground: getComputedStyle(document.querySelector('.remote-bubble-assistant')).backgroundColor,
    runState: document.querySelector('.sidebar-run-state')?.textContent ?? '',
    horizontalOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth
  }));
  assert.equal(report.planSteps, 3);
  // A finished turn folds to one line; opening it shows every step in order.
  assert.equal(foldedRows, 0);
  assert.equal(report.activitySummary.replace(/[›⌄]/g, '').trim(), 'Thought, ran a command, edited a file');
  assert.deepEqual(report.rowKinds, ['is-thinking', 'is-tool', 'is-tool', 'is-update', 'is-update']);
  assert.match(report.thinking, /hard lifecycle boundaries/);
  assert.match(report.activityLabels[0] ?? '', /go test/);
  assert.doesNotMatch(report.activityLabels[0] ?? '', /^Command\b/);
  assert.match(report.toolDetail, /exit code/);
  assert.deepEqual(report.updateTexts, [
    'The structured event adapter is live. I am finishing the desktop verification now.',
    'The desktop fixture passes. I am checking the final response now.'
  ]);
  assert.equal(report.oldChips, 0);
  assert.equal(report.logBeforeAnswer, true);
  assert.match(report.attachControl, /Attach/);
  assert.equal(report.quickKeyCount, 0);
  assert.equal(report.timestampCount, 2);
  assert.match(report.messageAuthor, /PM Claude · via Sessions/);
  assert.notEqual(report.userCardRadius, '0px');
  assert.match(report.assistantBackground, /rgba\(0, 0, 0, 0\)/);
  assert.match(report.runState, /Working|Ready|\d/);
  assert.equal(report.horizontalOverflow, 0);
  assert.deepEqual(pageErrors, []);
  if (process.env.STRUCTURED_VIEW_SCREENSHOT) {
    await withTimeout(
      page.screenshot({ path: screenshot, captureBeyondViewport: false }),
      'screenshot'
    );
  }
  t.scenario('a live Claude turn shows every step as it happens under one live header');
  await page.setViewport({ width: 1440, height: 960, deviceScaleFactor: 1 });
  await page.goto(`http://127.0.0.1:${address.port}/?claude`, { waitUntil: 'domcontentloaded' });
  await t.waitForSelector(page, '.work-log-live', 'the live work header');
  const live = await page.evaluate(() => ({
    headers: Array.from(document.querySelectorAll('.work-log-live')).map((element) => element.textContent?.trim() ?? ''),
    summaries: document.querySelectorAll('.work-log-summary').length,
    kinds: Array.from(document.querySelectorAll('.work-log-row')).map((row) =>
      ['is-thinking', 'is-tool', 'is-update'].find((kind) => row.classList.contains(kind)) ?? ''),
    labels: Array.from(document.querySelectorAll('.work-log-row-label')).map((element) => element.textContent?.trim() ?? ''),
    failed: Array.from(document.querySelectorAll('.work-log-row.is-failed .work-log-row-label')).map((element) => element.textContent?.trim() ?? ''),
    running: Array.from(document.querySelectorAll('.work-log-row.is-running .work-log-row-label')).map((element) => element.textContent?.trim() ?? ''),
    prose: document.querySelector('.remote-bubble-assistant .remote-bubble-content')?.textContent ?? '',
    rowLanes: Array.from(document.querySelectorAll('.work-log-row .work-log-lane')).map((element) => element.textContent?.trim() ?? ''),
    reportLanes: document.querySelectorAll('.remote-bubble-user .work-log-lane').length
  }));
  assert.equal(live.headers.length, 1);
  assert.match(live.headers[0], /^Working for 4\ds$/);
  assert.equal(live.summaries, 0);
  assert.deepEqual(live.kinds, ['is-thinking', 'is-tool', 'is-tool', 'is-tool', 'is-tool', 'is-tool']);
  assert.match(live.labels[0], /escape handling/);
  assert.deepEqual(live.failed, [live.labels[1]]);
  assert.equal(live.failed[0], 'Ran parser tests');
  assert.equal(live.running.length, 1);
  assert.equal(live.running[0], 'Run all tests');
  assert.match(live.prose, /trailing backslash/);
  // The delegating command and the lane's report both open that lane.
  assert.deepEqual(live.rowLanes, ['Open lane']);
  assert.equal(live.reportLanes, 1);
  const opened = await page.evaluate(() => new Promise((resolve) => {
    window.addEventListener('sessions:open-lane', (event) => resolve(event.detail), { once: true });
    document.querySelector('.work-log-row .work-log-lane').click();
  }));
  assert.deepEqual(opened, { sessionId: '31000000-0000-4000-8000-0000000000a1' });
  assert.deepEqual(pageErrors, []);
  if (process.env.STRUCTURED_VIEW_SCREENSHOT) {
    await page.screenshot({ path: `${process.env.STRUCTURED_VIEW_SCREENSHOT}-claude.png`, captureBeyondViewport: false });
  }

  t.scenario('active Codex steering stays visible and fits desktop and mobile composers');
  for (const width of [1440, 390]) {
    await page.setViewport({ width, height: 960, deviceScaleFactor: 1 });
    await page.goto(`http://127.0.0.1:${address.port}/?steering`, { waitUntil: 'domcontentloaded' });
    await page.type('.input-textarea', 'Use the updated answer');
    await t.waitForSelector(page, '.input-send.is-steering', 'the active-turn steering control');
    const control = await page.$eval('.input-send.is-steering', (element) => ({
      label: element.textContent,
      fits: element.scrollWidth <= element.clientWidth,
      right: element.getBoundingClientRect().right,
      viewport: window.innerWidth
    }));
    assert.equal(control.label, 'Send follow-up');
    assert.equal(control.fits, true);
    assert.ok(control.right <= control.viewport);
    if (process.env.STEERING_VIEW_SCREENSHOT) {
      await page.screenshot({ path: `${process.env.STEERING_VIEW_SCREENSHOT}-${width}.png`, captureBeyondViewport: false });
    }
  }
  t.pass(`structured-view smoke passed${process.env.STRUCTURED_VIEW_SCREENSHOT ? `: ${screenshot}` : ''}`);
} finally {
  t.release();
  // Both of these are deadline-bounded so a wedged browser or an unreleased
  // socket fails instead of hanging — and the deadline timers are unref'd, so
  // a clean shutdown exits immediately instead of paying the deadline. The
  // straightforward `Promise.race([close(), delay(3_000)])` this replaces was
  // costing three seconds on every passing run of every suite that used it.
  await closeBrowser(browser, 3_000);
  await closeServer(server, 3_000);
  if (!process.env.STRUCTURED_VIEW_SCREENSHOT) await rm(work, { recursive: true, force: true });
}

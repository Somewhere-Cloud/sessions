import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-footer-usage-'));
let server, browser;
try {
  await build({ entryPoints: ['scripts/footer-usage-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg', '/openai-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"><div id="root"></div><script type="module" src="/app.js"></script>');
  server = createServer(async (req, res) => {
    const path = req.url.split('?')[0];
    const name = path === '/' ? 'index.html' : path.slice(1);
    const icon = ['claude-icon.svg', 'openai-icon.svg', 'somewhere-logo.svg'].includes(name);
    if (!icon && !['index.html', 'app.js', 'app.css'].includes(name)) { res.writeHead(404); res.end(); return; }
    res.writeHead(200, { 'Content-Type': icon ? 'image/svg+xml' : name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
    res.end(await readFile(icon ? join('public', name) : join(work, name)));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true });
  const page = await browser.newPage();
  // Older native WebViews support query serialization but not the size getter.
  await page.evaluateOnNewDocument(() => {
    Object.defineProperty(URLSearchParams.prototype, 'size', { configurable: true, get: () => undefined });
  });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const scope = (machineId = 'local', profile = 'work', provider = 'codex', extra = {}) => ({ sessionId: 'one', machineId, profile, provider, ...extra });
  const change = async (next) => { await page.evaluate((value) => window.changeScope(value), next); };
  const text = () => page.$eval('main', (el) => el.textContent);
  await page.goto(`http://127.0.0.1:${server.address().port}/`, { waitUntil: 'networkidle0' });
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('12%'));
  assert.deepEqual(await page.evaluate(() => {
    const request = window.usageDaemon.requests.find((item) => item.path === '/api/account-usage/exact');
    const query = new URL(request.url).searchParams;
    return [query.get('tool'), query.get('name')];
  }), ['codex', 'work'], 'older WebViews must retain executing-account filters');
  assert.equal(await page.evaluate(() => window.usageDaemon.requests.filter(item => item.path === '/api/account-usage/exact').length), 1, 'two consumers share one exact reading');
  if (process.env.ACCOUNT_SCREENSHOT_DIR) {
    for (const [theme, width] of [['dark', 1280], ['dark', 1100], ['dark', 375], ['light', 1280], ['light', 375]]) {
      await page.evaluate(value => { document.documentElement.dataset.theme = value; document.querySelector('main').dataset.theme = value; }, theme);
      await page.setViewport({ width, height: 900 });
      await page.waitForFunction(() => Boolean(document.querySelector('.mobile-nav')) === (innerWidth <= 720));
      await page.waitForFunction(() => document.querySelector('summary').textContent.includes('12%'));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, `footer-selected-${theme}-${width}.png`), fullPage: true });
    }
    await page.setViewport({ width: 1280, height: 900 });
    await page.waitForFunction(() => !document.querySelector('.mobile-nav'));
    await page.waitForFunction(() => document.querySelector('summary').textContent.includes('12%'));
  }
  await page.focus('summary');
  await page.keyboard.press('Enter');
  await page.focus('.footer-usage-popover > button');
  await page.keyboard.press('Escape');
  assert.equal(await page.$eval('details', el => el.open), false);
  assert.equal(await page.$eval('summary', el => el === document.activeElement), true);
  await page.keyboard.press('Enter');
  assert.deepEqual(await page.$$eval('[role="meter"]', nodes => nodes.map(n => n.getAttribute('aria-valuenow'))), ['12', '31']);
  assert.match(await text(), /local work/);
  await page.evaluate(() => window.delayRead());
  await page.click('.footer-usage-popover > button');
  await page.waitForFunction(() => window.pendingRead());
  await change(scope('remote'));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('77%'));
  await page.evaluate(() => window.releaseRead());
  await page.click('summary');
  assert.match(await text(), /remote work/);
  assert.doesNotMatch(await text(), /12%/);
  await change(scope('remote', 'missing'));
  await page.waitForFunction(() => document.querySelector('summary .footer-usage-caption small').textContent.includes('unknown'));
  await page.click('summary');
  assert.match(await text(), /could not be matched/);
  assert.equal(await page.$$eval('[role="meter"]', nodes => nodes.length), 0);
  await change(scope('local', 'work', 'codex', { configDir: '/different/home' }));
  await page.waitForFunction(() => document.querySelector('summary .footer-usage-caption small').textContent.includes('unknown'));
  await page.click('summary');
  assert.match(await text(), /could not be matched/);
  await change(scope('local', 'work', 'claude-code'));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('unavailable'));
  await page.click('summary');
  assert.equal(await page.$$eval('[role="meter"]', nodes => nodes.length), 0);
  if (process.env.ACCOUNT_SCREENSHOT_DIR) {
    for (const [theme, width] of [['dark', 1280], ['dark', 1100], ['dark', 375], ['light', 1280], ['light', 375]]) {
      await page.evaluate(value => { document.documentElement.dataset.theme = value; document.querySelector('main').dataset.theme = value; }, theme);
      await page.setViewport({ width, height: 900 });
      await page.waitForFunction(() => Boolean(document.querySelector('.mobile-nav')) === (innerWidth <= 720));
      await page.waitForFunction(() => !document.querySelector('summary').textContent.includes('Reading limits'));
      await page.$eval('details.footer-usage', el => { el.open = true; });
      await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, `footer-unsupported-${theme}-${width}.png`), fullPage: true });
    }
  }
  // A late read cannot cross an account switch on the same host either.
  await page.evaluate(() => window.delayRead());
  await change(scope('local', 'work', 'codex', { sessionId: 'account-race' }));
  await page.waitForFunction(() => window.pendingRead());
  await change(scope('local', ''));
  await page.waitForFunction(() => document.querySelector('.footer-usage-identity')?.textContent.includes('CLI default'));
  await page.evaluate(() => window.releaseRead());
  assert.doesNotMatch(await text(), /12%/);
  // Failed re-read clears allowance without evidence of the current login.
  await change(scope('remote', 'work', 'codex', { sessionId: 'offline' }));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('77%'));
  await page.click('summary');
  await page.evaluate(() => { window.machines[1].reachable = false; });
  await page.click('.footer-usage-popover > button');
  await page.waitForFunction(() => document.querySelector('summary .footer-usage-caption small').textContent.includes('unknown'));
  assert.equal(await page.$$eval('[role="meter"]', nodes => nodes.length), 0);
  // Old snapshots remain explicitly last-known; multiple buckets are never summed.
  await page.evaluate(() => {
    const account = window.machines[0].accountUsage['codex/work'];
    account.read_at = Date.now() - 3600000;
    account.buckets.push({ limit_id: 'review', limit_name: 'Review', windows: [{ kind: 'primary', used_percent: 9 }] });
  });
  await change(scope('local', 'work', 'codex', { sessionId: 'stale' }));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('Last known'));
  await page.click('summary');
  assert.match(await text(), /2 independent limits/);
  assert.match(await text(), /1 h ago/);
  assert.deepEqual(await page.$$eval('[role="meter"]', nodes => nodes.map(n => n.getAttribute('aria-valuenow'))), ['12', '31', '9']);
  // Keyboard toggling uses native details semantics and keeps focus on summary.
  await page.focus('summary');
  await page.keyboard.press('Enter');
  assert.equal(await page.$eval('details', el => el.open), false);
  await page.keyboard.press('Enter');
  assert.equal(await page.$eval('details', el => el.open), true);
  // CLI default is an explicit empty-name placement, never a preferred home.
  await change(scope('local', ''));
  await page.waitForFunction(() => document.querySelector('.footer-usage-identity')?.textContent.includes('CLI default'));
  assert.doesNotMatch(await text(), /12%/);
  // A removed or unbound host cannot fall back to the globally active local host.
  await change(scope('removed'));
  await page.waitForFunction(() => document.querySelector('summary .footer-usage-caption small').textContent.includes('unknown'));
  await page.click('summary');
  assert.match(await text(), /no longer configured/);
  const requestsBefore = await page.evaluate(() => window.usageDaemon.requests.length);
  await change(scope(undefined, 'work', 'codex', { machineId: undefined }));
  await page.waitForFunction(() => document.querySelector('summary .footer-usage-caption small').textContent.includes('unknown'));
  assert.equal(await page.evaluate(() => window.usageDaemon.requests.length), requestsBefore);
  await change(scope('local', 'work', 'codex', { sessionId: 'visual' }));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('Last known'));
  await page.click('summary');
  const tokenEvent = (last, total = 900000) => [{ type: 'codex', subtype: 'token_count', source: 'codex-app-server', conversationId: 'native-one', usage: { last, total: { totalTokens: total }, modelContextWindow: 100000 } }];
  const context = async (events) => {
    await page.evaluate(value => window.changeEvents(value), events);
    await page.waitForFunction(() => document.querySelector('#context').textContent.length > 0);
    return page.$eval('#context', el => el.textContent);
  };
  await page.evaluate(() => window.changeNative('native-one'));
  assert.match(await context(tokenEvent(undefined)), /unknown/);
  assert.doesNotMatch(await context(tokenEvent(undefined)), /900/);
  assert.match(await context(tokenEvent({ totalTokens: 0 })), /≈0 tokens/);
  assert.match(await context(tokenEvent({ totalTokens: 12000 })), /12%/);
  const noWindow = tokenEvent({ totalTokens: 400 });
  delete noWindow[0].usage.modelContextWindow;
  assert.match(await context(noWindow), /≈400 tokens · window unknown/);
  assert.match(await context(tokenEvent({ totalTokens: -1 })), /unknown/);
  await page.evaluate(() => window.changeNative('native-two'));
  await page.waitForFunction(() => document.querySelector('#context').textContent.includes('unknown'));
  const observed = tokenEvent({ totalTokens: 12000 });
  observed[0].conversationId = 'native-two';
  observed[0].timestamp = new Date().toISOString();
  assert.match(await context(observed), /observed/);
  for (const [theme, width] of [['dark', 1280], ['dark', 1100], ['dark', 375], ['light', 1280], ['light', 375]]) {
    await page.evaluate(value => { document.documentElement.dataset.theme = value; document.querySelector('main').dataset.theme = value; }, theme);
    await page.setViewport({ width, height: 900 });
      await page.waitForFunction(() => Boolean(document.querySelector('.mobile-nav')) === (innerWidth <= 720));
      await page.waitForFunction(() => !document.querySelector('summary').textContent.includes('Reading limits'));
      await page.$eval('details.footer-usage', el => { el.open = true; });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    if (process.env.ACCOUNT_SCREENSHOT_DIR) {
      await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, `footer-usage-${theme}-${width}.png`), fullPage: true });
      await page.$eval('details.footer-usage', el => { el.open = false; });
      await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, `footer-summary-${theme}-${width}.png`), fullPage: true });
    }
  }
  await change(scope('local', 'work', 'codex', { sessionId: 'none' }));
  await page.waitForFunction(() => document.querySelector('summary').textContent.includes('No selected account'));
  await page.click('summary');
  assert.equal(await page.$$eval('[role="meter"]', nodes => nodes.length), 0);
  await page.click('.footer-usage-links button');
  assert.equal(await page.title(), 'Usage');
  await page.click('summary');
  await page.click('.footer-usage-links button:last-child');
  assert.equal(await page.title(), 'Daily');
  await page.setViewport({ width: 375, height: 900 });
  await page.waitForFunction(() => Boolean(document.querySelector('.mobile-nav')));
  await page.waitForSelector('[aria-label="Open More"]');
  await page.click('[aria-label="Open More"]');
  assert.deepEqual(await page.$$eval('.mobile-more-grid strong', nodes => nodes.map(el => el.textContent)),
    ['Accounts & computers', 'Settings', 'Connections', 'Usage']);
  await page.keyboard.press('Escape');
  assert.equal(await page.$('.mobile-more-sheet'), null);
  await page.evaluate(() => { document.querySelector('main').classList.replace('text-size-s', 'text-size-l'); });
  await page.click('summary');
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  assert.equal(await page.$eval('.footer-usage-popover', el => { const box = el.getBoundingClientRect(); return box.left >= 0 && box.right <= innerWidth && box.top >= 0 && box.bottom <= innerHeight; }), true, 'scaled disclosure stays inside viewport');
  if (process.env.ACCOUNT_SCREENSHOT_DIR) await page.screenshot({ path: join(process.env.ACCOUNT_SCREENSHOT_DIR, 'footer-scaled-mobile.png'), fullPage: true });
  assert.deepEqual(errors, []);
  console.log('footer usage (including stale/multiple buckets/default/missing host/account race/offline refresh): host/profile/home matching, unsupported Claude, independent windows, late response fencing, zero/unknown/cumulative/native footprint, narrow layout passed');
} finally {
  await closeBrowser(browser);
  await closeServer(server);
  await rm(work, { recursive: true, force: true });
}

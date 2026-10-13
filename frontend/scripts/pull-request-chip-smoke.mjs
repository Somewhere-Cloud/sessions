// The pull request chip shows only the answer for the session it is showing,
// and a "fresh" answer turns stale while the chip stays open, without asking
// the daemon or GitHub again.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';

const work = await mkdtemp(join(tmpdir(), 'sessions-pr-chip-'));
const FRESH_FOR_MS = 1500;
const link = (number) => ({ url: `https://github.com/acme/app/pull/${number}`, host: 'github.com', owner: 'acme', repo: 'app', number, linked_at: '2026-10-12T00:00:00Z' });
const report = (state) => ({ state, draft: false, checks: 'passing', checks_total: 1, checks_failing: 0, checks_pending: 0, mergeable: 'mergeable' });
const answers = {
  'lane-a': () => ({ session_id: 'lane-a', linked: true, runner: 'live', pull_request: link(42), status: report('open'),
    observed_at: new Date().toISOString(), freshness: 'fresh', fresh_for_ms: FRESH_FOR_MS, work: 'open', closure: { action: 'none', reason: '' } }),
  'lane-b': () => ({ session_id: 'lane-b', linked: true, runner: 'live', pull_request: link(77), status: report('open'),
    observed_at: '2026-10-12T00:00:00Z', freshness: 'stale', work: 'open', closure: { action: 'none', reason: '' } })
};
const mergedA = { ...answers['lane-a'](), status: report('merged'), freshness: 'final', fresh_for_ms: undefined, work: 'merged', refresh: { ok: true, recorded: true } };
let browser; let server;
const requests = []; const failed = []; const errors = []; const heldRefreshes = [];
try {
  await build({ entryPoints: [fileURLToPath(new URL('./pull-request-chip-fixture.tsx', import.meta.url))], outdir: work,
    bundle: true, platform: 'browser', format: 'esm', entryNames: 'app',
    define: { 'import.meta.env.BASE_URL': '"/"' }, external: ['/claude-icon.svg'], logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (request, response) => {
    const match = /^\/api\/sessions\/(lane-[ab])\/pr(\/refresh)?$/.exec(request.url ?? '');
    if (match) {
      requests.push(`${request.method} ${request.url}`);
      const send = (body) => { response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify(body)); };
      if (match[2]) heldRefreshes.push(() => send(mergedA)); else send(answers[match[1]]());
      return;
    }
    const name = request.url === '/' ? 'index.html' : request.url.slice(1);
    if (name === 'favicon.ico') { response.writeHead(204); response.end(); return; }
    if (!['index.html', 'app.js', 'app.css'].includes(name)) { response.writeHead(404); response.end(); return; }
    try { response.writeHead(200, { 'content-type': name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' }); response.end(await readFile(join(work, name))); }
    catch { response.writeHead(404); response.end(); }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage(); page.setDefaultTimeout(10000);
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('requestfailed', (request) => { if (!request.url().endsWith('/pr')) failed.push(request.url()); });
  page.on('response', (response) => { if (response.status() >= 400) failed.push(`${response.status()} ${response.url()}`); });
  const chipText = () => page.$eval('[data-chip]', (element) => element.textContent ?? '');
  const freshness = () => page.$eval('.session-pr-chip', (element) => element.getAttribute('data-freshness'));
  const waitForChip = (text) => page.waitForFunction((value) => document.querySelector('[data-chip]')?.textContent?.includes(value), {}, text);
  const show = async (id) => { await page.click(`[data-show="${id}"]`); await page.waitForFunction((value) => document.querySelector('[data-session]')?.textContent === value, {}, id); };
  const settle = () => new Promise((resolve) => setTimeout(resolve, 300));

  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await waitForChip('PR #42');
  assert.equal(await freshness(), 'fresh');
  assert.doesNotMatch(await chipText(), /stale/);

  // The daemon said fresh for FRESH_FOR_MS. Past that, the open chip says
  // stale on its own, and asks nobody.
  const asked = requests.length;
  await page.waitForFunction(() => document.querySelector('.session-pr-chip')?.getAttribute('data-freshness') === 'stale', { timeout: FRESH_FOR_MS + 3000 });
  assert.match(await chipText(), /stale/);
  assert.equal(requests.length, asked, 'aging a fresh answer must not ask the daemon or GitHub again');

  // A refresh for A that answers after the view moved to B is not B's status.
  await page.click('.session-pr-refresh');
  await page.waitForFunction(() => document.querySelector('.session-pr-refresh')?.textContent === 'Reading…');
  await show('lane-b'); await waitForChip('PR #77');
  heldRefreshes.shift()(); await settle();
  assert.match(await chipText(), /PR #77/);
  assert.doesNotMatch(await chipText(), /PR #42|merged/);
  assert.equal(await page.$eval('.session-pr-refresh', (element) => element.disabled), false, 'B must not inherit A\'s in-flight refresh');

  // Nor does it land on a later view of A: A, refresh, B, A again, answer.
  await show('lane-a'); await waitForChip('PR #42');
  await page.click('.session-pr-refresh');
  await page.waitForFunction(() => document.querySelector('.session-pr-refresh')?.textContent === 'Reading…');
  await show('lane-b'); await waitForChip('PR #77');
  await show('lane-a'); await waitForChip('PR #42');
  heldRefreshes.shift()(); await settle();
  assert.doesNotMatch(await chipText(), /merged/, 'an answer asked for by an earlier view of A must not replace the current one');
  assert.equal(await freshness(), 'fresh');

  // A refresh that is still the current view's applies normally.
  await page.click('.session-pr-refresh');
  await page.waitForFunction(() => document.querySelector('.session-pr-refresh')?.textContent === 'Reading…');
  heldRefreshes.shift()();
  await waitForChip('merged');
  assert.equal(await freshness(), 'final');

  assert.deepEqual(errors, []); assert.deepEqual(failed, []);
  console.log('pull request chip passed: local freshness aging without requests, no cross-session refresh, no stale-epoch refresh, current refresh applies');
} finally {
  for (const release of heldRefreshes) release();
  await closeBrowser(browser); await closeServer(server); await rm(work, { recursive: true, force: true });
}

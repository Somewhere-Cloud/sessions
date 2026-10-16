// The pull request chip shows only the answer for the session it is showing,
// on the server that runs that session, and a "fresh" answer turns stale while
// the chip stays open, without asking the daemon or GitHub again.
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
    observed_at: '2026-10-12T00:00:00Z', freshness: 'stale', work: 'open', closure: { action: 'none', reason: '' }, other_pull_requests: 2 })
};
// gh is signed out on the daemon's machine: a missing optional integration,
// not a failed pull request.
const unavailableB = { ...answers['lane-b'](), refresh: { ok: false, error_kind: 'auth', error: 'gh is not signed in', recorded: false, unavailable: true } };
const mergedA = { ...answers['lane-a'](), status: report('merged'), freshness: 'final', fresh_for_ms: undefined, work: 'merged', refresh: { ok: true, recorded: true } };
// The same lane id on another computer is a different lane with its own link.
const miniA = () => ({ session_id: 'lane-a', linked: true, runner: 'live', pull_request: link(91), status: report('open'),
  observed_at: '2026-10-12T00:00:00Z', freshness: 'stale', work: 'open', closure: { action: 'none', reason: '' } });
const closedMiniA = { ...miniA(), status: report('closed'), freshness: 'final', work: 'closed_unmerged', refresh: { ok: true, recorded: true } };
let browser; let server;
const requests = []; const failed = []; const errors = []; const heldRefreshes = [];
try {
  await build({ entryPoints: [fileURLToPath(new URL('./pull-request-chip-fixture.tsx', import.meta.url))], outdir: work,
    bundle: true, platform: 'browser', format: 'esm', entryNames: 'app',
    define: { 'import.meta.env.BASE_URL': '"/"' }, external: ['/claude-icon.svg'], logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (request, response) => {
    const match = /^(\/api\/fleet\/mini)?\/api\/sessions\/(lane-[ab])\/pr(\/refresh)?$/.exec(request.url ?? '');
    if (match) {
      requests.push(`${request.method} ${request.url}`);
      const send = (body) => { response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify(body)); };
      const mini = Boolean(match[1]);
      const refreshed = mini ? closedMiniA : match[2] === 'lane-b' ? unavailableB : mergedA;
      if (match[3]) heldRefreshes.push(() => send(refreshed)); else send(mini ? miniA() : answers[match[2]]());
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
  const shown = (value) => page.waitForFunction((expected) => document.querySelector('[data-session]')?.textContent === expected, {}, value);
  const show = async (id) => { await page.click(`[data-show="${id}"]`); await page.waitForFunction((value) => document.querySelector('[data-session]')?.textContent?.endsWith(`:${value}`), {}, id); };
  const source = async (id) => { await page.click(`[data-source="${id}"]`); await page.waitForFunction((value) => document.querySelector('[data-session]')?.textContent?.startsWith(`${value}:`), {}, id); };
  const startRefresh = async () => {
    await page.click('.session-pr-refresh');
    await page.waitForFunction(() => document.querySelector('.session-pr-refresh')?.textContent === 'Reading…');
  };
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

  // Selecting another computer does not move this session: its refresh still
  // goes to the daemon that runs it.
  await page.click('[data-activate="mini"]'); await shown('fixture:lane-a');
  let sent = requests.length;
  await startRefresh();
  assert.deepEqual(requests.slice(sent), ['POST /api/sessions/lane-a/pr/refresh'], 'refresh must target the session\'s own server, not the active one');
  heldRefreshes.shift()(); await waitForChip('merged');

  // The row now names a session with the same id on Mini while a refresh to
  // this computer is still in flight. That answer is not Mini's status.
  await page.click('[data-activate="fixture"]');
  await startRefresh();
  sent = requests.length;
  await source('mini'); await waitForChip('PR #91');
  assert.deepEqual(requests.slice(sent), ['GET /api/fleet/mini/api/sessions/lane-a/pr'], 'the chip reads the new source server');
  heldRefreshes.shift()(); await settle();
  assert.match(await chipText(), /PR #91/);
  assert.doesNotMatch(await chipText(), /PR #42|merged/, 'an answer from the previous server must not land on this one');
  assert.equal(await page.$eval('.session-pr-refresh', (element) => element.disabled), false, 'Mini must not inherit the other server\'s in-flight refresh');

  // Mini's own refresh goes to Mini and applies there.
  sent = requests.length;
  await startRefresh();
  assert.deepEqual(requests.slice(sent), ['POST /api/fleet/mini/api/sessions/lane-a/pr/refresh']);
  heldRefreshes.shift()(); await waitForChip('closed');
  assert.match(await chipText(), /PR #91/);
  // A lane with more links says so, and a signed-out gh reads as
  // unavailable, not as a failed refresh.
  await source('fixture'); await show('lane-b'); await waitForChip('PR #77');
  assert.match(await chipText(), /\+2/);
  await startRefresh();
  heldRefreshes.shift()();
  await page.waitForFunction(() => document.querySelector('.session-pr-refresh')?.textContent === 'GitHub unavailable');
  assert.doesNotMatch(await chipText(), /Refresh failed/);
  if (process.env.PR_CHIP_SCREENSHOT) await page.screenshot({ path: process.env.PR_CHIP_SCREENSHOT });

  assert.deepEqual(errors, []); assert.deepEqual(failed, []);
  console.log('pull request chip passed: local freshness aging without requests, no cross-session refresh, no stale-epoch refresh, current refresh applies, source-server routing, no cross-server refresh, other-link count, unavailable gh is not a failure');
} finally {
  for (const release of heldRefreshes) release();
  await closeBrowser(browser); await closeServer(server); await rm(work, { recursive: true, force: true });
}

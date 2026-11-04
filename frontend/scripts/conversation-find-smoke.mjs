// Find in conversation: Mod+F opens a find bar over the open conversation, the
// count is live, Enter and Shift+Enter walk the matches with highlights and
// scrolling, expanding a folded tool output makes its text findable, Escape
// closes and hands focus back, and the command palette opens it too.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer, smoke } from './lib/smoke.mjs';

const t = smoke('conversation-find');
const work = await mkdtemp(join(tmpdir(), 'sessions-find-'));
let server;
let browser;
try {
  await build({ entryPoints: ['scripts/conversation-find-fixture.tsx'], bundle: true, outdir: work,
    entryNames: 'app', format: 'esm', platform: 'browser', jsx: 'automatic', external: ['/claude-icon.svg'],
    define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root" style="height:100dvh"></div><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (request, response) => {
    const pathname = new URL(request.url, 'http://fixture.invalid').pathname;
    const name = pathname === '/' ? 'index.html' : pathname.slice(1);
    if (!['index.html', 'app.js', 'app.css'].includes(name)) { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { 'Content-Type': name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
    response.end(await readFile(join(work, name)));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await t.bounded(puppeteer.launch({ headless: true, args: ['--no-sandbox'] }), 'Chromium to launch', 60_000);
  const page = await browser.newPage();
  t.watch(page);
  page.setDefaultTimeout(10_000);
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.setViewport({ width: 1200, height: 800 });
  await page.goto(`http://127.0.0.1:${server.address().port}/`, { waitUntil: 'domcontentloaded' });
  await t.waitForSelector(page, '.remote-scroll .remote-msg', 'the conversation to render');
  const mod = await page.evaluate(() => /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)) ? 'Meta' : 'Control';
  const count = () => page.$eval('.conversation-find-count', (node) => node.textContent);
  const waitForCount = (text, what) => t.waitForFunction(page, (value) => document.querySelector('.conversation-find-count')?.textContent === value, `the count to read ${what ?? text}`, {}, text);
  // The current match: its text, and whether it sits inside the scroller's view.
  const currentMatch = () => page.evaluate(() => {
    const current = CSS.highlights.get('sessions-find-current');
    const range = current && [...current][0];
    if (!range) return null;
    const rect = range.getBoundingClientRect();
    const view = document.querySelector('.remote-scroll').getBoundingClientRect();
    return { text: range.toString(), inView: rect.top >= view.top && rect.bottom <= view.bottom && rect.height > 0 };
  });

  t.scenario('Mod+F from the composer opens the bar and the count is live');
  await page.focus('.remote-input-wrap textarea');
  await page.keyboard.down(mod); await page.keyboard.press('f'); await page.keyboard.up(mod);
  await t.waitForFunction(page, () => document.activeElement?.matches('.conversation-find input'), 'the find input to take focus');
  await page.keyboard.type('kumquat');
  await waitForCount('1 of 3');
  // The count is live while typing; wait for the whole word's highlight.
  await t.waitForFunction(page, () => [...(CSS.highlights.get('sessions-find-current') ?? [])][0]?.toString().length === 7, 'the whole query to be highlighted');
  assert.equal(await page.evaluate(() => CSS.highlights.get('sessions-find')?.size), 3, 'every visible match is highlighted');
  let match = await currentMatch();
  assert.equal(match?.text.toLowerCase(), 'kumquat');
  assert.ok(match.inView, 'the first match scrolled into view');

  t.scenario('Enter and Shift+Enter walk the matches, wrapping, and keep each in view');
  await page.keyboard.press('Enter');
  await waitForCount('2 of 3');
  assert.equal((await currentMatch())?.text, 'Kumquat');
  await page.keyboard.press('Enter');
  await waitForCount('3 of 3');
  match = await currentMatch();
  assert.equal(match?.text, 'KUMQUAT');
  assert.ok(match.inView, 'the last match scrolled into view');
  await page.keyboard.press('Enter');
  await waitForCount('1 of 3');
  assert.ok((await currentMatch())?.inView, 'wrapping to the first match scrolls back up');
  await page.keyboard.down('Shift'); await page.keyboard.press('Enter'); await page.keyboard.up('Shift');
  await waitForCount('3 of 3');

  t.scenario('a folded tool output is searched once it is expanded');
  await page.evaluate(() => {
    const summary = document.querySelector('.work-log-summary[aria-expanded="false"]');
    summary?.click();
  });
  await t.waitForSelector(page, '.work-log-row-head', 'the tool steps to show');
  await page.evaluate(() => document.querySelector('.work-log-row-head[aria-expanded="false"]')?.click());
  await t.waitForSelector(page, '.work-log-detail-output', 'the tool output to show');
  await t.waitForFunction(page, () => /of 4$/.test(document.querySelector('.conversation-find-count')?.textContent ?? ''), 'the count to include the expanded output');

  t.scenario('a query with no match says so calmly');
  await page.focus('.conversation-find input');
  await page.keyboard.type('zzz');
  await waitForCount('No matches');
  assert.equal(await page.evaluate(() => CSS.highlights.get('sessions-find')?.size ?? 0), 0);

  t.scenario('Escape closes the bar, clears highlights, and returns focus to the composer');
  await page.keyboard.press('Escape');
  await t.waitForFunction(page, () => !document.querySelector('.conversation-find'), 'the bar to close');
  assert.equal(await page.evaluate(() => CSS.highlights.has('sessions-find')), false);
  assert.equal(await page.evaluate(() => document.activeElement?.matches('.remote-input-wrap textarea')), true, 'focus returns to the composer');

  t.scenario('the command palette offers Find in conversation');
  await page.click('#open-palette');
  await t.waitForFunction(page, () => document.activeElement?.id === 'sessions-command-query', 'the palette query to take focus');
  await page.keyboard.type('Find in conv');
  await t.waitForFunction(page, () => document.querySelector('[role="dialog"] [role="option"][aria-selected="true"] strong')?.textContent === 'Find in conversation', 'the palette to offer Find in conversation first');
  await page.keyboard.press('Enter');
  await t.waitForFunction(page, () => document.activeElement?.matches('.conversation-find input'), 'the palette to open the find bar');
  await page.keyboard.type('sample');
  await waitForCount('1 of 1');

  assert.deepEqual(errors, []);
  t.pass('conversation find smoke passed');
} finally {
  t.release();
  await closeBrowser(browser, 3_000);
  await closeServer(server, 3_000);
  await rm(work, { recursive: true, force: true });
}

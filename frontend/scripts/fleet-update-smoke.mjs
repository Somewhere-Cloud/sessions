import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';

// Updating Sessions on other computers from this one: the sidebar card names
// the computer and the versions, follows the remote job through its daemon
// restart, and Settings lists every computer's version. In-memory daemons
// only: no HTTP server, live daemon or network request.
const result = await build({ entryPoints: ['scripts/fleet-update-fixture.tsx'], bundle: true, write: false, preserveSymlinks: true, external: ['/claude-icon.svg', '/openai-icon.svg'],
  outdir: 'fleet-update-fixture-output', format: 'iife', platform: 'browser', jsx: 'automatic',
  define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
const js = result.outputFiles.find((file) => file.path.endsWith('.js')).text;
const css = result.outputFiles.find((file) => file.path.endsWith('.css')).text;
const browser = await puppeteer.launch({ headless: true });
const text = (page) => page.$eval('body', (body) => body.textContent);
try {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', (request) => { errors.push(`unexpected network: ${request.url()}`); void request.abort(); });
  for (const [theme, width] of [['dark', 1100], ['light', 375]]) {
    await page.setViewport({ width, height: 850 });

    // One computer behind: the card names it, both versions, and one Update.
    await page.setContent(`<html data-theme="${theme}"><style>${css}</style><script>window.__scenario='one'</script><div id="root"></div></html>`);
    await page.addScriptTag({ content: js });
    await page.waitForSelector('.product-update-card.is-fleet');
    assert.match(await page.$eval('.product-update-card.is-fleet', (el) => el.textContent), /Mac mini · Sessions 0\.2\.40 → 0\.2\.46/);
    assert.match(await text(page), /Sessions 0\.2\.46 · updates with this app/);
    assert.match(await text(page), /Studio.*Sessions 0\.2\.46 · up to date/);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal overflow');
    await page.click('.product-update-card.is-fleet button[aria-label="Update Sessions on Mac mini"]');
    await page.waitForFunction(() => document.body.textContent.includes('Mac mini: Updated Sessions'));
    assert.equal(await page.$('.product-update-card.is-fleet'), null, 'the card steps back once the computer is current');
    assert.deepEqual(await page.evaluate(() => [window.__hosts['mini.invalid'].started, window.__hosts['studio.invalid'].started, window.__hosts['local.invalid'].started]), [1, 0, 0]);
    if (process.env.FLEET_UPDATE_SCREENSHOT_DIR) {
      await mkdir(process.env.FLEET_UPDATE_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({ path: join(process.env.FLEET_UPDATE_SCREENSHOT_DIR, `one-${theme}-${width}.png`), fullPage: true });
    }

    // Several behind: one Update all, each computer followed to its outcome.
    await page.setContent(`<html data-theme="${theme}"><style>${css}</style><script>window.__scenario='many'</script><div id="root"></div></html>`);
    await page.addScriptTag({ content: js });
    await page.waitForSelector('.product-update-card.is-fleet button[aria-label="Update Sessions on every computer"]');
    assert.match(await page.$eval('.product-update-card.is-fleet', (el) => el.textContent), /Sessions on 2 computers.*Update all/);
    await page.click('.product-update-card.is-fleet button');
    await page.waitForFunction(() => !document.querySelector('.product-update-card.is-fleet')
      && window.__hosts['mini.invalid'].version === '0.2.46' && window.__hosts['studio.invalid'].version === '0.2.46');
    assert.deepEqual(await page.evaluate(() => [window.__hosts['mini.invalid'].started, window.__hosts['studio.invalid'].started, window.__hosts['local.invalid'].started]), [1, 1, 0]);
    await page.waitForFunction(() => (document.body.textContent.match(/Last update: Updated Sessions/g) ?? []).length === 2);
  }
  assert.deepEqual(errors, []);
  console.log('Fleet Sessions update: card, progress through restart, Update all, Settings versions PASS');
} finally { await browser.close(); }

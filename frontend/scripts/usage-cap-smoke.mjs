import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';

// In-memory fixture only: no HTTP server, live daemon or provider request.
const result = await build({ entryPoints: ['scripts/usage-cap-fixture.tsx'], bundle: true, write: false, preserveSymlinks: true, external: ['/claude-icon.svg', '/openai-icon.svg'],
  outdir: 'usage-cap-fixture-output', format: 'iife', platform: 'browser', jsx: 'automatic',
  define: { 'import.meta.env.BASE_URL': '"/"' }, loader: { '.svg': 'dataurl', '.png': 'dataurl' }, logLevel: 'silent' });
const js = result.outputFiles.find((file) => file.path.endsWith('.js')).text;
const css = result.outputFiles.find((file) => file.path.endsWith('.css')).text;
const browser = await puppeteer.launch({ headless: true });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.setRequestInterception(true);
  page.on('request', (request) => { errors.push(`unexpected network: ${request.url()}`); void request.abort(); });
  for (const [theme, width] of [['dark', 1100], ['light', 1100], ['dark', 375], ['light', 375]]) {
    await page.setViewport({ width, height: 900 });
    await page.setContent(`<html data-theme="${theme}"><style>${css}</style><div id="root"></div></html>`);
    await page.addScriptTag({ content: js });
    await page.waitForSelector('.usage-cap-card input[type="checkbox"]:not(:disabled)');
    const card = await page.$eval('.usage-cap-card', (node) => node.textContent);
    assert.match(card, /Usage cap/);
    assert.match(card, /Over the cap: claude default at 92% of weekly, resets/);
    assert.match(card, /1 session waits for usage/);
    const toggles = await page.$$eval('.usage-cap-card input[type="checkbox"]', (inputs) => inputs.map((input) => input.checked));
    assert.deepEqual(toggles, [true, true], 'cap and switching are on by default');
    assert.equal(await page.$eval('.usage-cap-card input[type="number"]', (input) => input.value), '90');
    const chip = await page.$eval('#chip', (node) => node.textContent);
    assert.match(chip, /^Waiting for usage · resets /);
    assert.match(chip, /switch to claude\/work\?/);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal overflow');

    await page.focus('.usage-cap-card input[type="number"]');
    await page.keyboard.press('End');
    await page.keyboard.press('Backspace');
    await page.keyboard.press('Backspace');
    await page.keyboard.type('85');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => window.usageCapRequests.some((request) => request.method === 'PUT' && request.body?.percent === 85));
    const toggles2 = await page.$$('.usage-cap-card input[type="checkbox"]');
    await toggles2[1].click();
    await page.waitForFunction(() => window.usageCapRequests.some((request) => request.method === 'PUT' && request.body?.switch === false));
    await page.waitForFunction(() => !document.querySelectorAll('.usage-cap-card input[type="checkbox"]')[1].checked);
    if (process.env.USAGE_CAP_SCREENSHOT_DIR) {
      await mkdir(process.env.USAGE_CAP_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({ path: join(process.env.USAGE_CAP_SCREENSHOT_DIR, `${theme}-${width}.png`), fullPage: true });
    }
  }
  assert.deepEqual(errors, []);
  console.log('Usage cap fixture: card, waiting chip, cap and switch changes, desktop/mobile, light/dark, no network PASS');
} finally { await browser.close(); }

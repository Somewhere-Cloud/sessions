import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';

// In-memory fixture only: no HTTP server, live daemon or provider request.
const result = await build({ entryPoints: ['scripts/auto-archive-fixture.tsx'], bundle: true, write: false, preserveSymlinks: true, external: ['/claude-icon.svg', '/openai-icon.svg'],
  outdir: 'auto-archive-fixture-output', format: 'iife', platform: 'browser', jsx: 'automatic',
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
    await page.setViewport({ width, height: 850 });
    await page.setContent(`<html data-theme="${theme}"><style>${css}</style><div id="root"></div></html>`);
    await page.addScriptTag({ content: js });
    await page.waitForSelector('input:not(:disabled)');
    assert.equal(await page.$eval('input', (input) => input.checked), false);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal overflow');
    await page.click('input');
    await page.waitForFunction(() => document.querySelector('input').checked && !document.querySelector('input').disabled);
    if (process.env.AUTO_ARCHIVE_SCREENSHOT_DIR) {
      await mkdir(process.env.AUTO_ARCHIVE_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({ path: join(process.env.AUTO_ARCHIVE_SCREENSHOT_DIR, `${theme}-${width}.png`), fullPage: true });
    }
  }
  assert.deepEqual(errors, []);
  console.log('Auto archive fixture: desktop/mobile, light/dark, opt-in and no network PASS');
} finally { await browser.close(); }

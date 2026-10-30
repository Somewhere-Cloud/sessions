import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, extname } from 'node:path';
import { build } from 'esbuild';
import puppeteer from 'puppeteer';
import { closeBrowser, closeServer } from './lib/smoke.mjs';
const work = await mkdtemp(join(tmpdir(), 'sidebar-identity-'));
const output = process.env.UI_SCREENSHOT_DIR;
let server, browser;
try {
  await build({ entryPoints: ['scripts/project-groups-fixture.tsx'], outdir: work, bundle: true, platform: 'browser', format: 'esm', entryNames: 'app',
    define: { 'import.meta.env.BASE_URL': '"/"' }, external: ['/claude-icon.svg'], loader: { '.svg': 'dataurl', '.png': 'dataurl', '.woff2': 'dataurl' }, logLevel: 'silent' });
  await writeFile(join(work, 'index.html'), '<!doctype html><html><head><meta name="viewport" content="width=device-width"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script>localStorage.setItem("sessions:projects-machine-scope", "fixture")</script><script type="module" src="/app.js"></script></body></html>');
  server = createServer(async (req, res) => {
    const name = new URL(req.url, 'http://fixture.invalid').pathname.slice(1) || 'index.html';
    if (!['index.html', 'app.js', 'app.css', 'claude-icon.svg', 'openai-icon.svg'].includes(name)) { res.writeHead(404); res.end(); return; }
    try {
      const body = await readFile(join(name.endsWith('.svg') ? 'public' : work, name));
      res.writeHead(200, { 'content-type': { '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' }[extname(name)] || 'text/html' }); res.end(body);
    } catch { res.writeHead(404); res.end(); }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  browser = await puppeteer.launch({ headless: true, args: ['--no-sandbox'] });
  const page = await browser.newPage();
  const errors = []; page.on('pageerror', (error) => errors.push(error.message));
  if (output) await mkdir(output, { recursive: true });
  for (const width of [1141, 1440, 390]) for (const theme of ['dark', 'light']) {
    await page.setViewport({ width, height: 960 });
    await page.goto(`http://127.0.0.1:${server.address().port}/?identity&theme=${theme}`, { waitUntil: 'networkidle0' });
    await page.waitForSelector('[data-session-id="project-0-agent-18"] .session-workspace-branch');
    await page.waitForFunction(() => [...document.querySelectorAll('.project-mark img')].some((image) => image.complete && image.naturalWidth > 0));
    const result = await page.evaluate(() => {
      const row = document.querySelector('[data-session-id="project-0-agent-18"]');
      const title = row.querySelector('.session-nav-title');
      return { title: title.textContent, preview: row.querySelector('.session-last-message')?.textContent,
        bounded: title.clientHeight <= 2 * parseFloat(getComputedStyle(title).lineHeight) + 1, fullTitle: title.title === title.textContent, overflow: document.documentElement.scrollWidth > innerWidth,
        noise: [...document.querySelectorAll('.session-last-message')].some((node) => /Agent:|─|❯/.test(node.textContent)),
        detached: document.body.textContent.includes('Detached abcdef12'), unavailable: document.body.textContent.includes('Branch unavailable') };
    });
    assert.ok(!result.title.startsWith('Codex:')); assert.equal(result.preview, 'The navigation now keeps the conversation title clear.');
    assert.equal(result.overflow, false); assert.equal(result.noise, false); assert.ok(result.bounded); assert.ok(result.fullTitle); assert.ok(result.detached); assert.ok(result.unavailable);
    if (output) await page.screenshot({ path: join(output, `sidebar-${width}-${theme}.png`) });
  }
  assert.deepEqual(errors, []);
  console.log('sidebar identity: 1141/1440/390, both themes; authored previews, noise omission, branch/detached/unavailable, bounded titles passed');
} finally {
  if (browser) await closeBrowser(browser); if (server) await closeServer(server); await rm(work, { recursive: true, force: true });
}

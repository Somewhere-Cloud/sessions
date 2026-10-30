import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

// A project without a favicon gets two letters and a hue of its own, both
// derived from its name alone.
const scratch = await mkdtemp(join(tmpdir(), 'sessions-monogram-'));
try {
  const output = join(scratch, 'monogram.mjs');
  await build({ entryPoints: ['src/lib/projectMonogram.ts'], bundle: true, format: 'esm', outfile: output, platform: 'node', target: 'node20', logLevel: 'silent' });
  const { projectMonogram } = await import(`${pathToFileURL(output).href}?v=${Date.now()}`);
  const letters = (name) => projectMonogram(name).letters;
  assert.equal(letters('Sessions'), 'SE');
  assert.equal(letters('emailsomewhere-refresh'), 'ER');
  assert.equal(letters('uzihaq/bolo-web'), 'UB');
  assert.equal(letters('somewhereTech'), 'ST');
  assert.equal(letters('tech'), 'TE');
  assert.equal(letters('  '), '·');
  assert.equal(projectMonogram('Sessions').hue, projectMonogram('sessions').hue, 'hue ignores case');
  assert.notEqual(projectMonogram('Sessions').hue, projectMonogram('brightroom').hue);
  assert.ok(projectMonogram('anything').hue >= 0 && projectMonogram('anything').hue < 360);
  console.log('project monogram smoke passed');
} finally {
  await rm(scratch, { recursive: true, force: true });
}

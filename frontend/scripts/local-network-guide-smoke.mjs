// The nearby-access guide may only claim what was actually observed.
//
// The host's permission status is its last observation — macOS exposes no API
// for the Local Network switch — so a stored `granted` is history, and a check
// that finds nothing or fails is fresher evidence than that history.
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

const scratch = await mkdtemp(join(tmpdir(), 'sessions-local-network-smoke-'));
const output = join(scratch, 'local-network-guide.mjs');

try {
  await build({
    entryPoints: ['src/lib/localNetworkGuide.ts'],
    bundle: true, format: 'esm', outfile: output, platform: 'node', target: 'node20', logLevel: 'silent'
  });
  const { localNetworkGuideMode } = await import(`${pathToFileURL(output).href}?v=${Date.now()}`);
  const mode = (status, sawUnproven, lastCheck) => localNetworkGuideMode({ status, sawUnproven, lastCheck });

  // Fresh contact is the only thing that proves nearby access works now.
  assert.equal(mode('granted', true, 'reached'), 'confirmed');
  assert.equal(mode('not-yet-asked', true, 'reached'), 'confirmed');

  // A stored success plus an empty discovery is not a restored connection, but
  // neither is it evidence about the macOS switch: the calm wording stays up.
  assert.equal(mode('granted', true, 'none-found'), 'not-found');
  assert.equal(mode('granted', true, 'failed'), 'not-found');
  assert.equal(mode('denied', true, 'failed'), 'not-found');

  // Without fresh evidence, a stored success is reported as history, never as a
  // live connection.
  assert.equal(mode('granted', true, 'none'), 'previously-worked');

  // Someone who never saw a problem is not shown a recovery surface at all, and
  // platforms without the permission never see one.
  assert.equal(mode('granted', false, 'none'), 'hidden');
  assert.equal(mode('not-required', true, 'failed'), 'hidden');
  assert.equal(mode(undefined, false, 'none'), 'hidden');

  // Unproven and older-host states are calm: nothing names the setting.
  assert.equal(mode('not-yet-asked', true, 'none'), 'unconfirmed');
  assert.equal(mode('denied', true, 'none'), 'unconfirmed');

  // Only the host's verified block shows the permission steps, and it outranks
  // an empty or failed check, which cannot contradict it.
  assert.equal(mode('blocked', true, 'none'), 'blocked');
  assert.equal(mode('blocked', true, 'failed'), 'blocked');
  assert.equal(mode('blocked', true, 'none-found'), 'blocked');
  assert.equal(mode('blocked', true, 'reached'), 'confirmed');

  // The permission wording lives only in the blocked branch of the component.
  const component = await readFile('src/components/LocalNetworkGuide.tsx', 'utf8');
  const settingsMentions = component.split('System Settings →').length - 1;
  assert.equal(settingsMentions, 1, 'System Settings steps must appear exactly once, in the verified-block guide');
  const calmBranch = component.slice(component.indexOf("mode === 'unconfirmed'"), component.indexOf('const endpoint ='));
  assert.ok(!calmBranch.includes('System Settings') && calmBranch.includes('off, asleep, or on another network'));

  console.log('local-network guide smoke passed');
} finally {
  await rm(scratch, { recursive: true, force: true });
}

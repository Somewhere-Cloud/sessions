import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

// A delegated lane's report is parsed from the exact text the daemon sends
// (runtime/internal/api/parent_reports.go formatParentReport), and saved
// history tool steps become finished work-log rows.
const scratch = await mkdtemp(join(tmpdir(), 'sessions-lane-report-'));
const entry = join(scratch, 'entry.ts');
const output = join(scratch, 'lane-report.mjs');
try {
  await writeFile(entry, `export { parseLaneReport } from ${JSON.stringify(join(process.cwd(), 'src/lib/laneReport.ts'))};
export { savedToolActivity, summarizeActivity, toolRunning } from ${JSON.stringify(join(process.cwd(), 'src/lib/workLog.ts'))};\n`);
  await build({ entryPoints: [entry], bundle: true, format: 'esm', outfile: output, platform: 'node', target: 'node20', logLevel: 'silent' });
  const { parseLaneReport, savedToolActivity, summarizeActivity, toolRunning } = await import(`${pathToFileURL(output).href}?v=${Date.now()}`);
  const child = '9075b95a-2987-476e-a3ba-094be0c86242';
  const machine = '9b2f0c3e-1d4a-4e5b-8c6d-7e8f9a0b1c2d';

  const finished = parseLaneReport(`[sessions] Delegated lane "fix \\"tests\\"" (${child}) finished:\ntests pass; committed abc123\n\nsecond paragraph\n\nFull result: sessions last ${child} · Link for the user: sessions://lane/${child}?machine=${machine}`);
  assert.deepEqual(finished, {
    name: 'fix "tests"', sessionId: child, outcome: 'finished',
    body: 'tests pass; committed abc123\n\nsecond paragraph',
    lane: { sessionId: child, machineId: machine }
  });

  const blocked = parseLaneReport(`[sessions] Delegated lane "worker" (${child}) needs input:\nAllow edits to go.mod?\n\nFull result: sessions last ${child}`);
  assert.equal(blocked.outcome, 'needs input');
  assert.deepEqual(blocked.lane, { sessionId: child });

  // Anything else a lane sends stays an ordinary message.
  assert.equal(parseLaneReport('please review my branch'), null);
  assert.equal(parseLaneReport(`[sessions] Delegated lane "worker" (${child}) finished:\nbody\n\nFull result: sessions last 00000000-0000-4000-8000-000000000000`), null);

  const steps = savedToolActivity([
    { id: 't1', name: 'Bash', input: { command: 'go test ./...', description: 'Run all tests' }, result: 'ok' },
    { id: 't2', name: 'Read', input: { file_path: 'missing.go' }, failed: true },
    { id: 't3', name: 'Grep', input: { pattern: 'TODO' } }
  ]);
  assert.equal(steps.length, 3);
  assert.equal(steps[0].call.inputPreview, 'Run all tests');
  assert.equal(steps[0].call.resultFull, 'ok');
  assert.ok(steps.every((step) => !toolRunning(step.call, false)), 'saved steps never show as running');
  assert.equal(summarizeActivity(steps), 'Ran a command, read a file, searched once, 1 failed');
  assert.deepEqual(savedToolActivity(undefined), []);
  console.log('lane report smoke passed');
} finally {
  await rm(scratch, { recursive: true, force: true });
}

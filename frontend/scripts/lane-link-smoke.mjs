import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

// A lane link is only ever two UUIDs. The viewer re-checks what the native
// shell handed over and resolves the machine the way the fleet navigator
// does: a configured machine by its stable identity, an unidentified one by
// asking it, and anything else as a machine this app is not connected to.
const scratch = await mkdtemp(join(tmpdir(), 'sessions-lane-link-'));
const output = join(scratch, 'lane-link.mjs');
try {
  await build({
    entryPoints: ['src/lib/laneLink.ts'],
    bundle: true,
    format: 'esm',
    outfile: output,
    platform: 'node',
    target: 'node20',
    external: ['react', 'zustand'],
    logLevel: 'silent'
  });
  const { findLaneLinks, laneLinkTarget, resolveLaneLinkServerId } = await import(`${pathToFileURL(output).href}?v=${Date.now()}`);
  const session = '31000000-0000-4000-8000-0000000000a1';
  const machine = '9b2f0c3e-1d4a-4e5b-8c6d-7e8f9a0b1c2d';
  const other = '0f1e2d3c-4b5a-4968-8776-655443322110';

  assert.deepEqual(laneLinkTarget({ sessionId: session }), { sessionId: session });

  // Links inside command output or a lane's report: each lane once, in order,
  // with its machine when named; anything that is not two UUIDs is ignored.
  assert.deepEqual(findLaneLinks(`open in Sessions: sessions://lane/${session}?machine=${machine}\nagain sessions://lane/${session}`),
    [{ sessionId: session, machineId: machine }]);
  assert.deepEqual(findLaneLinks(`a sessions://lane/${other} b sessions://lane/${session}`),
    [{ sessionId: other }, { sessionId: session }]);
  assert.deepEqual(findLaneLinks('sessions://lane/not-a-uuid and sessions://pair/abc'), []);
  assert.deepEqual(findLaneLinks(undefined), []);
  assert.deepEqual(laneLinkTarget({ sessionId: session.toUpperCase(), machineId: machine }), { sessionId: session, machineId: machine });
  for (const bad of [null, 'sessions://lane/x', { sessionId: '../etc' }, { sessionId: session, machineId: 'mini' }, { sessionId: `${session} ` }]) {
    assert.equal(laneLinkTarget(bad), null, JSON.stringify(bad));
  }

  const local = { id: 'local', name: 'This Mac', host: 'localhost', port: 8787, isDefault: true };
  const mini = { id: 'mini', name: 'mini', host: '192.168.1.20', port: 8787, isDefault: false, machineId: machine };
  const never = async () => { throw new Error('a known machine must not be asked'); };
  assert.equal(await resolveLaneLinkServerId({ sessionId: session }, [local, mini], 'mini', never), 'mini');
  assert.equal(await resolveLaneLinkServerId({ sessionId: session, machineId: machine }, [local, mini], 'local', never), 'mini');

  const asked = [];
  const identify = async (server) => { asked.push(server.id); return server.id === 'local' ? other : null; };
  assert.equal(await resolveLaneLinkServerId({ sessionId: session, machineId: other }, [local, mini], 'mini', identify), 'local');
  assert.deepEqual(asked, ['local']);
  const unknown = '11111111-2222-4333-8444-555555555555';
  assert.equal(await resolveLaneLinkServerId({ sessionId: session, machineId: unknown }, [local, mini], 'local', identify), null);
  const failing = async () => { throw new Error('offline'); };
  assert.equal(await resolveLaneLinkServerId({ sessionId: session, machineId: unknown }, [local], 'local', failing), null);
} finally {
  await rm(scratch, { recursive: true, force: true });
}

console.log('lane link smoke: ok');

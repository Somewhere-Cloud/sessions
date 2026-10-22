import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

const scratch = await mkdtemp(join(tmpdir(), 'sessions-observations-smoke-'));
const output = join(scratch, 'observations.mjs');

const now = Date.UTC(2026, 9, 16, 17, 0, 0);
const fiveHoursAgo = now - 5 * 60 * 60 * 1000;

function session(overrides) {
  return {
    id: 's',
    observations: {
      version: 1,
      capabilities: { runtime: 'claude-structured', turn_boundaries: 'provider-stream', tool_records: 'provider-stream', terminal_output: false, approvals: 'runner-brokered' },
      runner: { state: 'reachable', since: fiveHoursAgo, exit_code: null, adopted_at: null },
      working: { value: true, source: 'provider-stream' },
      turn: { open: true, started_at: fiveHoursAgo, started_by: 'provider', provider_confirmed_at: fiveHoursAgo, ended_at: null, ended_by: null },
      last: { provider_record_observed_at: fiveHoursAgo, provider_record_event_at: null, terminal_output_at: null, transcript_record_event_at: null, input_dispatched_at: fiveHoursAgo, provider_user_record_at: null },
      tools: { open_count: 1, open_count_exact: true, history_complete: true, last_started_at: fiveHoursAgo, last_ended_at: null, last_end_status: null, observed_since: fiveHoursAgo },
      approval: { pending: false, id: null, kind: null, requested_at: null },
      observers: [],
      ...overrides
    }
  };
}

try {
  await build({ entryPoints: ['src/lib/observations.ts'], bundle: true, format: 'esm', outfile: output, platform: 'node', target: 'node20', logLevel: 'silent' });
  const { observationNote, observedTime } = await import(`${pathToFileURL(output).href}?v=${Date.now()}`);
  assert.equal(observedTime(1e30, now), 'unknown');
  assert.equal(observedTime(NaN, now), 'unknown');

  // An open turn whose newest provider record is five hours old: the gap is
  // stated as times, never as a guessed status.
  const stale = observationNote(session({}), now);
  assert.match(stale, /Turn open since 2026-10-16 12:00:00 UTC \(5h 0m ago\), started by provider\./);
  assert.match(stale, /Last provider record observed 2026-10-16 12:00:00 UTC \(5h 0m ago\); its own time unknown\./);
  assert.match(stale, /1 tool call open\. Last tool end unknown\./);
  for (const guess of [/stale/i, /stuck/i, /idle/i, /hung/i, /blocked/i]) assert.doesNotMatch(stale, guess);

  // Partial tools: missing ends prevent any bound on currently running calls,
  // even when history_complete alone would say otherwise.
  const partial = observationNote(session({
    tools: { open_count: 3, open_count_exact: false, history_complete: true, last_started_at: fiveHoursAgo, last_ended_at: fiveHoursAgo, last_end_status: 'error', observed_since: fiveHoursAgo }
  }), now);
  assert.match(partial, /3 tool starts without a recorded end \(partial history; current open count unknown\)\./);
  assert.match(partial, /Last tool end 2026-10-16 12:00:00 UTC \(5h 0m ago\) \(error\)\./);

  // Zero tracked starts with an inexact count does not mean "none running".
  const zeroPartial = observationNote(session({ tools: { open_count: 0, open_count_exact: false, history_complete: false, last_ended_at: null, last_end_status: null } }), now);
  assert.match(zeroPartial, /0 tool starts without a recorded end/);

  // A Sessions dispatch the provider has not confirmed says so.
  const dispatched = observationNote(session({ turn: { open: true, started_at: fiveHoursAgo, started_by: 'sessions-dispatch', provider_confirmed_at: null, ended_at: null, ended_by: null } }), now);
  assert.match(dispatched, /started by sessions-dispatch; not confirmed by the provider\./);

  // Unknown labels and times say unknown.
  const unknown = observationNote(session({
    working: null,
    turn: { open: true, started_at: null, started_by: null, provider_confirmed_at: null, ended_at: null, ended_by: null },
    last: { provider_record_observed_at: null, provider_record_event_at: null, terminal_output_at: null, transcript_record_event_at: null, input_dispatched_at: null, provider_user_record_at: null }
  }), now);
  assert.match(unknown, /Turn open since unknown, started by unknown\./);
  assert.match(unknown, /Last provider record observed unknown; its own time unknown\./);
  assert.doesNotMatch(unknown, /Working state/);

  // A runtime without turn or tool records says nothing about them.
  const terminal = observationNote(session({ working: null, turn: null, last: null, tools: null, approval: null }), now);
  assert.equal(terminal, '');
  assert.equal(observationNote({ id: 'none' }, now), '');

  // Claude hook facts are restated with observed times; configured coverage
  // without records says so, and a gap is named. Stop is never "done".
  const hookEvent = { name: 'Stop', observed_at: fiveHoursAgo, conversation_id: 'c', tool_use_id: null, tool_name: null, agent_id: null, detail: null, is_interrupt: null, stop_hook_active: false };
  const hooks = (overrides) => ({ source: 'claude-command-hooks', coverage: 'receiving', unavailable_reason: null, conversation_id: 'c', last_event: hookEvent, events: { Stop: hookEvent }, tools: null, gaps: [], rejected: { foreign_launch: 0, foreign_conversation: 0, malformed: 0 }, ...overrides });
  const hooked = observationNote(session({ hooks: hooks({}) }), now);
  assert.match(hooked, /Last Claude hook Stop observed 2026-10-16 12:00:00 UTC \(5h 0m ago\)\./);
  for (const guess of [/done/i, /finished/i, /complete/i, /blocked/i]) assert.doesNotMatch(hooked.slice(hooked.indexOf('Claude hook')), guess);
  assert.match(observationNote(session({ hooks: hooks({ last_event: null, events: {}, coverage: 'configured' }) }), now), /Claude hooks configured; no hook event received yet\./);
  assert.match(observationNote(session({ hooks: hooks({ gaps: ['sink-full'] }) }), now), /Hook history incomplete: sink-full\./);
  assert.match(observationNote(session({ hooks: hooks({ coverage: 'unavailable', unavailable_reason: 'not-installed-for-this-launch', last_event: null, events: null }) }), now), /Claude hooks unavailable \(not-installed-for-this-launch\)\./);

  console.log('observations smoke: ok');
} finally {
  await rm(scratch, { recursive: true, force: true });
}

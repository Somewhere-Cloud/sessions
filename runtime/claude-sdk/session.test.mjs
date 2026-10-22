import test from 'node:test';
import assert from 'node:assert/strict';
import { SDKSession } from './session.mjs';

function fixture() {
  const events = [], inputs = [];
  let end;
  const provider = {
    async *[Symbol.asyncIterator]() { await new Promise(resolve => { end = resolve; }); },
    interrupt: async () => {}, close() { end(); },
  };
  const query = ({ prompt }) => { inputs.push(prompt); return provider; };
  const session = new SDKSession(query, { sessionId: 'conversation' }, e => events.push(e));
  return { session, events, inputs, provider };
}
const result = (id, extra = {}) => ({ type: 'result', subtype: 'success',
  session_id: 'conversation', user_message_uuid: id, is_error: false, ...extra });

test('background snapshots replace membership and old run notifications do not remove new runs', async () => {
  const { session } = fixture();
  session.observe({ type: 'system', subtype: 'background_tasks_changed', tasks: [
    { task_id: 'watcher', run_id: '002', task_type: 'shell', ambient: true },
  ] });
  session.observe({ type: 'system', subtype: 'task_notification', task_id: 'watcher', run_id: '001' });
  assert.equal(session.tasks.size, 1);
  assert.equal(session.tasks.get('watcher').ambient, true);
  session.observe({ type: 'system', subtype: 'background_tasks_changed', tasks: [] });
  assert.equal(session.tasks.size, 0);
  await session.close();
});

test('watcher survives result; next prompt completes without provider exit', async () => {
  const { session, events, inputs } = fixture();
  session.submit('first', 'start a watcher');
  assert.equal((await inputs[0].next()).value.uuid, 'first');
  session.observe({ type: 'system', subtype: 'task_started', task_id: 'watcher' });
  session.observe(result('first'));
  assert.equal(events.at(-1).ready, true);
  assert.equal(events.at(-1).background_tasks.length, 1);
  session.submit('second', 'do different work');
  assert.equal((await inputs[0].next()).value.uuid, 'second');
  session.observe(result(undefined, { turn_origin: 'task_notification' }));
  assert.equal(session.active.id, 'second');
  session.observe(result('first')); // late duplicate result
  assert.equal(session.active.id, 'second');
  session.observe(result('second'));
  assert.deepEqual(events.filter(e => e.type === 'settled').map(e => e.operation_id), ['first', 'second']);
  assert.equal(events.at(-1).transport_open, true);
  await session.close();
});

test('acceptance is not consumption; duplicate submission does not write twice', async () => {
  const { session, events, inputs } = fixture();
  session.submit('one', 'hello'); session.submit('one', 'hello');
  assert.equal(session.pending.length, 1);
  assert.equal(events.some(e => e.type === 'consumed'), false);
  assert.throws(() => session.submit('one', 'different'), /different text/);
  await inputs[0].next();
  session.observe({ type: 'command_lifecycle', command_uuid: 'one', state: 'started' });
  session.observe(result('one'));
  assert.equal(events.filter(e => e.type === 'consumed').length, 1);
  session.submit('one', 'hello');
  assert.equal(session.pending.length, 0);
  await session.close();
});

test('unbound, other-session and subagent results cannot finish a prompt', async () => {
  const { session } = fixture();
  session.submit('one', 'hello');
  session.observe(result(undefined));
  session.observe(result('one', { parent_tool_use_id: 'subagent' }));
  assert.equal(session.active.id, 'one');
  assert.throws(() => session.observe(result('one', { session_id: 'other' })), /another/);
  await session.close();
});

test('interruption is not success or authentication failure', async () => {
  const { session, events } = fixture();
  session.submit('one', 'hello');
  await session.interrupt('one');
  assert.equal(session.active.id, 'one');
  session.observe(result('one', { terminal_reason: 'aborted_tools' }));
  assert.equal(events.find(e => e.type === 'settled').phase, 'interrupted');
  await session.close();
});

test('account limit is a failed matched result, not process liveness', async () => {
  const { session, events } = fixture();
  session.submit('one', 'hello');
  session.observe(result('one', { api_error_status: 429, is_error: true }));
  assert.equal(events.find(e => e.type === 'settled').phase, 'failed');
  assert.equal(events.at(-1).transport_open, true);
  await session.close();
});

test('result racing interrupt does not admit work before interrupt control finishes', async () => {
  const { session, provider, events } = fixture();
  let ack;
  provider.interrupt = () => new Promise(resolve => { ack = resolve; });
  session.submit('one', 'hello');
  const interrupted = session.interrupt('one');
  session.observe(result('one'));
  assert.equal(events.at(-1).ready, false);
  assert.throws(() => session.submit('two', 'next'), /interrupt/);
  ack(); await interrupted;
  assert.equal(events.at(-1).ready, true);
  await session.close();
});

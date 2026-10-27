import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { RemoteView } from '../src/components/RemoteView';
import type { StructuredSessionEvent } from '../src/types';

const base = { source: 'codex-app-server', conversationId: 'thread-1' } as const;
const steeringFixture = new URLSearchParams(window.location.search).has('steering');
// ?claude renders a live Claude turn: thinking, a finished command, a failed
// one, prose, and a command still running.
const claudeFixture = new URLSearchParams(window.location.search).has('claude');
const turnStart = Date.now() - 42_000;
const at = (offset: number): string => new Date(turnStart + offset).toISOString();
const claudeEvents: StructuredSessionEvent[] = [
  { type: 'user', uuid: 'cl-report', timestamp: at(-60_000), author: { kind: 'session', id: '31000000-0000-4000-8000-0000000000a1', name: 'docs-lane', client: 'sessions-report' },
    message: { role: 'user', content: '[sessions] Delegated lane "docs-lane" (31000000-0000-4000-8000-0000000000a1) finished:\nDocs updated.\n\nFull result: sessions last 31000000-0000-4000-8000-0000000000a1 · Link for the user: sessions://lane/31000000-0000-4000-8000-0000000000a1' } },
  { type: 'user', uuid: 'cl-user', timestamp: at(0), message: { role: 'user', content: 'Make the parser tests pass.' } },
  { type: 'assistant', uuid: 'cl-think', timestamp: at(2_000), message: { role: 'assistant', content: [
    { type: 'thinking', thinking: 'The parser test failure is probably the new escape handling. I should run the package tests first.' }
  ] } },
  { type: 'assistant', uuid: 'cl-run', timestamp: at(4_000), message: { role: 'assistant', content: [
    { type: 'tool_use', id: 'cl-tool1', name: 'Bash', input: { command: 'go test ./parser', description: 'Run parser tests' } }
  ] } },
  { type: 'user', uuid: 'cl-r1', timestamp: at(9_000), message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'cl-tool1', is_error: true, content: '--- FAIL: TestEscape (0.00s)\nFAIL parser' }
  ] } },
  { type: 'assistant', uuid: 'cl-read', timestamp: at(11_000), message: { role: 'assistant', content: [
    { type: 'tool_use', id: 'cl-tool2', name: 'Read', input: { file_path: '/Users/example/sessions/parser/escape.go' } }
  ] } },
  { type: 'user', uuid: 'cl-r2', timestamp: at(12_000), message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'cl-tool2', content: 'package parser' }
  ] } },
  { type: 'assistant', uuid: 'cl-text', timestamp: at(20_000), message: { role: 'assistant', content: [
    { type: 'text', text: 'The escape handler drops a trailing backslash. Fixing it and rerunning the tests.' }
  ] } },
  { type: 'assistant', uuid: 'cl-edit', timestamp: at(25_000), message: { role: 'assistant', content: [
    { type: 'tool_use', id: 'cl-tool3', name: 'Edit', input: { file_path: '/Users/example/sessions/parser/escape.go', old_string: 'a', new_string: 'b' } }
  ] } },
  { type: 'user', uuid: 'cl-r3', timestamp: at(26_000), message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'cl-tool3', content: 'updated' }
  ] } },
  { type: 'assistant', uuid: 'cl-delegate', timestamp: at(28_000), message: { role: 'assistant', content: [
    { type: 'tool_use', id: 'cl-tool5', name: 'Bash', input: { command: 'sessions new --report --name docs-lane', description: 'Delegate the docs update' } }
  ] } },
  { type: 'user', uuid: 'cl-r5', timestamp: at(29_000), message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'cl-tool5', content: '31000000-0000-4000-8000-0000000000a1\nopen in Sessions: sessions://lane/31000000-0000-4000-8000-0000000000a1' }
  ] } },
  { type: 'assistant', uuid: 'cl-rerun', timestamp: at(30_000), message: { role: 'assistant', content: [
    { type: 'tool_use', id: 'cl-tool4', name: 'Bash', input: { command: 'go test ./...', description: 'Run all tests' } }
  ] } }
] as unknown as StructuredSessionEvent[];
const events: StructuredSessionEvent[] = [
  {
    ...base,
    type: 'user',
    uuid: 'user-1',
    timestamp: '2026-07-20T10:00:00Z',
    author: { kind: 'session', id: 'lane-1', name: 'PM Claude', client: 'sessions-cli' },
    message: { role: 'user', content: 'Build a first-class Codex GUI inside Sessions.' }
  },
  {
    ...base,
    type: 'codex', subtype: 'plan_updated', turnId: 'turn-1',
    explanation: 'Keep the durable Go runtime and make structured events the UI boundary.',
    plan: [
      { step: 'Preserve complete app-server events', status: 'completed' },
      { step: 'Render the conversation and activity', status: 'completed' },
      { step: 'Verify the signed desktop build', status: 'inProgress' }
    ]
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    timestamp: '2026-07-20T10:02:14Z',
    item: {
      id: 'reasoning-1', type: 'reasoning',
      summary: ['The current backend already owns the hard lifecycle boundaries, so the GUI should project those events instead of adding another server.']
    }
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    item: {
      id: 'command-1', type: 'commandExecution', status: 'completed',
      command: 'go test ./...', cwd: '/Users/example/sessions',
      aggregatedOutput: 'ok   github.com/somewhere-tech/sessions/runtime/internal/codexapp\n',
      exitCode: 0, durationMs: 881
    }
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    item: {
      id: 'files-1', type: 'fileChange', status: 'completed',
      changes: [{
        path: 'frontend/src/components/RemoteView.tsx', kind: 'update',
        diff: '@@ -1,2 +1,3 @@\n+ Conversation\n+ Activity timeline\n+ Plan progress'
      }]
    }
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    item: { id: 'update-1', type: 'agentMessage', phase: 'commentary', text: 'The structured event adapter is live. I am finishing the desktop verification now.' }
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    item: { id: 'update-2', type: 'agentMessage', phase: 'commentary', text: 'The desktop fixture passes. I am checking the final response now.' }
  },
  {
    ...base,
    type: 'codex', subtype: 'item_completed', turnId: 'turn-1',
    item: {
      id: 'answer-1', type: 'agentMessage', phase: 'final_answer',
      text: 'Sessions now has a real Codex conversation view with streaming activity, plans, command output, file diffs, reasoning summaries, context usage, and a safe interrupt control.'
    }
  },
  { ...base, type: 'codex', subtype: 'turn_completed', turnId: 'turn-1', status: 'completed' }
];

const sidebar = {
  parserName: 'Codex',
  parserIcon: '🟢',
  isWorking: false,
  timer: '',
  tokens: '4.8k',
  context: '18% of 258.0k',
  finalElapsed: '2m 14s',
  currentTask: '',
  checklist: []
};

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <main style={{ height: '100vh', display: 'flex' }}>
      <RemoteView
        sessionId="fixture-session"
        events={claudeFixture ? claudeEvents : events}
        send={() => {}}
        submitMessage={async () => {}}
        steerMessage={steeringFixture ? async () => {} : undefined}
        connected
        hasEarlierClaudeEvents={false}
        loadingEarlierClaudeEvents={false}
        onLoadEarlierClaudeEvents={() => {}}
        sidebar={{ ...sidebar, parserName: claudeFixture ? 'Claude' : sidebar.parserName, isWorking: steeringFixture || claudeFixture }}
        cwd="/Users/example/sessions"
        onOpenTerminal={() => {}}
        provider={claudeFixture ? 'claude-code' : 'codex'}
      />
    </main>
  </React.StrictMode>
);

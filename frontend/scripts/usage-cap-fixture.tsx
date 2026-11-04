import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { UsageCapCard } from '../src/components/UsageCapCard';
import { UsageCapChip } from '../src/components/UsageCapChip';
import { useServers } from '../src/lib/servers';
import type { SessionInfo } from '../src/types';

// In-memory daemon: GET and PUT /api/usage-cap only. Every request is
// recorded so the smoke can check the card sends exactly the CLI's changes.
useServers.setState({ servers: [{ id: 'fixture', name: 'Fixture Mac', host: 'fixture.invalid', port: 8787, isDefault: true }], activeId: 'fixture' });
const resetsAt = Date.now() + 3 * 60 * 60 * 1000;
const status = {
  enabled: true, percent: 90, tools: { claude: 90, codex: 90 }, switch: true,
  accounts: [{ tool: 'claude', name: '', state: 'over', cap: 90, usedPercent: 92, window: 'weekly', resetsAt, detail: '92% of weekly' }],
  lanes: [{ id: 'lane-1', tool: 'claude', account: '', override: null, enabled: true, hold: { state: 'waiting', account: '' } }]
};
const requests: Array<{ method: string; url: string; body?: Record<string, unknown> }> = [];
(window as unknown as { usageCapRequests: typeof requests }).usageCapRequests = requests;
window.fetch = async (input, init) => {
  const url = String(input);
  const method = init?.method ?? 'GET';
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  requests.push({ method, url, body });
  if (!url.endsWith('/api/usage-cap')) return new Response(JSON.stringify({ error: 'not in fixture' }), { status: 404 });
  if (method === 'PUT') Object.assign(status, body);
  return new Response(JSON.stringify(status), { headers: { 'content-type': 'application/json' } });
};
const session = {
  id: 'lane-1', name: 'Long run', tool: 'claude', cmd: 'claude',
  usageCap: {
    state: 'waiting', tool: 'claude', account: '', accountLabel: 'the default claude login', usedPercent: 92, cap: 90,
    window: 'weekly', resetsAt, switchAccount: 'work', switchLabel: 'claude/work'
  }
} as unknown as SessionInfo;
createRoot(document.getElementById('root')!).render(
  <main className="operations-shell settings-page" style={{ minHeight: '100vh', height: 'auto', display: 'block' }}>
    <div id="chip"><UsageCapChip session={session} serverId="fixture" /></div>
    <UsageCapCard clientOnly={false} hostName="Fixture Mac" />
  </main>
);

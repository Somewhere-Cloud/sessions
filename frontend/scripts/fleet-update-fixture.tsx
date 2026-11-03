import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { SidebarUpdateCards } from '../src/components/SidebarUpdateCards';
import { FleetSessionsUpdates } from '../src/components/FleetSessionsUpdates';
import { useServers } from '../src/lib/servers';
import { setSessionsFleetWait } from '../src/lib/sessionsFleet';

// In-memory daemons only: this Mac on 0.2.46, a Mac mini on 0.2.40, and a
// studio Mac that is current (scenario "one") or also behind ("many"). Each
// update walks the real job phases, and the first poll fails the way a
// relay does while the remote daemon restarts.
const scenario = (window as unknown as { __scenario: string }).__scenario;
const hosts: Record<string, { version: string; started: number; polls: number }> = {
  'local.invalid': { version: '0.2.46', started: 0, polls: 0 },
  'mini.invalid': { version: '0.2.40', started: 0, polls: 0 },
  'studio.invalid': { version: scenario === 'many' ? '0.2.44' : '0.2.46', started: 0, polls: 0 }
};
(window as unknown as { __hosts: typeof hosts }).__hosts = hosts;
const phases = ['', 'downloading', 'restarting', 'updated'];

useServers.setState({
  servers: [
    { id: 'local', name: 'This Mac', host: 'local.invalid', port: 8787, isDefault: true },
    { id: 'mini', name: 'Mac mini', customName: 'Mac mini', host: 'mini.invalid', port: 8787, isDefault: false },
    { id: 'studio', name: 'Studio', customName: 'Studio', host: 'studio.invalid', port: 8787, isDefault: false }
  ],
  activeId: 'local'
});
setSessionsFleetWait(() => new Promise((resolve) => window.setTimeout(resolve, 30)));

const reply = (status: number, body: unknown): Response => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
window.fetch = async (input, init) => {
  const url = new URL(String(input));
  const host = hosts[url.hostname];
  if (!host) return reply(404, { error: 'not found' });
  if (url.pathname === '/api/providers') return reply(200, { providers: [], updateCheck: { enabled: false } });
  if (url.pathname !== '/api/update') return reply(404, { error: 'not found' });
  const job = (phase: string) => ({ id: `job-${url.hostname}`, phase, fromVersion: host.version, toVersion: '0.2.46',
    startedAt: new Date().toISOString(), detail: `phase ${phase}`, ...(phase === 'updated' ? { finishedAt: new Date().toISOString(), detail: 'Updated Sessions → 0.2.46. Running sessions kept running.' } : {}) });
  if (init?.method === 'POST') {
    host.started += 1;
    return reply(202, { job: job('starting') });
  }
  if (!host.started) return reply(200, { version: host.version, os: 'darwin', supported: true });
  const phase = phases[Math.min(host.polls++, phases.length - 1)];
  if (!phase) return reply(502, { error: 'sessionsd is restarting' });
  if (phase === 'updated') host.version = '0.2.46';
  return reply(200, { version: host.version, os: 'darwin', supported: true, job: job(phase) });
};

createRoot(document.getElementById('root')!).render(
  <main className="operations-shell settings-page" data-theme={document.documentElement.dataset.theme} style={{ minHeight: '100vh', height: 'auto', display: 'block' }}>
    <aside className="product-sidebar" style={{ width: 280 }}><SidebarUpdateCards /></aside>
    <FleetSessionsUpdates />
  </main>
);

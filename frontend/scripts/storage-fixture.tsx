import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { StorageView } from '../src/components/StorageView';
import { useServers } from '../src/lib/servers';
import type { StorageReport } from '../src/api/sessionsd/storage';

const theme = new URLSearchParams(location.search).get('theme') ?? 'dark';
document.documentElement.dataset.theme = theme;
document.body.style.overflow = 'auto';
document.body.style.height = 'auto';
document.documentElement.style.height = 'auto';
document.getElementById('root')!.style.height = 'auto';
const size = { logical: 1024 ** 3, allocated: 512 * 1024 ** 2, shared_logical: 0, files: 200, symlinks: 2, confidence: 'observed' as const };
const path = '/fixture/projects/very-long-repository-name/worktrees/parser-fix';
const report: StorageReport = {
  machine_id: 'remote-fixture', machine_name: 'Remote fixture workstation', preview: true, complete: false, scanned_at: 1791930000000,
  roots: [{ path, ownership: 'unknown', sessions: [], git: { dirty: 'yes', untracked: 'yes', unpushed: 'unknown' }, references: [{ path, session: 'live-fixture', pid: 999 }],
    bytes: { ...size, shared_logical: size.logical / 2, confidence: 'partial' }, categories: { source: { ...size, logical: size.logical / 4, allocated: size.allocated / 4 }, dependencies: { ...size, logical: size.logical / 2, allocated: size.allocated / 2, shared_logical: size.logical / 2 }, unknown: { ...size, logical: size.logical / 4, allocated: null, confidence: 'partial' } },
    candidates: [{ path: `${path}/frontend/node_modules`, category: 'dependencies', action: 'kept', bytes: { ...size, logical: size.logical / 2, allocated: size.allocated / 2, shared_logical: size.logical / 2, confidence: 'partial' }, reasons: ['external or unrecorded worktree; ownership unknown', 'Git changes: yes', 'untracked files: yes', 'unpushed commits: unknown (local refs only; no fetch)', 'live or unresolved runner references this checkout', 'scan incomplete or allocation unavailable', 'shared hardlinks; freed bytes cannot be inferred'], restore: 'Run npm ci using the recorded lockfile. Restoration has not been tested.' }],
    issues: ['directory permission denied; bytes are a lower bound'] }],
  totals: { ...size, logical: 2.5 * size.logical, allocated: 1.25 * size.logical, shared_logical: 0.75 * size.logical, confidence: 'partial' }, issues: [], limitations: ['Process references cover Sessions runners only; external processes and open files are unknown.']
};
// A host total can differ from row sums when repository sizes overlap.
// Rows are intentionally not summed by the product view.
const completeSize = { ...size, symlinks: 0 };
report.roots.push(
  { path: '/fixture/projects/sessions', ownership: 'unknown', sessions: [], git: { dirty: 'no', untracked: 'no', unpushed: 'no' }, references: [], bytes: { ...completeSize, logical: 2 * size.logical }, categories: {}, candidates: [], issues: [] },
  { path: '/fixture/projects/docs-preview', ownership: 'known', sessions: ['finished'], git: { dirty: 'no', untracked: 'no', unpushed: 'no' }, references: [], bytes: { ...completeSize, logical: size.logical / 4 }, categories: {},
    candidates: [{ path: '/fixture/projects/docs-preview/dist', category: 'build-output', action: 'potentially-reclaimable', bytes: { ...completeSize, logical: size.logical / 8 }, reasons: ['Generated build output; manual review required'], restore: 'Run npm run build. Restoration has not been tested.' }], issues: [] },
  { path: '/fixture/projects/unavailable', ownership: 'unknown', sessions: [], git: { dirty: 'unknown', untracked: 'unknown', unpushed: 'unknown' }, references: [], bytes: { ...size, logical: 0, allocated: null, files: 0, symlinks: 0, confidence: 'partial' }, categories: {}, candidates: [], issues: ['root inaccessible; retained without traversal'] }
);
useServers.setState({ servers: [
  { id: 'local', name: 'Local fixture', host: 'local.fixture.test', port: 8787, isDefault: true },
  { id: 'remote', name: 'Remote fixture workstation', host: 'relay.fixture.test', port: 8787, isDefault: false, relayMachineId: 'remote-fixture' }
], activeId: 'remote' });
const requests: string[] = [];
let mode = 'normal';
window.fetch = async (input, init) => {
  const url = input.toString();
  requests.push(`${init?.method ?? 'GET'} ${url}`);
  if (!url.endsWith('/api/storage/preview')) throw new Error(`Unexpected fixture request: ${url}`);
  if (mode === 'wait') await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(resolve, 800);
    init?.signal?.addEventListener('abort', () => { clearTimeout(timer); reject(new DOMException('Canceled', 'AbortError')); }, { once: true });
  });
  if (mode === 'error') return new Response(JSON.stringify({ error: 'Storage scan already running; wait and retry.' }), { status: 429 });
  if (mode === 'older') return new Response(JSON.stringify({ error: 'not found' }), { status: 404 });
  const answer = structuredClone(report);
  if (url.includes('local.fixture.test')) { answer.machine_id = 'local-fixture'; answer.machine_name = 'Local fixture'; }
  if (mode === 'empty') { answer.roots = []; answer.complete = true; answer.totals = { ...size, logical: 0, allocated: 0, shared_logical: 0, files: 0, symlinks: 0, confidence: 'observed' }; }
  return new Response(JSON.stringify(answer), { headers: { 'Content-Type': 'application/json' } });
};
Object.assign(window, { storageFixture: { requests, setMode: (value: string) => { mode = value; }, select: (activeId: string) => useServers.setState({ activeId }) } });
createRoot(document.getElementById('root')!).render(<div className="operations-shell text-size-s" data-theme={theme} style={{ minHeight: '100dvh', height: 'auto', display: 'block', overflow: 'visible' }}><StorageView /></div>);

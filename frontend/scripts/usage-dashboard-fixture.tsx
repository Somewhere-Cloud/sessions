import React from 'react';
import { createRoot } from 'react-dom/client';
import { UsageDashboard } from '../src/components/UsageDashboard';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';
import '../src/styles/globals.css';

const machines: FakeMachine[] = [{ id: 'local', name: 'This Mac', host: 'local.test', port: 8787, sessions: [] }];
installFakeDaemon(machines);
useFakeMachines(machines, 'local');
const fakeFetch = window.fetch;
window.fetch = async (input, init) => {
  const url = new URL(String(input));
  if (url.pathname !== '/api/usage') return fakeFetch(input, init);
  const row = { key: '2026-10-28', models: ['gpt-6.1-sol'], provider: 'codex', entries: 4,
    tokens: { inputTokens: 12000, cacheCreationTokens: 2000, cacheReadTokens: 26000, outputTokens: 8000, reasoningTokens: 3000 },
    costUSD: 0.24, recordedCostUSD: 0.1, calculatedCostUSD: 0.14, missingPricingEntries: 1 };
  return new Response(JSON.stringify({ schemaVersion: 1, machine: 'This Mac', generatedAt: new Date().toISOString(),
    group: url.searchParams.get('group') || 'daily', mode: 'auto',
    pricing: { source: 'fixture', revision: 'fixture', url: '#pricing', note: 'Some events have no matching price.' },
    scan: { filesSeen: 2, filesRead: 2, linesRead: 4, entriesSeen: 4 }, rows: [row], totals: { ...row, key: 'total' } }),
  { headers: { 'Content-Type': 'application/json' } });
};
const theme = new URLSearchParams(location.search).get('theme') || 'dark';
document.documentElement.dataset.theme = theme;
createRoot(document.getElementById('root')!).render(<main className="operations-shell" data-theme={theme} style={{ height: 'auto', minHeight: '100dvh', maxHeight: 'none', overflow: 'visible', display: 'block' }}><UsageDashboard /></main>);

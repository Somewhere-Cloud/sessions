import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { AutoArchiveCard } from '../src/components/AutoArchiveCard';
import { useServers } from '../src/lib/servers';

useServers.setState({ servers: [{ id: 'fixture', name: 'Fixture Mac', host: 'fixture.invalid', port: 8787, isDefault: true }], activeId: 'fixture' });
window.fetch = async (_input, init) => new Response(JSON.stringify({
  enabled: init?.method === 'PUT' && JSON.parse(String(init.body)).enabled,
  inactivityMs: 86400000, scope: 'machine-structured-sessions'
}));
createRoot(document.getElementById('root')!).render(<main className="operations-shell settings-page" data-theme={document.documentElement.dataset.theme} style={{ minHeight: '100vh', height: 'auto', display: 'block' }}><AutoArchiveCard hostName="Fixture Mac" /></main>);

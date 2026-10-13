import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { PullRequestChip } from '../src/components/PullRequestChip';
import { useServers } from '../src/lib/servers';
import '../src/styles/globals.css';

useServers.setState({ servers: [{ id: 'fixture', name: 'Fixture', host: '127.0.0.1', port: Number(window.location.port), isDefault: true }], activeId: 'fixture' });

// The session header keeps one chip mounted and changes its sessionId, which
// is exactly what this fixture does.
function Fixture(): JSX.Element {
  const [sessionId, setSessionId] = useState('lane-a');
  return <main style={{ padding: 24 }}>
    <button data-show="lane-a" onClick={() => setSessionId('lane-a')}>Show A</button>
    <button data-show="lane-b" onClick={() => setSessionId('lane-b')}>Show B</button>
    <output data-session>{sessionId}</output>
    <div data-chip><PullRequestChip sessionId={sessionId} /></div>
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

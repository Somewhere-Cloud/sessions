import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { PullRequestChip } from '../src/components/PullRequestChip';
import { useServers } from '../src/lib/servers';
import '../src/styles/globals.css';

// "fixture" is this page's own daemon; "mini" is another computer reached
// through its fleet relay path on the same fixture server.
const port = Number(window.location.port);
useServers.setState({ servers: [
  { id: 'fixture', name: 'Fixture', host: '127.0.0.1', port, isDefault: true },
  { id: 'mini', name: 'Mini', host: '127.0.0.1', port, relayMachineId: 'mini' }
], activeId: 'fixture' });

// The session header keeps one chip mounted and changes its sessionId or the
// server that runs the session, which is exactly what this fixture does.
function Fixture(): JSX.Element {
  const [sessionId, setSessionId] = useState('lane-a');
  const [serverId, setServerId] = useState('fixture');
  return <main style={{ padding: 24 }}>
    <button data-show="lane-a" onClick={() => setSessionId('lane-a')}>Show A</button>
    <button data-show="lane-b" onClick={() => setSessionId('lane-b')}>Show B</button>
    <button data-source="fixture" onClick={() => setServerId('fixture')}>Session on this computer</button>
    <button data-source="mini" onClick={() => setServerId('mini')}>Session on Mini</button>
    <button data-activate="fixture" onClick={() => useServers.getState().setActive('fixture')}>Select this computer</button>
    <button data-activate="mini" onClick={() => useServers.getState().setActive('mini')}>Select Mini</button>
    <output data-session>{`${serverId}:${sessionId}`}</output>
    <div data-chip><PullRequestChip sessionId={sessionId} serverId={serverId} /></div>
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

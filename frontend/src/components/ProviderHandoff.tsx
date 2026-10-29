import { lazy, Suspense, useState } from 'react';
import type { SessionInfo } from '../types';

const HandoffDialog = lazy(() => import('./ProviderHandoffDialog').then((module) => ({ default: module.ProviderHandoffDialog })));

export function ProviderHandoff({ session, serverId, onOpen }: {
  session: SessionInfo; serverId: string; onOpen: (id: string) => void;
}): JSX.Element | null {
  const [open, setOpen] = useState(false);
  if (!['claude-structured', 'codex-app-server'].includes(session.kind ?? '')) return null;
  return <>
    <button type="button" className="view-toggle-btn" onClick={() => setOpen(true)}>Switch provider…</button>
    {open ? <Suspense fallback={<span role="status">Loading provider switch…</span>}>
      <HandoffDialog session={session} serverId={serverId} onOpen={onOpen} onClose={() => setOpen(false)} />
    </Suspense> : null}
  </>;
}

import { PageHeader, Status } from './ui/Layout';
import { lazy, Suspense, useEffect, useState } from 'react';
import { fetchProfiles, type AccountProfile } from '../api/sessionsd';
import { Button } from './ui/Button';
import { AccountsPanel } from './AccountsPanel';

const FleetView = lazy(() => import('./FleetView').then((module) => ({ default: module.FleetView })));

// Accounts is its own destination: the Claude and ChatGPT subscriptions each
// computer can run chats on. It reads the selected computer's list itself so
// opening it does not depend on Settings having loaded first.

export function AccountsView({ hostName, serverId, initialTab = 'accounts', onOpenSession = () => {}, onOpenMachine = () => {} }: {
  hostName: string; serverId?: string; initialTab?: 'accounts' | 'computers';
  onOpenSession?: (serverId: string, sessionId: string) => void;
  onOpenMachine?: (serverId: string) => void;
}): JSX.Element {
  const [tab, setTab] = useState(initialTab);
  const [profiles, setProfiles] = useState<AccountProfile[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setProfiles(null);
    setError(null);
    void fetchProfiles(controller.signal, serverId)
      .then((list) => { if (!controller.signal.aborted) setProfiles(list); })
      .catch((reason) => {
        if (controller.signal.aborted) return;
        setError(reason instanceof Error ? reason.message : `${hostName} did not answer.`);
        setProfiles([]);
      });
    return () => controller.abort();
  }, [serverId, hostName]);

  return (
    <div className="accounts-view account-workspace">
      <header className="account-workspace-heading">
        <PageHeader title="Accounts & computers" description="Your subscriptions and the computers you work on." />
        <div className="account-workspace-tabs" role="group" aria-label="Account workspace">
          <Button variant="ghost" aria-pressed={tab === 'accounts'} onClick={() => setTab('accounts')}>Accounts</Button>
          <Button variant="ghost" aria-pressed={tab === 'computers'} onClick={() => setTab('computers')}>Computers</Button>
        </div>
      </header>
      {tab === 'computers' ? <Suspense fallback={<Status>Reading computers…</Status>}><FleetView onOpenSession={onOpenSession} onOpenMachine={onOpenMachine} /></Suspense> : <div>
      {profiles === null ? (
        <section className="settings-page accounts-panel"><PageHeader title="Accounts" description={`Sign-ins on ${hostName}`} /><Status>Reading accounts…</Status></section>
      ) : (
        <>
          {error ? <p className="settings-page settings-message" role="status">Could not read the accounts on {hostName}: {error}</p> : null}
          <AccountsPanel embedded profiles={profiles} machineName={hostName} serverId={serverId} onReload={setProfiles} />
        </>
      )}
      </div>}
    </div>
  );
}

import { useState, type FormEvent } from 'react';
import { useServers } from '../lib/servers';
import { Input } from './ui/Field';
import type { MachineAccounts } from '../lib/accountRollup';
import { Button } from './ui/Button';
import { Section } from './ui/Layout';

/** A compact index of the same computers used for account grouping; no second fetch. */
export function ComputerRoster({ machines, onOpenMachine, onOpenConnections }: {
  machines: MachineAccounts[];
  onOpenMachine?: (id: string) => void;
  onOpenConnections?: () => void;
}): JSX.Element {
  return <div className="accounts-roster"><Section title="Computers" actions={onOpenConnections
    ? <Button variant="ghost" onClick={onOpenConnections}>Connections & discovery</Button> : undefined}>
    <ul aria-label="Computers" className="computer-roster">
      {machines.map((machine) => <li key={machine.serverId}>
        <div className="computer-roster-identity">
          <strong>{machine.machineName}</strong>
          <span className="computer-roster-evidence" data-answered={!machine.profilesError && machine.profiles !== null}>
            {machine.profilesError ? 'Accounts unavailable' : machine.profiles === null ? 'Reading accounts…'
              : `${machine.profiles.length} ${machine.profiles.length === 1 ? 'profile' : 'profiles'} · Accounts read`}
          </span>
          <ComputerDetails machine={machine} />
          {machine.usage === null ? <span>Usage needs a newer Sessions runtime</span> : null}
        </div>
        {onOpenMachine ? <Button variant="ghost" aria-label={`Open sessions on ${machine.machineName}`} onClick={() => onOpenMachine(machine.serverId)}>Open sessions <span aria-hidden="true">↗</span></Button> : null}
      </li>)}
    </ul>
  </Section></div>;
}


function ComputerDetails({ machine }: { machine: MachineAccounts }): JSX.Element {
  const configured = useServers((state) => state.servers.some((server) => server.id === machine.serverId));
  const [name, setName] = useState(machine.machineName);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const save = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const label = name.trim().replace(/\s+/g, ' ').slice(0, 48);
    if (!label) { setMessage('Add a name for this computer.'); return; }
    setBusy(true); setMessage(null);
    try {
      await useServers.getState().updateServer(machine.serverId, { name: label, customName: label });
      setMessage('Computer name saved.');
    } catch (reason) {
      setMessage(reason instanceof Error ? reason.message : 'Could not save this computer name.');
    } finally { setBusy(false); }
  };
  return <details className="computer-roster-details"><summary aria-label={`Details for ${machine.machineName}`}>Details</summary>
    {machine.profilesError ? <><p>{machine.profilesError}</p><p>This read does not establish whether the computer is offline. Check Connections to retry its connection.</p></> : null}
    {configured ? <form onSubmit={(event) => void save(event)}>
      <Input aria-label={`Name for ${machine.machineName}`} value={name} maxLength={48} onChange={(event) => setName(event.target.value)} />
      <Button type="submit" variant="ghost" disabled={busy}>{busy ? 'Saving…' : 'Save name'}</Button>
    </form> : null}
    {message ? <p role="status">{message}</p> : null}
  </details>;
}

import { Surface, Section, Status } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { useServers } from '../lib/servers';
import { fetchAutoArchive, updateAutoArchive, type AutoArchiveStatus } from '../api/sessionsd';

export function AutoArchiveCard({ hostName }: { hostName: string }): JSX.Element {
  const activeId = useServers((state) => state.activeId);
  return <MachineAutoArchiveCard key={activeId} hostName={hostName} />;
}

function MachineAutoArchiveCard({ hostName }: { hostName: string }): JSX.Element {
  const mounted = useRef(true);
  const [status, setStatus] = useState<AutoArchiveStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    void fetchAutoArchive(controller.signal).then((value) => {
      if (!controller.signal.aborted) setStatus(value);
    }).catch((error) => {
      if (!controller.signal.aborted) setMessage(error instanceof Error ? error.message : 'Could not read automatic archive. Reconnect to this computer and try again.');
    });
    return () => { mounted.current = false; controller.abort(); };
  }, []);
  const save = async (enabled: boolean): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try { const saved = await updateAutoArchive(enabled); if (mounted.current) setStatus(saved); }
    catch (error) { if (mounted.current) setMessage(error instanceof Error ? error.message : 'Could not save automatic archive.'); }
    finally { if (mounted.current) setBusy(false); }
  };
  return <Surface><Section title="Auto archive">
    <p>Keep the active list small on {hostName}. After 24 hours of observed inactivity, eligible structured sessions move to Archived. This does not mean your task is complete.</p>
    <label className="settings-toggle">
      <span><strong>Automatically set inactive sessions aside</strong>
        <small>Off by default. Pins, running work, attention and unknown states stay visible. Activity brings relevant work back.</small></span>
      <input type="checkbox" aria-label="Automatically set inactive sessions aside" checked={status?.enabled ?? false}
        disabled={busy || status === null} onChange={(event) => void save(event.currentTarget.checked)} />
    </label>
    <p>Runners keep running and drafts stay saved. Drafts and unsent queues on other clients are not observed. Restore from Archived at any time. Restarting the service starts a fresh 24-hour window.</p>
    {message ? <Status>{message}</Status> : null}
  </Section></Surface>;
}

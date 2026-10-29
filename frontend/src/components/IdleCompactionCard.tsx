import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { fetchIdleCompaction, updateIdleCompaction, type IdleCompactionStatus } from '../api/sessionsd';

interface Props {
  clientOnly: boolean;
  hostName: string;
}

function plannedSummary(status: IdleCompactionStatus): string | null {
  if (status.error) return `Paused: ${status.error}`;
  const planned = status.lanes.filter((lane) => lane.next);
  if (planned.length === 0) return null;
  const next = Math.min(...planned.map((lane) => lane.next!.fireAtMs));
  const time = new Date(next).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  return `${planned.length} idle ${planned.length === 1 ? 'lane' : 'lanes'} planned; next /compact at ${time}.`;
}

// The machine-wide idle compaction default. Each lane's own choice is set with
// `sessions compaction on|off|default --lane SESSION`.
export function IdleCompactionCard({ clientOnly, hostName }: Props): JSX.Element {
  const [status, setStatus] = useState<IdleCompactionStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const current = generation.current + 1;
    generation.current = current;
    void fetchIdleCompaction(controller.signal)
      .then((value) => {
        if (generation.current === current) setStatus(value);
      })
      .catch(() => {
        if (!controller.signal.aborted && generation.current === current) {
          setMessage('Idle compaction requires a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, []);

  const save = async (enabled: boolean): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try {
      setStatus(await updateIdleCompaction(enabled));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save the idle compaction setting.');
    } finally {
      setBusy(false);
    }
  };

  const summary = status ? plannedSummary(status) : null;
  return (
    <Surface className="settings-card settings-status-card">
      <PageHeader level={2} title="Compact idle Claude sessions" />
      <p>
        Claude’s prompt cache lasts about an hour on a subscription and five minutes with an API key. When this is on,
        Sessions sends <code>/compact</code> to an idle Claude session of at least 100,000 tokens two minutes before its
        cache expires, so coming back reads a summary instead of the whole conversation uncached.
      </p>
      {clientOnly ? <div className="settings-host-choice">Chosen on {hostName}</div> : null}
      <label className="settings-toggle">
        <span>
          <strong>Compact before the cache expires (beta)</strong>
          <small>The compaction appears in the conversation as a /compact turn from Idle compaction. Delegated child sessions are never compacted.</small>
        </span>
        <input
          type="checkbox"
          checked={status?.enabled ?? false}
          disabled={clientOnly || busy || status === null}
          onChange={(event) => void save(event.currentTarget.checked)}
        />
      </label>
      {summary ? <div className="settings-message" role="status">{summary}</div> : null}
      {message ? <div className="settings-message" role="status">{message}</div> : null}
    </Surface>
  );
}

import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { fetchRestartContinuation, updateRestartContinuation, type RestartContinuationStatus } from '../api/sessionsd';
import { LimitRecoveryCard } from './LimitRecoveryCard';

interface Props {
  clientOnly: boolean;
  hostName: string;
  showHostChoice?: boolean;
}

function recentSummary(status: RestartContinuationStatus): string | null {
  if (status.error) return `Paused: ${status.error}`;
  const continued = status.lanes.filter((lane) => lane.last?.state === 'sent' || lane.last?.state === 'unconfirmed');
  const waiting = status.lanes.filter((lane) => lane.last && ['pending', 'reopening', 'sending'].includes(lane.last.state));
  const parts: string[] = [];
  if (waiting.length > 0) parts.push(`${waiting.length} ${waiting.length === 1 ? 'session is' : 'sessions are'} being brought back now.`);
  if (continued.length > 0) parts.push(`${continued.length} ${continued.length === 1 ? 'session was' : 'sessions were'} continued after a restart.`);
  return parts.length > 0 ? parts.join(' ') : null;
}

// The machine-wide default for carrying on a session after a reboot, update,
// or crash. Each session's own choice is
// `sessions continuation on|off|default --lane SESSION`.
export function RestartContinuationCard({ clientOnly, hostName, showHostChoice = true }: Props): JSX.Element {
  const [status, setStatus] = useState<RestartContinuationStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const current = generation.current + 1;
    generation.current = current;
    void fetchRestartContinuation(controller.signal)
      .then((value) => {
        if (generation.current === current) setStatus(value);
      })
      .catch(() => {
        if (!controller.signal.aborted && generation.current === current) {
          setMessage('Continuing after a restart requires a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, []);

  const save = async (enabled: boolean): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try {
      setStatus(await updateRestartContinuation(enabled));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save the restart setting.');
    } finally {
      setBusy(false);
    }
  };

  const summary = status ? recentSummary(status) : null;
  return (
    <Surface className="settings-card settings-status-card">
      <PageHeader level={2} title="Continue after restart" />
      <p>
        When a reboot, a Sessions update, or a crash cuts a Rich Claude or Codex turn short, Sessions brings the
        session back and sends one short note asking it to carry on and check what was in progress. A session you
        ended, archived, set aside, or wrote to after the restart is left alone.
      </p>
      {clientOnly && showHostChoice ? <div className="settings-host-choice">Chosen on {hostName}</div> : null}
      <label className="settings-toggle">
        <span>
          <strong>Continue interrupted sessions</strong>
          <small>The note appears in the conversation from Restart continuation, not from you.</small>
        </span>
        <input
          type="checkbox"
          checked={status?.enabled ?? true}
          disabled={clientOnly || busy || status === null}
          onChange={(event) => void save(event.currentTarget.checked)}
        />
      </label>
      {summary ? <div className="settings-message" role="status">{summary}</div> : null}
      {message ? <div className="settings-message" role="status">{message}</div> : null}
    </Surface>
  );
}

// The two automatic recoveries sit together in Settings.
export function RecoveryCards(props: Props): JSX.Element {
  return (
    <>
      <LimitRecoveryCard {...props} />
      <RestartContinuationCard {...props} />
    </>
  );
}

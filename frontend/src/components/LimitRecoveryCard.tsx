import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { fetchLimitRecovery, updateLimitRecovery, type LimitRecoveryStatus } from '../api/sessionsd';
import { returnTimeLabel } from '../lib/returnTime';

interface Props {
  clientOnly: boolean;
  hostName: string;
  showHostChoice?: boolean;
}

function plannedSummary(status: LimitRecoveryStatus): string | null {
  if (status.error) return `Paused: ${status.error}`;
  const planned = status.lanes.filter((lane) => lane.next);
  if (planned.length === 0) return null;
  const next = Math.min(...planned.map((lane) => lane.next!.fireAtMs));
  return `${planned.length} ${planned.length === 1 ? 'session waits' : 'sessions wait'} for a usage limit; the next resumes at ${returnTimeLabel(next)}.`;
}

// The machine-wide default for resuming after a usage limit. Each session's
// own choice is `sessions limits on|off|default --lane SESSION`, or
// "Don't resume" on the session's notice.
export function LimitRecoveryCard({ clientOnly, hostName, showHostChoice = true }: Props): JSX.Element {
  const [status, setStatus] = useState<LimitRecoveryStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const current = generation.current + 1;
    generation.current = current;
    void fetchLimitRecovery(controller.signal)
      .then((value) => {
        if (generation.current === current) setStatus(value);
      })
      .catch(() => {
        if (!controller.signal.aborted && generation.current === current) {
          setMessage('Resuming after usage limits requires a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, []);

  const save = async (enabled: boolean): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try {
      setStatus(await updateLimitRecovery(enabled));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save the usage limit setting.');
    } finally {
      setBusy(false);
    }
  };

  const summary = status ? plannedSummary(status) : null;
  return (
    <Surface className="settings-card settings-status-card">
      <PageHeader level={2} title="Resume after usage limits" />
      <p>
        When a Claude or Codex session stops on its plan’s usage limit, Sessions waits for the limit to reset and then
        sends <q>Continue where you left off.</q> once. Sending the session anything cancels the plan.
      </p>
      {clientOnly && showHostChoice ? <div className="settings-host-choice">Chosen on {hostName}</div> : null}
      <label className="settings-toggle">
        <span>
          <strong>Resume when the limit resets</strong>
          <small>The message appears in the conversation from Limit recovery, not from you.</small>
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

import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { fetchUsageCap, updateUsageCap, type UsageCapChange, type UsageCapStatus } from '../api/sessionsd';
import { returnTimeLabel } from '../lib/returnTime';

interface Props {
  clientOnly: boolean;
  hostName: string;
  showHostChoice?: boolean;
}

function accountName(account: UsageCapStatus['accounts'][number]): string {
  if (account.label) return account.label;
  return account.name ? `${account.tool}/${account.name}` : `${account.tool} default`;
}

// One calm line: which accounts are over the cap and which sessions wait.
export function usageCapSummary(status: UsageCapStatus): string | null {
  if (status.error) return status.error;
  const over = status.accounts.filter((account) => account.state === 'over');
  const waiting = status.lanes.filter((lane) => lane.hold?.state === 'waiting').length;
  if (over.length === 0) return null;
  const accounts = over.map((account) => {
    const reset = account.resetsAt ? `, resets ${returnTimeLabel(account.resetsAt)}` : '';
    return `${accountName(account)} at ${account.usedPercent}% of ${account.window ?? 'its limit'}${reset}`;
  }).join('; ');
  const sessions = waiting === 0 ? '' : ` ${waiting} ${waiting === 1 ? 'session waits' : 'sessions wait'} for usage.`;
  return `Over the cap: ${accounts}.${sessions}`;
}

// The machine-wide usage cap and account switching. The CLI equivalent is
// `sessions cap`; each session's own choice is `sessions cap on|off|default --lane`.
export function UsageCapCard({ clientOnly, hostName, showHostChoice = true }: Props): JSX.Element {
  const [status, setStatus] = useState<UsageCapStatus | null>(null);
  const [draft, setDraft] = useState<string>('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const current = generation.current + 1;
    generation.current = current;
    void fetchUsageCap(controller.signal)
      .then((value) => {
        if (generation.current !== current) return;
        setStatus(value);
        setDraft(String(value.percent));
      })
      .catch(() => {
        if (!controller.signal.aborted && generation.current === current) {
          setMessage('The usage cap requires a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, []);

  const save = async (change: UsageCapChange): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try {
      const next = await updateUsageCap(change);
      setStatus(next);
      setDraft(String(next.percent));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save the usage cap.');
    } finally {
      setBusy(false);
    }
  };

  const commitPercent = (): void => {
    if (!status) return;
    const percent = Number.parseInt(draft, 10);
    if (!Number.isFinite(percent) || percent < 1 || percent > 100) {
      setDraft(String(status.percent));
      setMessage('Choose a cap from 1% to 100%.');
      return;
    }
    if (percent !== status.percent) void save({ percent });
  };

  const disabled = clientOnly || busy || status === null;
  const summary = status ? usageCapSummary(status) : null;
  return (
    <Surface className="settings-card settings-status-card usage-cap-card">
      <PageHeader level={2} title="Usage cap" />
      <p>
        When an account has used this much of any usage window (the 5-hour session, weekly, or a model’s weekly limit),
        Sessions stops starting new turns on it. A turn already running always finishes.
      </p>
      {clientOnly && showHostChoice ? <div className="settings-host-choice">Chosen on {hostName}</div> : null}
      <label className="settings-toggle">
        <span>
          <strong>Hold new turns at the cap</strong>
          <small>Messages to a capped account are not sent and say when it resets. A reading Sessions can’t confirm never holds anything.</small>
        </span>
        <input type="checkbox" checked={status?.enabled ?? true} disabled={disabled} onChange={(event) => void save({ enabled: event.currentTarget.checked })} />
      </label>
      <label className="settings-toggle">
        <span>
          <strong>Cap</strong>
          <small>Percent of any window. 100% waits only for the provider’s own limit.</small>
        </span>
        <span className="usage-cap-percent">
          <input
            type="number"
            min={1}
            max={100}
            inputMode="numeric"
            aria-label="Usage cap percent"
            value={draft}
            disabled={disabled || !status?.enabled}
            onChange={(event) => setDraft(event.currentTarget.value)}
            onBlur={commitPercent}
            onKeyDown={(event) => { if (event.key === 'Enter') event.currentTarget.blur(); }}
          />
          <span aria-hidden="true">%</span>
        </span>
      </label>
      <label className="settings-toggle">
        <span>
          <strong>Switch to another account</strong>
          <small>New sessions start on the signed-in account with the most headroom, and a session stopped by a limit continues there with its conversation. Credentials are never copied.</small>
        </span>
        <input type="checkbox" checked={status?.switch ?? true} disabled={disabled} onChange={(event) => void save({ switch: event.currentTarget.checked })} />
      </label>
      {summary ? <div className="settings-message" role="status">{summary}</div> : null}
      {message ? <div className="settings-message" role="status">{message}</div> : null}
    </Surface>
  );
}

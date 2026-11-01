import { useEffect, useId, useState } from 'react';
import { automationErrorText, fetchAutomationRuns, type AutomationRun } from '../api/sessionsd/automations';
import { automationClock, outcomeLabel } from '../lib/automations';
import { Button } from './ui/Button';
import { ModalDialog } from './ui/ModalDialog';

interface Props {
  onClose: () => void;
  onOpenSession: (sessionId: string) => void;
}

type RunFilter = 'all' | 'attention';

/** Previous agents: every automation run, newest first, linked to its session. */
export function AutomationRunsDialog({ onClose, onOpenSession }: Props): JSX.Element {
  const titleId = useId();
  const [runs, setRuns] = useState<AutomationRun[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<RunFilter>('all');
  useEffect(() => {
    const controller = new AbortController();
    void fetchAutomationRuns(undefined, controller.signal)
      .then((result) => { if (!controller.signal.aborted) setRuns(result.runs); })
      .catch((reason) => { if (!controller.signal.aborted) setError(automationErrorText(reason, 'Could not read previous agents.')); });
    return () => controller.abort();
  }, []);
  const visible = (runs ?? []).filter((run) => filter === 'all'
    || run.outcome === 'failed' || run.outcome === 'unknown' || run.outcome === 'needs-input');
  return <ModalDialog className="dialog automation-runs" labelledBy={titleId} onClose={onClose}>
    <header className="automation-editor-head">
      <h2 id={titleId}>Previous agents</h2>
      <p>Each run of an automation is its own session. Skipped runs started nothing because the previous run was still open.</p>
    </header>
    <div className="automation-runs-filter" role="group" aria-label="Show">
      <button type="button" aria-pressed={filter === 'all'} onClick={() => setFilter('all')}>All</button>
      <button type="button" aria-pressed={filter === 'attention'} onClick={() => setFilter('attention')}>Needs a look</button>
    </div>
    {error ? <p className="ui-error" role="alert">{error}</p> : null}
    {runs === null && !error ? <p className="automations-message">Loading…</p> : null}
    {runs !== null && visible.length === 0 ? <p className="automations-message">No runs here yet.</p> : null}
    <ol className="automation-runs-list">
      {visible.map((run) => <AutomationRunRow key={run.id} run={run} onOpenSession={onOpenSession} />)}
    </ol>
    <footer className="automation-editor-actions"><span /><Button variant="ghost" onClick={onClose}>Close</Button></footer>
  </ModalDialog>;
}

function AutomationRunRow({ run, onOpenSession }: { run: AutomationRun; onOpenSession: (sessionId: string) => void }): JSX.Element {
  const sessionId = run.sessionId;
  return <li className={`automation-run is-${run.outcome}`}>
    <span className={`automation-run-outcome is-${run.outcome}`}>{outcomeLabel(run.outcome)}</span>
    <span className="automation-run-copy">
      <strong>{run.automationName}</strong>
      <small>{automationClock(run.startedAtMs)}{run.trigger === 'manual' ? ' · run now' : ''}{run.detail ? ` · ${run.detail}` : ''}</small>
    </span>
    {sessionId ? <button type="button" className="automation-run-open" onClick={() => onOpenSession(sessionId)}>Open session</button> : null}
  </li>;
}

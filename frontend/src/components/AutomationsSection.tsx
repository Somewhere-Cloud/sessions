import { useState } from 'react';
import './Automations.css';
import {
  automationErrorText,
  runAutomationNow,
  setAutomationEnabled,
  type AutomationView
} from '../api/sessionsd/automations';
import { automationSubline, automationTone } from '../lib/automations';
import { useAutomations } from '../hooks/useAutomations';
import { AutomationEditor } from './AutomationEditor';
import { AutomationRunsDialog } from './AutomationRunsDialog';

interface Props {
  // The folder a new automation starts in, usually the open session's.
  defaultCwd?: string;
  onOpenSession: (sessionId: string) => void;
}

type EditorTarget = { automation: AutomationView | null } | null;

/**
 * The sidebar's Automations section: one compact row per scheduled agent and
 * a "Previous agents" row that opens their run history. With no automations
 * it is a single quiet "New automation" link.
 */
export default function AutomationsSection({ defaultCwd = '', onOpenSession }: Props): JSX.Element | null {
  const { listing, unavailable, refresh } = useAutomations();
  const [editor, setEditor] = useState<EditorTarget>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  if (!listing && unavailable) return null;
  if (!listing) return null;
  const automations = listing.automations;
  const act = async (id: string, action: () => Promise<unknown>, fallback: string): Promise<void> => {
    setBusyId(id);
    setMessage(null);
    try { await action(); } catch (error) { setMessage(automationErrorText(error, fallback)); }
    finally { setBusyId(null); refresh(); }
  };
  const dialogs = <>
    {editor ? <AutomationEditor automation={editor.automation} defaultCwd={defaultCwd}
      onClose={() => setEditor(null)} onSaved={() => { setEditor(null); refresh(); }} /> : null}
    {historyOpen ? <AutomationRunsDialog onClose={() => { setHistoryOpen(false); refresh(); }}
      onOpenSession={(id) => { setHistoryOpen(false); onOpenSession(id); }} /> : null}
  </>;
  if (automations.length === 0) {
    return <div className="automations-empty">
      <button type="button" className="automations-new-link" onClick={() => setEditor({ automation: null })}>
        <span aria-hidden>+</span> New automation
      </button>
      {dialogs}
    </div>;
  }
  return <section className="automations-panel" aria-label="Automations">
    <header className="automations-head">
      <span>Automations</span>
      <button type="button" className="automations-icon-button" aria-label="New automation" title="New automation"
        onClick={() => setEditor({ automation: null })}><PlusIcon /></button>
    </header>
    {listing.error ? <p className="automations-message" role="status">{listing.error}</p> : null}
    <ul className="automations-list">
      {automations.map((automation) => (
        <AutomationRow key={automation.id} automation={automation} busy={busyId === automation.id}
          onEdit={() => setEditor({ automation })}
          onRun={() => void act(automation.id, () => runAutomationNow(automation.id), 'Could not start the run.')}
          onToggle={(enabled) => void act(automation.id, () => setAutomationEnabled(automation.id, enabled), 'Could not save the change.')} />
      ))}
    </ul>
    {message ? <p className="automations-message" role="alert">{message}</p> : null}
    <PreviousAgentsRow total={listing.runs.total} failed={listing.runs.recentFailed} needsInput={listing.runs.needsInput}
      onOpen={() => setHistoryOpen(true)} />
    {dialogs}
  </section>;
}

function AutomationRow({ automation, busy, onEdit, onRun, onToggle }: {
  automation: AutomationView;
  busy: boolean;
  onEdit: () => void;
  onRun: () => void;
  onToggle: (enabled: boolean) => void;
}): JSX.Element {
  const tone = automationTone(automation);
  const subline = automationSubline(automation);
  return <li className={`automation-row${automation.enabled ? '' : ' is-paused'}`}>
    <span className="automation-icon" aria-hidden><CalendarIcon /><span className={`automation-dot is-${tone}`} /></span>
    <button type="button" className="automation-main" onClick={onEdit} title={`${automation.name} — ${subline}`}>
      <strong>{automation.name}</strong>
      <small>{subline}</small>
    </button>
    <span className="automation-controls">
      <button type="button" className="automations-icon-button" aria-label={`Edit ${automation.name}`} title="Edit" onClick={onEdit}><PencilIcon /></button>
      <button type="button" className="automations-icon-button" aria-label={`Run ${automation.name} now`} title="Run now" disabled={busy} onClick={onRun}><PlayIcon /></button>
      <label className="automation-switch" title={automation.enabled ? 'On' : 'Paused'}>
        <input type="checkbox" role="switch" aria-label={`${automation.name} on schedule`} checked={automation.enabled}
          disabled={busy} onChange={(event) => onToggle(event.currentTarget.checked)} />
        <span aria-hidden />
      </label>
    </span>
  </li>;
}

function PreviousAgentsRow({ total, failed, needsInput, onOpen }: { total: number; failed: number; needsInput: number; onOpen: () => void }): JSX.Element {
  return <button type="button" className="automations-previous" onClick={onOpen} aria-label={`Previous agents: ${total}${failed ? `, ${failed} failed` : ''}`}>
    <span>Previous agents</span>
    {needsInput > 0 ? <span className="automations-badge is-attention">{needsInput} need you</span> : null}
    {failed > 0 ? <span className="automations-badge is-failed">{failed} failed</span> : null}
    <span className="automations-count">{total}</span>
    <ChevronIcon />
  </button>;
}

function Icon({ children }: { children: React.ReactNode }): JSX.Element {
  return <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>{children}</svg>;
}
function CalendarIcon(): JSX.Element { return <Icon><rect x="4" y="5" width="16" height="15" rx="2" /><path d="M8 3v4M16 3v4M4 10h16" /></Icon>; }
function PencilIcon(): JSX.Element { return <Icon><path d="M4 20h4L19 9l-4-4L4 16v4Z" /><path d="m13.5 6.5 4 4" /></Icon>; }
function PlayIcon(): JSX.Element { return <Icon><path d="M8 5.5v13l10-6.5-10-6.5Z" /></Icon>; }
function PlusIcon(): JSX.Element { return <Icon><path d="M12 5v14M5 12h14" /></Icon>; }
function ChevronIcon(): JSX.Element { return <Icon><path d="m9 6 6 6-6 6" /></Icon>; }

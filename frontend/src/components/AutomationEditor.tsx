import { useId, useState } from 'react';
import {
  automationErrorText,
  createAutomation,
  deleteAutomation,
  updateAutomation,
  type AutomationAccess,
  type AutomationAgent,
  type AutomationView
} from '../api/sessionsd/automations';
import { automationDraft, draftSpec, type AutomationDraft } from '../lib/automations';
import { NO_PROJECT_CWD, startFolder } from '../lib/projectFolders';
import { getActiveServer, serverDisplayName, useServers } from '../lib/servers';
import { DirectoryBrowser } from './DirectoryBrowser';
import { LauncherProjectChoices } from './LauncherProjectChoices';
import { CLAUDE_MODEL_OPTIONS } from './ModelPicker';
import { Button } from './ui/Button';
import { Field, Input, Select, Textarea } from './ui/Field';
import { ModalDialog } from './ui/ModalDialog';

interface Props {
  automation: AutomationView | null;
  defaultCwd: string;
  onClose: () => void;
  onSaved: () => void;
}

type SetDraft = <K extends keyof AutomationDraft>(key: K, value: AutomationDraft[K]) => void;

/** Create or edit one automation on the active computer. */
export function AutomationEditor({ automation, defaultCwd, onClose, onSaved }: Props): JSX.Element {
  const titleId = useId();
  const serverId = useServers((state) => state.activeId) ?? '';
  const [draft, setDraftState] = useState<AutomationDraft>(() => automationDraft(automation ?? undefined, defaultCwd));
  const [busy, setBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const setDraft: SetDraft = (key, value) => setDraftState((current) => ({ ...current, [key]: value }));
  const run = async (action: () => Promise<unknown>, fallback: string): Promise<void> => {
    setBusy(true);
    setError(null);
    try { await action(); onSaved(); } catch (reason) { setError(automationErrorText(reason, fallback)); } finally { setBusy(false); }
  };
  const save = (): void => {
    const pending = draftSpec(draft, draft.cwd);
    if (typeof pending === 'string') { setError(pending); return; }
    void run(async () => {
      const cwd = await startFolder(draft.cwd, draft.name, serverId, (path) => setDraft('cwd', path));
      const spec = { ...pending, cwd };
      if (automation) await updateAutomation(automation.id, { ...spec, profile: automation.profile, effort: automation.effort });
      else await createAutomation(spec);
    }, 'Could not save the automation.');
  };
  const remove = (): void => {
    if (!automation) return;
    if (!confirmRemove) { setConfirmRemove(true); return; }
    void run(() => deleteAutomation(automation.id), 'Could not remove the automation.');
  };
  return <ModalDialog className="dialog automation-editor" labelledBy={titleId} onClose={onClose} dismissible={!busy}>
    <form className="automation-editor-form" onSubmit={(event) => { event.preventDefault(); save(); }}>
      <header className="automation-editor-head">
        <h2 id={titleId}>{automation ? 'Edit automation' : 'New automation'}</h2>
        <p>Each run starts a fresh agent on {serverDisplayName(getActiveServer(), true)} with this prompt. A run never starts while the previous one is still open.</p>
      </header>
      <Field label="Name"><Input value={draft.name} maxLength={120} placeholder="Nightly triage" onChange={(event) => setDraft('name', event.currentTarget.value)} /></Field>
      <Field label="Prompt" hint="The first request of every run.">
        <Textarea rows={5} value={draft.prompt} placeholder="Look at new issues, label them, and summarize what needs me." onChange={(event) => setDraft('prompt', event.currentTarget.value)} />
      </Field>
      <AutomationFolderField serverId={serverId} cwd={draft.cwd} onChange={(path) => setDraft('cwd', path)} />
      <AutomationAgentFields draft={draft} setDraft={setDraft} />
      <AutomationScheduleFields draft={draft} setDraft={setDraft} />
      {error ? <p className="ui-error" role="alert">{error}</p> : null}
      <footer className="automation-editor-actions">
        {automation ? <Button variant={confirmRemove ? 'danger' : 'ghost'} disabled={busy} onClick={remove}>
          {confirmRemove ? 'Remove automation and its history' : 'Remove'}
        </Button> : null}
        <span />
        <Button variant="ghost" disabled={busy} onClick={onClose}>Cancel</Button>
        <Button variant="primary" type="submit" loading={busy}>{automation ? 'Save' : 'Create automation'}</Button>
      </footer>
    </form>
  </ModalDialog>;
}

function AutomationFolderField({ serverId, cwd, onChange }: { serverId: string; cwd: string; onChange: (path: string) => void }): JSX.Element {
  const [open, setOpen] = useState(!cwd);
  const label = cwd === NO_PROJECT_CWD ? 'No project (a scratch folder is made)' : cwd || 'Choose a project folder';
  return <div className="automation-folder">
    <span className="field-label">Project folder</span>
    <button type="button" className="launcher-intent-control is-workspace" aria-expanded={open} onClick={() => setOpen((value) => !value)} title={label}>
      <span className="workspace-folder-icon" aria-hidden /><strong>{label}</strong><span className="launcher-chip-caret" aria-hidden>⌄</span>
    </button>
    {open ? <div className="launcher-workspace-picker">
      <LauncherProjectChoices serverId={serverId} cwd={cwd} onChoose={(path) => { onChange(path); setOpen(false); }} />
      <div className="launcher-directory-picker">
        <DirectoryBrowser serverId={serverId} value={cwd === NO_PROJECT_CWD ? '' : cwd} onChange={(path, confirmed) => { onChange(path); if (confirmed) setOpen(false); }} />
      </div>
    </div> : null}
  </div>;
}

function AutomationAgentFields({ draft, setDraft }: { draft: AutomationDraft; setDraft: SetDraft }): JSX.Element {
  return <div className="automation-editor-row">
    <Field label="Agent">
      <Select value={draft.agent} onChange={(event) => { setDraft('agent', event.currentTarget.value as AutomationAgent); setDraft('model', ''); }}>
        <option value="claude">Claude</option>
        <option value="codex">Codex</option>
      </Select>
    </Field>
    <Field label="Access">
      <Select value={draft.access} onChange={(event) => setDraft('access', event.currentTarget.value as AutomationAccess)}>
        <option value="full">Full access</option>
        <option value="ask">Ask me</option>
        <option value="plan">Plan</option>
      </Select>
    </Field>
    <Field label="Model">
      {draft.agent === 'claude' ? <Select value={draft.model} onChange={(event) => setDraft('model', event.currentTarget.value)}>
        <option value="">Use Settings</option>
        {CLAUDE_MODEL_OPTIONS.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
      </Select> : <Input value={draft.model} placeholder="Codex default" onChange={(event) => setDraft('model', event.currentTarget.value)} />}
    </Field>
  </div>;
}

function AutomationScheduleFields({ draft, setDraft }: { draft: AutomationDraft; setDraft: SetDraft }): JSX.Element {
  return <div className="automation-editor-row">
    <Field label="Runs">
      <Select value={draft.scheduleKind} onChange={(event) => setDraft('scheduleKind', event.currentTarget.value as AutomationDraft['scheduleKind'])}>
        <option value="interval">Every…</option>
        <option value="daily">Daily at…</option>
      </Select>
    </Field>
    {draft.scheduleKind === 'interval' ? <>
      <Field label="Interval"><Input type="number" min={1} value={draft.every} onChange={(event) => setDraft('every', event.currentTarget.value)} /></Field>
      <Field label="Unit">
        <Select value={draft.unit} onChange={(event) => setDraft('unit', event.currentTarget.value as AutomationDraft['unit'])}>
          <option value="minutes">minutes</option>
          <option value="hours">hours</option>
        </Select>
      </Field>
    </> : <Field label="Time"><Input type="time" value={draft.at} onChange={(event) => setDraft('at', event.currentTarget.value)} /></Field>}
  </div>;
}

import { useEffect, useState } from 'react';
import { PROJECT_DEFAULT_KEYS, updateProjectDefaults, type ProjectDefaultKey, type ProjectDefaults } from '../api/sessionsd';
import type { ProjectLaunch } from '../hooks/useProjectDefaults';
import { Button } from './ui/Button';
import { Input, Select } from './ui/Field';

const LABELS: Record<ProjectDefaultKey, string> = { provider: 'Agent', model: 'Model', effort: 'Effort', worktree: 'Worktree', permissions: 'Access' };

function describe(key: ProjectDefaultKey, value: string): string {
  if (key === 'provider') return value === 'codex' ? 'Codex' : 'Claude Code';
  if (key === 'worktree') return value === 'on' ? 'new worktree' : 'shared folder';
  if (key === 'permissions') return value === 'ask' ? 'Ask me' : 'Full access';
  if (key === 'effort') return `${value} effort`;
  return value;
}

// ProjectDefaultsNote is the launcher's confirm step for defaults: which ones
// this start took from the project, and the one launch choice with no other
// control, the worktree.
export function ProjectDefaultsNote({ launch }: { launch: ProjectLaunch }): JSX.Element | null {
  const { answer, isolated: worktree, setIsolated: setWorktree } = launch;
  const applied = answer ? PROJECT_DEFAULT_KEYS.filter((key) => answer.defaults[key]) : [];
  if (!answer || (applied.length === 0 && !worktree)) return null;
  return <div className="launcher-project-defaults-note" role="status">
    {applied.length ? <span>From {answer.project.name}’s defaults: {applied.map((key) => describe(key, answer.defaults[key] as string)).join(' · ')}</span> : null}
    <label className="field-checkbox"><input type="checkbox" checked={worktree} onChange={(event) => setWorktree(event.currentTarget.checked)} /><span>Start in a new worktree</span></label>
  </div>;
}

// ProjectDefaultsEditor is "Defaults for this project": the same store as
// `sessions projects defaults <folder>`, on the computer chosen above.
export function ProjectDefaultsEditor({ launch, machineId, cwd, current }: {
  launch: ProjectLaunch; machineId: string; cwd: string; current: ProjectDefaults;
}): JSX.Element | null {
  const { answer, reload: onSaved } = launch;
  const [draft, setDraft] = useState<ProjectDefaults>({});
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  useEffect(() => { setDraft(answer?.defaults ?? {}); setMessage(null); }, [answer]);
  if (!answer) return null;
  const edit = (key: ProjectDefaultKey, value: string): void => setDraft((previous) => ({ ...previous, [key]: value || undefined }));
  const save = async (next: ProjectDefaults): Promise<void> => {
    setBusy(true); setMessage(null);
    try {
      const clear = PROJECT_DEFAULT_KEYS.filter((key) => answer.defaults[key] && !next[key]);
      await updateProjectDefaults(cwd, next, clear, machineId);
      setMessage('Saved for new sessions in this project on this computer.');
      onSaved();
    } catch (reason) {
      setMessage(reason instanceof Error ? reason.message : 'The defaults were not saved.');
    } finally { setBusy(false); }
  };
  const choice = (key: ProjectDefaultKey, options: Array<[string, string]>): JSX.Element => <label key={key}><span>{LABELS[key]}</span>
    <Select value={draft[key] ?? ''} aria-label={`Project default ${LABELS[key].toLowerCase()}`} onChange={(event) => edit(key, event.currentTarget.value)} disabled={busy}>
      <option value="">This computer’s default</option>{options.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
    </Select></label>;
  return <details className="launcher-advanced-subsection launcher-project-defaults">
    <summary>Defaults for this project <span>{answer.project.name}</span></summary>
    <div className="launcher-claude-options">
      {choice('provider', [['claude', 'Claude Code'], ['codex', 'Codex']])}
      <label><span>Model</span><Input value={draft.model ?? ''} aria-label="Project default model" placeholder="Provider default" onChange={(event) => edit('model', event.currentTarget.value.trim())} disabled={busy} /></label>
      {choice('effort', ['minimal', 'low', 'medium', 'high', 'xhigh', 'max'].map((value) => [value, value]))}
      {choice('worktree', [['on', 'New worktree'], ['off', 'Shared folder']])}
      {choice('permissions', [['bypass', 'Full access'], ['ask', 'Ask me']])}
    </div>
    <div className="launcher-project-defaults-actions">
      <Button size="compact" disabled={busy} onClick={() => void save(draft)}>Save defaults</Button>
      <Button size="compact" variant="ghost" disabled={busy} onClick={() => void save({ ...current, worktree: launch.isolated ? 'on' : 'off' })}>Use this launch’s choices</Button>
      {message ? <small role="status">{message}</small> : null}
    </div>
  </details>;
}

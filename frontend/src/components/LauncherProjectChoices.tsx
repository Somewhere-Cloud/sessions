import { useState, type KeyboardEvent } from 'react';
import { makeProjectFolder } from '../api/sessionsd';
import { NO_PROJECT_CWD, projectFolderName } from '../lib/projectFolders';
import { Input } from './ui/Field';
import { Button } from './ui/Button';

// LauncherProjectChoices offers the two ways to start without picking an
// existing folder: no project (a scratch folder made at start) or a new named
// project, made as a git repository under ~/Projects right away.
export function LauncherProjectChoices({ serverId, cwd, onChoose }: {
  serverId: string;
  cwd: string;
  onChoose: (path: string) => void;
}): JSX.Element {
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const folder = projectFolderName(name);

  const create = async (): Promise<void> => {
    if (!folder || busy) return;
    setBusy(true);
    setError(null);
    try {
      const made = await makeProjectFolder('new', name.trim(), serverId);
      setName('');
      setNaming(false);
      onChoose(made.path);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sessions could not make the project.');
    } finally {
      setBusy(false);
    }
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>): void => {
    if (event.key === 'Enter') { event.preventDefault(); void create(); }
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setNaming(false); }
  };

  return (
    <div className="launcher-project-choices">
      <div className="recent-workspaces">
        <button type="button" className={cwd === NO_PROJECT_CWD ? 'is-active' : ''} onClick={() => onChoose(NO_PROJECT_CWD)}>
          <span className="workspace-folder-icon is-scratch" aria-hidden />
          <span className="workspace-card-copy"><strong>No project</strong><small>A fresh scratch folder</small></span>
          <span className="workspace-radio" aria-hidden />
        </button>
        <button type="button" className={naming ? 'is-active' : ''} onClick={() => setNaming((open) => !open)} aria-expanded={naming}>
          <span className="workspace-folder-icon is-new" aria-hidden />
          <span className="workspace-card-copy"><strong>New project</strong><small>A new Git repository</small></span>
          <span className="workspace-radio" aria-hidden />
        </button>
      </div>
      {naming ? (
        <div className="launcher-new-project">
          <Input autoFocus value={name} onChange={(event) => setName(event.currentTarget.value)} onKeyDown={onKeyDown} placeholder="Project name" aria-label="New project name" maxLength={120} />
          <Button variant="primary" disabled={!folder || busy} loading={busy} onClick={() => void create()}>Create</Button>
          <small>{error ?? (folder ? `Creates ~/Projects/${folder}` : 'Use letters or numbers')}</small>
        </div>
      ) : null}
    </div>
  );
}

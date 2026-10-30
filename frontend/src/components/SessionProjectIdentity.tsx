import type { SessionInfo } from '../types';

/** Local, deterministic fallback. It never fetches a favicon or implies a brand asset. */
export function ProjectMark({ name }: { name: string }): JSX.Element {
  return <span className="project-mark" aria-hidden title="Project icon fallback">{Array.from(name.trim())[0]?.toLocaleUpperCase() || '◇'}</span>;
}

export function SessionWorkspaceBranch({ session }: { session: SessionInfo }): JSX.Element | null {
  const git = session.workspaceGit;
  if (!git || git.state === 'not-repository') return null;
  const label = git.state === 'branch' ? git.branch || 'Branch unavailable' : git.state === 'detached' ? git.revision ? `Detached ${git.revision.slice(0, 8)}` : 'Detached HEAD' : 'Branch unavailable';
  if (!label) return null;
  return <span className="session-workspace-branch" title={`${session.cwd}\n${label}\nObserved ${new Date(git.observedAt).toLocaleString()}; cached for up to 30 seconds`}>
    <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden><path d="M4 4v7m0-3c0-3 8 0 8-4" fill="none" stroke="currentColor" /><circle cx="4" cy="3" r="2" fill="none" stroke="currentColor" /><circle cx="4" cy="12" r="2" fill="none" stroke="currentColor" /><circle cx="12" cy="3" r="2" fill="none" stroke="currentColor" /></svg>
    <span>{label}</span>
  </span>;
}

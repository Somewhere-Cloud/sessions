import { projectMonogram } from '../lib/projectMonogram';
import type { SessionInfo } from '../types';

/** Images are host-observed local assets, never remote URLs or inline markup.
 *  Without one, the project's two-letter monogram shows on its own tint. */
export function ProjectMark({ name, icon }: { name: string; icon?: string }): JSX.Element {
  const monogram = projectMonogram(name);
  return <span className="project-mark" style={{ '--project-hue': monogram.hue } as React.CSSProperties} aria-hidden>
    <span className="project-mark-letters">{monogram.letters}</span>
    {icon?.startsWith('data:image/') ? <img key={icon} src={icon} alt="" onError={(event) => { event.currentTarget.hidden = true; }} /> : null}
  </span>;
}

export function SessionWorkspaceBranch({ session }: { session: SessionInfo }): JSX.Element | null {
  const git = session.workspaceGit;
  if (!git || git.state === 'not-repository') return null;
  const label = git.state === 'branch' ? git.branch || 'Branch unavailable' : git.state === 'detached' ? git.revision ? `Detached ${git.revision.slice(0, 8)}` : 'Detached HEAD' : 'Branch unavailable';
  if (!label) return null;
  return <span className="session-workspace-branch" title={`${session.cwd}\n${label}\nObserved ${new Date(git.observedAt).toLocaleString()}; refreshes in the background`}>
    <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden><path d="M4 4v7m0-3c0-3 8 0 8-4" fill="none" stroke="currentColor" /><circle cx="4" cy="3" r="2" fill="none" stroke="currentColor" /><circle cx="4" cy="12" r="2" fill="none" stroke="currentColor" /><circle cx="12" cy="3" r="2" fill="none" stroke="currentColor" /></svg>
    <span>{label}</span>
  </span>;
}

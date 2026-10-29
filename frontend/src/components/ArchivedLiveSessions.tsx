import { lazy, Suspense, useState, type ReactNode } from 'react';
import type { ProjectView } from '../api/sessionsd';
import type { FleetSessionSnapshot } from '../hooks/useFleetSessions';
import { groupAgents, type Agent } from '../lib/projectAgents';
import type { SessionInfo } from '../types';

const ProjectAgents = lazy(() => import('./ProjectAgents').then((module) => ({ default: module.ProjectAgents })));

interface Props {
  snapshots: FleetSessionSnapshot[];
  projects: Record<string, ProjectView[]>;
  matches: (session: SessionInfo, serverId: string) => boolean;
  activeMachineId: string | null;
  searching: boolean;
  renderLocal: (row: Agent) => ReactNode;
  onOpen: (serverId: string, sessionId: string) => void;
}

export function ArchivedLiveSessions({ snapshots, projects, matches, activeMachineId, searching, renderLocal, onOpen }: Props): JSX.Element | null {
  const [expanded, setExpanded] = useState(false);
  const open = expanded || searching;
  const groups = groupAgents(snapshots, projects, false, matches, 'archived');
  const count = groups.reduce((sum, group) => sum + group.rows.length, 0);
  if (!count) return null;
  return <section className="session-tree-group" aria-label="Set aside">
    <button type="button" className="session-tree-group-head" aria-expanded={open} onClick={() => setExpanded(!open)}>
      <span>Set aside</span><strong>{count}</strong>
    </button>
    {open ? <>
      <p className="session-tree-empty is-compact">Conversations set aside manually or by auto archive. Open one to pick up where you left off. Runners and drafts stay intact.</p>
      <Suspense fallback={<p role="status">Loading archived sessions…</p>}>
        <ProjectAgents organizeWorking={false} groups={groups} activeMachineId={activeMachineId} compact={false} renderLocal={renderLocal} onOpen={onOpen} />
      </Suspense>
    </> : null}
  </section>;
}

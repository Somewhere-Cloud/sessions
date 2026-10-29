import { useEffect, useState, type ReactNode } from 'react';
import type { SessionInfo } from '../types';
import { classifySession } from '../lib/sessionStatus';

export function isObservedWorking(session: SessionInfo): boolean {
  return classifySession(session).state === 'working';
}

// Prefer a provider-confirmed open turn; otherwise name the observation age.
export function WorkingObservation({ session }: { session: SessionInfo }): JSX.Element {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  const observations = session.observations;
  const turn = observations?.turn;
  const structured = observations?.capabilities.turn_boundaries === 'provider-stream';
  const started = structured && turn?.open ? turn.started_by === 'provider' ? turn.started_at : turn.provider_confirmed_at : null;
  const at = started || (!session.lastDataUnknown ? session.lastDataAt : null);
  const minutes = at ? Math.max(0, Math.floor((now - at) / 60_000)) : 0;
  const elapsed = minutes < 1 ? '<1m' : minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h`;
  const label = started ? `In progress ${elapsed}` : at ? `Active · observed ${minutes < 1 ? 'just now' : `${elapsed} ago`}` : 'Active · time unavailable';
  return <small className="session-working-observation" title={started ? 'Since the provider-confirmed turn start' : 'Age of the latest observed activity; turn start unavailable'}>{label}</small>;
}

/** One cross-project section. Callers remove these rows from project groups. */
export function SessionActivityGroups<T>({ items, sessionOf, renderItem }: {
  items: T[];
  sessionOf: (item: T) => SessionInfo;
  renderItem: (item: T) => ReactNode;
}): JSX.Element {
  const working = items.filter((item) => isObservedWorking(sessionOf(item)))
    .sort((a, b) => sessionOf(b).lastDataAt - sessionOf(a).lastDataAt);
  return <>{working.length ? <section className="session-activity-group" aria-label="Conversations in progress">
    <div className="session-activity-heading">In progress <span>{working.length}</span></div>
    {working.map(renderItem)}
  </section> : null}</>;
}

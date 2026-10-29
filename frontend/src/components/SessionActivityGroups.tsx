import type { ReactNode } from 'react';
import type { SessionInfo } from '../types';
import { classifySession } from '../lib/sessionStatus';

/** Working is observed activity; setting work aside is a separate choice. */
export function SessionActivityGroups<T>({ items, sessionOf, renderItem }: {
  items: T[];
  sessionOf: (item: T) => SessionInfo;
  renderItem: (item: T) => ReactNode;
}): JSX.Element {
  const working = items.filter((item) => classifySession(sessionOf(item)).state === 'working');
  const active = items.filter((item) => classifySession(sessionOf(item)).state !== 'working');
  return <>
    {working.length ? <div className="session-activity-group" role="group" aria-label="Working conversations">
      <div className="session-activity-heading">Working <span>{working.length}</span></div>
      {working.map(renderItem)}
    </div> : null}
    {active.length ? <div className="session-activity-group">
      {working.length ? <div className="session-activity-heading">Conversations</div> : null}
      {active.map(renderItem)}
    </div> : null}
  </>;
}

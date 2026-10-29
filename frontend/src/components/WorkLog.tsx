import { Button } from './ui/Button';
import { useEffect, useState } from 'react';
import type { ActivityEntry, ToolCall } from '../types';
import { renderContent } from '../lib/contentRender';
import { findLaneLinks, requestOpenLane, type LaneLinkTarget } from '../lib/laneLink';
import {
  firstLine, formatElapsed, summarizeActivity, toolActivityLabel, toolCategory, toolFailed, toolRunning,
  type ToolCategory
} from '../lib/workLog';
import '../styles/work-log.css';

// One agent turn's work, in order. While the turn is live every step shows
// as it arrives under a "Working for 40s" header; once it ends the group
// folds into one summary line that opens back up on request.

interface WorkLogProps {
  entries: ActivityEntry[];
  live: boolean;
  // When the live turn began; drives the elapsed timer on the live header.
  startedAt?: number;
  // Only one group per turn carries the live header.
  showLiveHeader?: boolean;
  cwd?: string;
}

export function WorkLog({ entries, live, startedAt, showLiveHeader = false, cwd = '' }: WorkLogProps): JSX.Element | null {
  const [manualOpen, setManualOpen] = useState<boolean | null>(null);
  const [openRow, setOpenRow] = useState<string | null>(null);
  if (entries.length === 0 && !(live && showLiveHeader)) return null;
  const open = manualOpen ?? live;
  return (
    <div className={`work-log${live ? ' is-live' : ''}${open ? ' is-open' : ''}`} data-no-copy onClick={(event) => event.stopPropagation()}>
      {live && showLiveHeader ? (
        <LiveHeader startedAt={startedAt} />
      ) : !live && entries.length > 0 ? (
        <Button variant="ghost" size="compact" type="button" className="work-log-summary" aria-expanded={open} onClick={() => setManualOpen(!open)}>
          <span>{summarizeActivity(entries)}</span>
          <span className="work-log-caret" aria-hidden>{open ? '⌄' : '›'}</span>
        </Button>
      ) : null}
      {open && entries.length > 0 ? (
        <ol className="work-log-rows">
          {entries.map((entry) => (
            <WorkLogRow
              key={entry.id}
              entry={entry}
              live={live}
              cwd={cwd}
              open={openRow === entry.id}
              onToggle={() => setOpenRow(openRow === entry.id ? null : entry.id)}
            />
          ))}
        </ol>
      ) : null}
    </div>
  );
}

function LiveHeader({ startedAt }: { startedAt?: number }): JSX.Element {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return (
    <div className="work-log-live" role="status">
      <span className="work-log-live-dot" aria-hidden />
      <span>{startedAt ? `Working for ${formatElapsed(now - startedAt)}` : 'Working'}</span>
    </div>
  );
}

interface WorkLogRowProps {
  entry: ActivityEntry;
  live: boolean;
  cwd: string;
  open: boolean;
  onToggle: () => void;
}

function WorkLogRow({ entry, live, cwd, open, onToggle }: WorkLogRowProps): JSX.Element {
  if (entry.kind !== 'tool') {
    const thinking = entry.kind === 'thinking';
    return (
      <li className={`work-log-row is-${entry.kind}${open ? ' is-open' : ''}`}>
        <Button variant="ghost" size="compact" type="button" className="work-log-row-head" onClick={onToggle} aria-expanded={open}>
          <WorkIcon kind={thinking ? 'thinking' : 'update'} />
          <span className={`work-log-row-label${open ? ' is-hidden' : ''}`}>{firstLine(entry.text)}</span>
          <span className="work-log-row-caret" aria-hidden>{open ? '▾' : '▸'}</span>
        </Button>
        {open ? <div className="work-log-row-text md-content" dangerouslySetInnerHTML={{ __html: renderContent(entry.text, cwd) }} /> : null}
      </li>
    );
  }
  const call = entry.call;
  const running = toolRunning(call, live);
  const failed = toolFailed(call);
  // A command that started or named a Sessions lane links straight to it.
  const lanes = findLaneLinks(`${call.inputFull ?? ''}\n${call.resultFull ?? ''}`);
  return (
    <li className={`work-log-row is-tool${running ? ' is-running' : ''}${failed ? ' is-failed' : ''}${open ? ' is-open' : ''}`}>
      <div className="work-log-row-line">
        <Button variant="ghost" size="compact" type="button" className="work-log-row-head" onClick={onToggle} aria-expanded={open} title={call.name}>
          <WorkIcon kind={running ? 'running' : lanes.length > 0 ? 'agent' : toolCategory(call)} />
          <span className="work-log-row-label">{toolActivityLabel(call, running)}</span>
          {failed ? <span className="work-log-row-badge">failed</span> : null}
          <span className="work-log-row-caret" aria-hidden>{open ? '▾' : '▸'}</span>
        </Button>
        <LaneLinkButtons lanes={lanes} />
      </div>
      {open ? <ToolDetail call={call} running={running} /> : null}
    </li>
  );
}

/** "Open lane" for each Sessions lane a step or report names. */
export function LaneLinkButtons({ lanes }: { lanes: LaneLinkTarget[] }): JSX.Element | null {
  if (lanes.length === 0) return null;
  return (
    <span className="work-log-lanes">
      {lanes.map((lane, index) => (
        <Button variant="ghost" size="compact"
          key={lane.sessionId}
          type="button"
          className="work-log-lane"
          title={`Open lane ${lane.sessionId}`}
          onClick={(event) => { event.stopPropagation(); requestOpenLane(lane); }}
        >
          {lanes.length > 1 ? `Open lane ${index + 1}` : 'Open lane'}
        </Button>
      ))}
    </span>
  );
}

function ToolDetail({ call, running }: { call: ToolCall; running: boolean }): JSX.Element {
  return (
    <div className="work-log-detail">
      {call.inputFull ? <pre className="work-log-detail-input">{call.inputFull}</pre> : null}
      {call.resultFull ? (
        <pre className="work-log-detail-output">{call.resultFull}</pre>
      ) : (
        <div className="work-log-detail-empty">{running ? 'Running…' : 'No output captured'}</div>
      )}
      {call.durationMs != null ? <div className="work-log-detail-meta">{formatElapsed(call.durationMs)}</div> : null}
    </div>
  );
}

type IconKind = ToolCategory | 'thinking' | 'update' | 'running';

const ICON_PATHS: Record<Exclude<IconKind, 'running'>, string> = {
  command: 'M4 7l4 4-4 4M10 16h6',
  read: 'M6 4h6l4 4v10H6zM12 4v4h4',
  edit: 'M5 17l1-4 8-8 3 3-8 8zM12 7l3 3',
  search: 'M10.5 15a4.5 4.5 0 110-9 4.5 4.5 0 010 9zM14 14l4 4',
  web: 'M11 18a7 7 0 100-14 7 7 0 000 14zM4 11h14M11 4c2 2 2.8 4.5 2.8 7s-.8 5-2.8 7c-2-2-2.8-4.5-2.8-7s.8-5 2.8-7z',
  agent: 'M7 9a2 2 0 100-4 2 2 0 000 4zM15 9a2 2 0 100-4 2 2 0 000 4zM4 16c0-2 1.5-4 3-4s3 2 3 4M12 16c0-2 1.5-4 3-4s3 2 3 4',
  mcp: 'M8 5v4M14 5v4M6 9h10v2a5 5 0 01-10 0zM11 16v3',
  plan: 'M6 6h10M6 11h10M6 16h6',
  tool: 'M11 13a2 2 0 100-4 2 2 0 000 4z',
  thinking: 'M11 4l1.6 4.4L17 10l-4.4 1.6L11 16l-1.6-4.4L5 10l4.4-1.6z',
  update: 'M5 7h12M5 11h12M5 15h8'
};

function WorkIcon({ kind }: { kind: IconKind }): JSX.Element {
  if (kind === 'running') return <span className="work-log-icon is-running" aria-hidden />;
  return (
    <svg className={`work-log-icon is-${kind}`} viewBox="0 0 22 22" width="15" height="15" aria-hidden>
      <path d={ICON_PATHS[kind]} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

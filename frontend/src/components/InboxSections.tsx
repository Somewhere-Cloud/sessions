import { ProjectMark, SessionWorkspaceBranch } from './SessionProjectIdentity';
import { SessionActivityGroups, WorkingObservation, isObservedWorking } from './SessionActivityGroups';
import { useId, useState, type JSX } from 'react';
import type { SessionInfo } from '../types';
import { type InboxLayout, type InboxSection, type ProviderFaultNotice } from '../lib/inboxSections';
import { classifySession } from '../lib/sessionStatus';
import { quietSessionTitle, resolvedSessionLabel } from '../lib/tabLabels';
import { ProviderMark, normalizeProvider } from './ProviderBadge';
import { AccountBadge } from './AccountBadge';
import { lastMessage } from '../lib/lastMessage';
import { projectPreview } from '../lib/projectPreview';

interface Props {
  layout: InboxLayout;
  activeSessionId?: string | null;
  renderNode: (session: SessionInfo, endedFlat?: boolean, projectLabel?: string) => JSX.Element | null;
  onOpen: (id: string) => void;
  onShowAllNeedsYou: () => void;
  folderOf: (session: SessionInfo) => string;
  relativeTime: (at: number) => string;
  lastActivity: (session: SessionInfo) => number;
  providerNotices?: ProviderFaultNotice[];
  onOpenProviderFault?: (notice: ProviderFaultNotice) => void;
  /** In the flat list a row says which project it belongs to, since no header does. */
  projectLabelOf?: (session: SessionInfo) => string;
}

// The inbox body: a needs-you strip, then one section per named project,
// then everything unnamed under Other projects. Rows come from the
// navigator's own renderer so pins, drag, menus, and child folding behave the
// same everywhere; this component only decides where each row sits.
export function InboxSections({ layout, activeSessionId, renderNode, onOpen, onShowAllNeedsYou, folderOf, relativeTime, lastActivity, providerNotices = [], onOpenProviderFault, projectLabelOf }: Props) {
  const original = layout.flat ? [layout.flat] : layout.other ? [...layout.sections, layout.other] : layout.sections;
  const working = original.flatMap((section) => section.live.filter(isObservedWorking).map((session) => ({ session, project: projectLabelOf?.(session) || section.name })));
  const sections = original.map((section) => ({ ...section, live: section.live.filter((session) => !isObservedWorking(session)) }))
    .filter((section) => section.live.length || section.notConnected.length || section.finished.length);
  return (
    <>
      <ProviderFaultBanners notices={providerNotices} onOpen={onOpenProviderFault} />
      {layout.providerTrouble.length > 0 ? (
        <div className="session-tree-group inbox-provider-trouble" role="group" aria-label="Sessions with provider trouble">
          <div className="session-tree-group-head is-provider-trouble"><span>Provider trouble</span><strong>{layout.providerTrouble.length}</strong></div>
          {layout.providerTrouble.map((session) => (
            <AttentionRow
              key={session.id}
              session={session}
              why={classifySession(session).label}
              onOpen={onOpen}
              folderOf={folderOf}
              relativeTime={relativeTime}
              lastActivity={lastActivity}
              providerTrouble
            />
          ))}
        </div>
      ) : null}
      {layout.needsYou.length > 0 ? (
        <div className="session-tree-group inbox-needs" role="group" aria-label="Sessions waiting on you">
          <div className="session-tree-group-head is-attention"><span>Waiting for you</span><strong>{layout.needsYou.length + layout.moreNeedsYou}</strong></div>
          {layout.needsYou.map((session) => (
            <AttentionRow
              key={session.id}
              session={session}
              why={session.idleDetail || 'Waiting for you'}
              onOpen={onOpen}
              folderOf={folderOf}
              relativeTime={relativeTime}
              lastActivity={lastActivity}
            />
          ))}
          {layout.moreNeedsYou > 0 ? (
            <button type="button" className="inbox-fold" onClick={onShowAllNeedsYou}>
              <span>{layout.moreNeedsYou} more waiting</span><small>show all</small>
            </button>
          ) : null}
        </div>
      ) : null}
      <SessionActivityGroups items={working} sessionOf={(row) => row.session} renderItem={({ session, project }) => <div key={session.id}>
        {renderNode(session, false, project)}<WorkingObservation session={session} />
      </div>} />
      {layout.flat
        ? sections[0] ? <RecentList section={sections[0]} renderNode={renderNode} projectLabelOf={projectLabelOf} /> : null
        : sections.map((section) => (
          <ProjectSection key={section.id} section={section} activeSessionId={activeSessionId} renderNode={renderNode} projectLabelOf={projectLabelOf} />
        ))}
    </>
  );
}

export function ProviderFaultBanners({ notices, onOpen }: {
  notices: ProviderFaultNotice[];
  onOpen?: (notice: ProviderFaultNotice) => void;
}): JSX.Element {
  return <>{notices.map((notice) => (
    <button type="button" className="inbox-provider-banner" key={notice.provider} onClick={() => onOpen?.(notice)}>
      <span><strong>{notice.provider === 'codex' ? 'Codex' : 'Claude'} is having trouble</strong> — {notice.count} sessions since {new Date(notice.since).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</span>
    </button>
  ))}</>;
}

function AttentionRow({ session, why, onOpen, folderOf, relativeTime, lastActivity, providerTrouble = false }: {
  session: SessionInfo;
  why: string;
  onOpen: Props['onOpen'];
  folderOf: Props['folderOf'];
  relativeTime: Props['relativeTime'];
  lastActivity: Props['lastActivity'];
  providerTrouble?: boolean;
}) {
  const provider = normalizeProvider(session.tool);
  const status = classifySession(session);
  return (
    <button type="button" className="inbox-needs-row" onClick={() => onOpen(session.id)} title={session.failureDetail || 'Open this session'}>
      <span className="inbox-needs-mark">{provider ? <ProviderMark provider={provider} size={16} /> : <span aria-hidden>⌘</span>}</span>
      <span className="inbox-needs-copy">
        <strong>{quietSessionTitle(resolvedSessionLabel(session))}<AccountBadge session={session} /></strong>
        <small>{folderOf(session)}</small>
        <SessionWorkspaceBranch session={session} />
        <span className={`inbox-needs-why${providerTrouble ? ` ${status.className}` : ''}`}>{why}</span>
      </span>
      <time>{relativeTime(lastMessage(session).at || lastActivity(session))}</time>
    </button>
  );
}

// A collapsed group of rows with one line saying what is inside it. Both
// arrangements fold the same two things, so they fold them the same way.
function Fold({ label, count, detail, children }: {
  label: string;
  count: number;
  detail: string;
  children: () => Array<JSX.Element | null>;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" className="inbox-fold inbox-status-fold" onClick={() => setOpen((current) => !current)} aria-expanded={open}>
        <span className="inbox-fold-label"><span>{open ? '▾' : '▸'} {label}</span><span>{count}</span></span>
        <small>{detail}</small>
      </button>
      {open ? children() : null}
    </>
  );
}

// The flat arrangement: one list, newest first, no project headers. Each row
// carries its project as a label, so "which work is this?" is still answerable
// without the grouping that used to answer it.
function RecentList({ section, renderNode, projectLabelOf }: {
  section: InboxSection;
  renderNode: Props['renderNode'];
  projectLabelOf?: Props['projectLabelOf'];
}) {
  const row = (session: SessionInfo, ended = false): JSX.Element | null =>
    renderNode(session, ended, projectLabelOf?.(session));
  return (
    <div className="session-tree-group inbox-recent" role="group" aria-label="Sessions, most recent first">
      {section.live.map((session) => row(session))}
      {section.live.length === 0 && section.notConnected.length === 0
        ? <div className="session-tree-empty is-compact">Nothing live here.</div>
        : null}
      {section.notConnected.length > 0 ? (
        <Fold label="Unavailable agents" count={section.notConnected.length} detail="Cannot reach these agents. Open one for connection details.">
          {() => section.notConnected.map((session) => row(session))}
        </Fold>
      ) : null}
      {section.finished.length > 0 ? (
        <Fold
          label="Closed"
          count={section.finished.length}
          detail={`${resolvedSessionLabel(section.finished[0]!)}${section.finished.length > 1 ? ` · +${section.finished.length - 1}` : ''}`}
        >
          {() => section.finished.map((session) => row(session, true))}
        </Fold>
      ) : null}
    </div>
  );
}

function ProjectSection({ section, activeSessionId, renderNode, projectLabelOf }: { section: InboxSection; activeSessionId?: string | null; renderNode: Props['renderNode']; projectLabelOf: Props['projectLabelOf'] }) {
  const [open, setOpen] = useState(true);
  const [showAll, setShowAll] = useState(false);
  const rowsId = useId();
  const preview = projectPreview(section.live, (session) => session, (session) => session.id === activeSessionId);
  const remaining = section.live.length - preview.length;
  const row = (session: SessionInfo, ended = false) => renderNode(session, ended, section.implicit ? projectLabelOf?.(session) : undefined);
  const count = section.live.length + section.notConnected.length;
  return (
    <div className={`session-tree-group inbox-project${section.implicit ? ' is-implicit' : ''}`}>
      <button type="button" className="session-tree-group-head inbox-project-head" onClick={() => setOpen((current) => !current)} aria-expanded={open} aria-controls={rowsId}>
        <span className="session-group-disclosure">
          <span className={`inbox-chevron${open ? ' is-open' : ''}`} aria-hidden>▸</span>
          <ProjectMark name={section.name} icon={section.implicit ? undefined : section.live.find((session) => session.workspaceGit?.iconDataUrl)?.workspaceGit?.iconDataUrl} />{section.name}
        </span>
        <strong>
          {count}
        </strong>
      </button>
      <div id={rowsId} hidden={!open}>{open ? (
        <>
          {(showAll ? section.live : preview).map((session) => row(session))}
          {remaining > 0 ? <button type="button" className="inbox-fold project-preview-toggle" aria-expanded={showAll} aria-controls={rowsId} onClick={() => setShowAll((current) => !current)}>
            {showAll ? 'Show fewer agents' : `Show ${remaining} remaining agents`}
          </button> : null}
          {section.live.length === 0 && section.notConnected.length === 0 ? <div className="session-tree-empty is-compact">Nothing live here.</div> : null}
          {section.notConnected.length > 0 ? (
            <Fold label="Unavailable agents" count={section.notConnected.length} detail="Cannot reach these agents. Open one for connection details.">
              {() => section.notConnected.map((session) => row(session))}
            </Fold>
          ) : null}
          {section.finished.length > 0 ? (
            <Fold
              label="Closed"
              count={section.finished.length}
              detail={`${resolvedSessionLabel(section.finished[0]!)}${section.finished.length > 1 ? ` · +${section.finished.length - 1}` : ''}`}
            >
              {() => section.finished.map((session) => row(session, true))}
            </Fold>
          ) : null}
        </>
      ) : null}</div>
    </div>
  );
}

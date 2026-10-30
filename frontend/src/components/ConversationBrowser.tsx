import { EmptyState, PageHeader, Surface } from './ui/Layout';
import { Button } from './ui/Button';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  fetchServerHistoryListing,
  fetchServerHistoryTranscript,
  listServerSessions,
  type HistoryMessage
} from '../api/sessionsd';
import {
  buildConversationRows,
  compactMachineName,
  compactPath,
  conversationAge,
  conversationResumeTarget,
  filterConversations,
  plural,
  type BrowseFilters,
  type ConversationRow,
  type ConversationSource,
  type ResumeTarget
} from '../lib/conversationBrowser';
import { harnessDisplay } from '../lib/harnessContent';
import { serverDisplayName, useServers, type ServerConfig } from '../lib/servers';
import { ParserIcon } from './ParserIcon';
import { ProviderBadge, normalizeProvider } from './ProviderBadge';

// ───────────────────────────────────────────────────────────────────────────
// Every recorded conversation, not only the ones Sessions started.
//
// The papercut this answers: neither provider can tell you where a
// conversation was. `claude --resume` only offers the directory you happen to
// be standing in, Codex's picker filters by cwd too, and Sessions' own session
// list shows the runtimes Sessions created — 27 rows on a machine holding 303
// conversations. `sessions history` fixed that for the terminal; this is the
// same answer on screen, reading the same /api/history and applying the same
// resume rules (lib/conversationBrowser.ts).
//
// It shows the twenty most recent by default and says how many it did not
// show. A browser that lists three hundred undifferentiated rows has not
// helped anyone find one, and a browser that silently truncates is
// indistinguishable from one that found nothing else.
// ───────────────────────────────────────────────────────────────────────────

const PAGE = 20;
const PREVIEW_MESSAGES = 4;

interface Props {
  /** Everything except `all`, which is this component's own visible toggle. */
  filters: Omit<BrowseFilters, 'all'>;
  /** Open the read-only reader for this conversation. Resumes nothing. */
  onOpen: (row: ConversationRow) => void;
  onResume: (row: ConversationRow, target: ResumeTarget) => void;
  /** Attach to the session already running this conversation. */
  onAttach?: (row: ConversationRow) => void;
  /** The row whose resume is in flight, so exactly one button reads busy. */
  resumingKey?: string | null;
  /** True when a filter is set, so empty-state advice can name the cause. */
  filtered: boolean;
}

interface MachineAnswer {
  source: ConversationSource | null;
  serverName: string;
  error: string | null;
  unreadable: number;
}

interface PreviewState {
  open: boolean;
  loading: boolean;
  error: string | null;
  messages: HistoryMessage[];
}

async function readMachineHistory(server: ServerConfig, parentSignal: AbortSignal): Promise<MachineAnswer> {
  const serverName = serverDisplayName(server, true);
  const controller = new AbortController();
  const abort = (): void => controller.abort();
  parentSignal.addEventListener('abort', abort, { once: true });
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    const [listing, live] = await Promise.race([
      Promise.all([fetchServerHistoryListing(server, controller.signal), listServerSessions(server, controller.signal)]),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => { reject(new Error('History is taking too long. Refresh to try this computer again.')); controller.abort(); }, 8_000);
      })
    ]);
    return { source: { serverId: server.id, serverName, sessions: listing.sessions, live }, serverName, error: null, unreadable: listing.unreadableSessions };
  } catch (reason) {
    return { source: null, serverName, error: reason instanceof Error ? reason.message : 'did not answer', unreadable: 0 };
  } finally {
    clearTimeout(timer);
    parentSignal.removeEventListener('abort', abort);
  }
}

export function ConversationBrowser({
  filters,
  onOpen,
  onResume,
  onAttach,
  resumingKey = null,
  filtered
}: Props): JSX.Element {
  const servers = useServers((state) => state.servers);
  const serversRef = useRef(servers);
  serversRef.current = servers;
  // Endpoints are re-read on demand, but the fetch is keyed on which machines
  // exist. The servers store hands back a new array on every unrelated write,
  // and depending on that identity would re-list every machine's history each
  // time an identity probe or a token save touched the store.
  const serverKey = servers.map((server) => server.id).join('\u0000');
  const [answers, setAnswers] = useState<MachineAnswer[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloadToken, setReloadToken] = useState(0);
  const [limit, setLimit] = useState(PAGE);
  const [showAll, setShowAll] = useState(false);
  const [previews, setPreviews] = useState<Record<string, PreviewState>>({});

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setAnswers(null);
    // Render each computer as it answers; a slow host cannot hide local work.
    void Promise.all(serversRef.current.map(async (server) => {
      const answer = await readMachineHistory(server, controller.signal);
      if (!controller.signal.aborted) setAnswers((current) => [...(current ?? []), answer]);
    })).then(() => {
      if (controller.signal.aborted) return;
      setLoading(false);
    });
    return () => controller.abort();
  }, [serverKey, reloadToken]);

  const rows = useMemo(
    () => buildConversationRows((answers ?? []).flatMap((answer) => answer.source ? [answer.source] : [])),
    [answers]
  );
  const everything = useMemo(
    () => filterConversations(rows, { ...filters, all: true }),
    [rows, filters]
  );
  const matched = useMemo(
    () => showAll ? everything : filterConversations(rows, { ...filters, all: false }),
    [rows, filters, showAll, everything]
  );
  const shown = matched.slice(0, limit);

  // A narrowed list starts at the top again; keeping a raised page count across
  // a filter change shows a different twenty than the one the user asked for.
  useEffect(() => {
    setLimit(PAGE);
  }, [filters.tool, filters.since, filters.until, filters.name, filters.cwd, showAll]);

  const failures = (answers ?? []).filter((answer) => answer.error);
  const answered = (answers ?? []).length - failures.length;
  const unreadable = (answers ?? []).reduce((sum, answer) => sum + answer.unreadable, 0);
  // Any machine that did not answer makes every count on this screen a lower
  // bound. Saying "303 conversations" when a machine is missing is a quiet lie
  // about what exists.
  const partial = loading || failures.length > 0;
  const hiddenByDefault = showAll ? 0 : everything.length - matched.length;

  const serverFor = useCallback(
    (row: ConversationRow): ServerConfig | undefined => serversRef.current.find((server) => server.id === row.serverId),
    []
  );

  const togglePreview = (row: ConversationRow): void => {
    const current = previews[row.key];
    if (current?.open) {
      setPreviews((state) => ({ ...state, [row.key]: { ...current, open: false } }));
      return;
    }
    if (current && !current.error) {
      setPreviews((state) => ({ ...state, [row.key]: { ...current, open: true } }));
      return;
    }
    const server = serverFor(row);
    if (!server) {
      setPreviews((state) => ({
        ...state,
        [row.key]: { open: true, loading: false, error: `The ${row.serverName} connection is no longer configured.`, messages: [] }
      }));
      return;
    }
    setPreviews((state) => ({ ...state, [row.key]: { open: true, loading: true, error: null, messages: [] } }));
    // The preview route is tail-bounded on the daemon, so reading the end of a
    // two-thousand-message conversation never pulls the whole transcript. It
    // is a read: no session is created and nothing about the conversation
    // changes, which is the entire point of previewing before resuming.
    void fetchServerHistoryTranscript(server, row.id, undefined, { preview: true })
      .then((transcript) => {
        const spoken = transcript.messages.filter((message) => message.role === 'user' || message.role === 'assistant');
        setPreviews((state) => ({
          ...state,
          [row.key]: { open: true, loading: false, error: null, messages: spoken.slice(-PREVIEW_MESSAGES) }
        }));
      })
      .catch((reason: unknown) => setPreviews((state) => ({
        ...state,
        [row.key]: {
          open: true,
          loading: false,
        error: reason instanceof Error ? reason.message : 'Preview failed.',
          messages: []
        }
      })));
  };

  return (
    <section className="conversation-browser" aria-label="Recorded conversations">
      <PageHeader level={2} title="Recent conversations" description="Claude and Codex history from your machines, newest first." actions={
        <Button disabled={loading} onClick={() => setReloadToken((value) => value + 1)}>{loading ? 'Reading…' : 'Refresh'}</Button>
      } />

      {loading ? <p role="status">Reading available computers… Results appear as each one responds.</p> : null}
      {failures.length > 0 ? (
        <div className="search-notice" role="status">
          <span aria-hidden>±</span>
          <span>
            {failures.map((answer) => compactMachineName(answer.serverName)).join(', ')} did not answer, so there is
            more history than this. {failures.map((answer) => `${compactMachineName(answer.serverName)}: ${answer.error}`).join(' · ')}
          </span>
        </div>
      ) : null}
      {unreadable > 0 ? (
        <div className="search-notice" role="status">
          <span aria-hidden>⌁</span>
          <span>{plural(unreadable, 'conversation')} could not be read on this pass. {showAll ? 'They are listed below with what failed.' : 'Show everything to see them.'}</span>
        </div>
      ) : null}

      {loading && answered === 0 ? (
        <div className="search-loading-results" aria-hidden><span /><span /><span /></div>
      ) : answered === 0 ? (
        <EmptyState title="History is unavailable">
          No machine answered.
          {failures.length > 0 ? ` ${failures.map((answer) => `${compactMachineName(answer.serverName)}: ${answer.error}`).join(' · ')}` : ''}
        </EmptyState>
      ) : rows.length === 0 ? (
        <EmptyState title="No saved conversations">
          Claude and Codex history will appear here when it is available.
        </EmptyState>
      ) : matched.length === 0 ? (
        <EmptyState title="No conversations in this view">
          <p>
            No conversation matches {filtered ? 'these filters' : 'this view'}.{' '}
            {plural(rows.length, 'conversation')} {rows.length === 1 ? 'is' : 'are'} recorded
            {partial ? ' on the machines that answered' : ''}.
          </p>
          {!showAll ? (
            <Button type="button" variant="ghost" onClick={() => setShowAll(true)}>
              Show empty and unrecoverable
            </Button>
          ) : null}
        </EmptyState>
      ) : (
        <div className="search-results">
          {shown.map((row) => (
            <ConversationCard
              key={row.key}
              row={row}
              preview={previews[row.key]}
              resuming={resumingKey === row.key}
              onOpen={() => onOpen(row)}
              onTogglePreview={() => togglePreview(row)}
              onResume={onResume}
              onAttach={onAttach}
            />
          ))}
        </div>
      )}

      {matched.length > 0 ? (
        <footer className="conversation-browser-foot">
          <span>
            {partial ? 'at least ' : ''}{plural(matched.length, 'conversation')}
            {shown.length < matched.length ? ` · showing the ${shown.length} most recent` : ''}
            {` · ${partial ? 'at least ' : ''}${plural(rows.length, 'conversation')} recorded`}
            {hiddenByDefault > 0 ? ` · ${hiddenByDefault} hidden as empty, shell-only, or unrecoverable` : ''}
          </span>
          <span className="conversation-browser-foot-actions">
            {shown.length < matched.length ? (
              <Button type="button" onClick={() => setLimit((value) => value + PAGE)}>
                Show {Math.min(PAGE, matched.length - shown.length)} more
              </Button>
            ) : null}
            <Button type="button" onClick={() => setShowAll((value) => !value)}>
              {showAll ? 'Show conversations only' : 'Show everything'}
            </Button>
          </span>
        </footer>
      ) : null}
    </section>
  );
}

function ConversationCard({
  row,
  preview,
  resuming,
  onOpen,
  onTogglePreview,
  onResume,
  onAttach
}: {
  row: ConversationRow;
  preview?: PreviewState;
  resuming: boolean;
  onOpen: () => void;
  onTogglePreview: () => void;
  onResume: (row: ConversationRow, target: ResumeTarget) => void;
  onAttach?: (row: ConversationRow) => void;
}): JSX.Element {
  const provider = normalizeProvider(row.tool);
  const resumeTarget = conversationResumeTarget(row);
  const age = conversationAge(row.lastActiveAt);
  const stamp = row.lastActiveAt
    ? new Date(row.lastActiveAt).toLocaleString(undefined, {
      month: 'short', day: 'numeric', year: new Date(row.lastActiveAt).getFullYear() === new Date().getFullYear() ? undefined : 'numeric',
      hour: 'numeric', minute: '2-digit'
    })
    : 'No recorded activity';
  return (
    <Surface data-history-key={row.key} className={`search-result-card conversation-row${provider ? ` is-${provider}` : ''}${row.status === 'live' ? ' is-live' : ''}`}>
      <span className="search-result-provider" aria-label={provider ? `${provider} conversation` : 'Saved conversation'}>
        <ParserIcon icon={provider === 'claude' ? '🟠' : provider === 'codex' ? '🟢' : '⬛'} size={24} />
      </span>
      <span className="search-result-body">
        <Button type="button" variant="ghost" className="search-result-main" onClick={onOpen} disabled={!row.readable}>
          <span className="search-result-source">
            <strong>{row.name || 'Untitled conversation'}</strong>
            {row.status === 'live' ? <span className="search-title-match">Live now</span> : null}
            {row.archived ? <span className="conversation-row-tag">Archived</span> : null}
            {row.promptHistoryOnly ? <span className="conversation-row-tag">Prompts only</span> : null}
            {row.copies ? <span className="conversation-row-tag">{plural(row.copies, 'copy', 'copies')}</span> : null}
          </span>
          <span className="search-conversation-match-count">
            {row.dateApproximate ? 'Approx. ' : ''}{stamp}
            {age ? ` · ${age}` : ''}
            {row.messagesKnown === false ? '' : ` · ${row.messages > 0 ? plural(row.messages, 'message') : 'no messages recorded'}`}
          </span>
        </Button>

        {preview?.open ? (
          <span className="conversation-preview">
            {preview.loading ? <span className="conversation-preview-note">Reading the end of this conversation…</span> : null}
            {preview.error ? <span className="conversation-preview-note">Preview unavailable: {preview.error}</span> : null}
            {!preview.loading && !preview.error && preview.messages.length === 0
              ? <span className="conversation-preview-note">No readable messages.</span>
              : null}
            {preview.messages.map((message) => {
              // A user-role record the harness wrote is not something the
              // person said, in a preview any more than in the transcript.
              const shown = message.role === 'user'
                ? harnessDisplay(message.text, 'You said')
                : { speaker: provider === 'codex' ? 'Codex said' : provider === 'claude' ? 'Claude said' : 'Agent said', text: message.text };
              return (
                <span className="conversation-preview-line" key={message.id || message.index}>
                  <span>{shown.speaker}</span>
                  <span>{shown.text}</span>
                </span>
              );
            })}
          </span>
        ) : null}

        <span className="search-result-footer">
          <span className="search-result-location">
            {provider ? <ProviderBadge provider={provider} compact /> : <span>Shell</span>}
            <span>{compactMachineName(row.serverName)}</span>
            {row.cwd ? <code title={row.cwd}>{compactPath(row.cwd)}</code> : <span>No recorded folder</span>}
            {row.external ? <span>Started outside Sessions</span> : null}
          </span>
          <span className="search-result-actions">
            {row.readable ? (
              <Button type="button" onClick={onTogglePreview}>
                {preview?.open ? 'Hide preview' : 'Preview'}
              </Button>
            ) : null}
            {row.readable && !(row.status === 'live' && onAttach) ? <Button type="button" onClick={onOpen}>Open conversation <span aria-hidden>→</span></Button> : null}
            {row.status === 'live' && onAttach ? (
              <Button type="button" className="is-resume" onClick={() => onAttach(row)}>Open the live session</Button>
            ) : null}
            {resumeTarget ? (
              <Button type="button" className="is-resume" disabled={resuming} onClick={() => onResume(row, resumeTarget)}>
                {resuming ? 'Resuming…' : 'Resume conversation'}
              </Button>
            ) : null}
          </span>
        </span>

        {/* A row that cannot be resumed says why, in place of the button it
            does not have. This is the difference between a browser that is
            honest and one that offers an action the daemon will refuse. */}
        {row.copies ? <span className="conversation-row-note">Why there are copies: Sessions keeps each run. Resume uses the newest.</span> : null}
        {row.status !== 'resumable' && !(row.status === 'live' && onAttach) ? (
          <span className={`conversation-row-note${row.status === 'unrecoverable' ? ' is-gone' : ''}`}>{row.reason}</span>
        ) : null}
      </span>
    </Surface>
  );
}

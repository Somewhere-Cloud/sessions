import { Button } from './ui/Button';
import { EmptyState, PageHeader, Section, Stack, Surface } from './ui/Layout';
import type { SessionInfo } from '../types';
import { resolvedSessionLabel } from '../lib/tabLabels';
import { classifySession, sessionNeedsYou } from '../lib/sessionStatus';
import { ProviderBadge, normalizeProvider } from './ProviderBadge';
import { lastMessage, lastMessageLine } from '../lib/lastMessage';

interface Props {
  sessions: SessionInfo[];
  machine: string;
  onOpen: (id: string) => void;
  onNew: () => void;
  onNavigate: (view: 'today' | 'fleet' | 'usage') => void;
}

export function HomeView({ sessions, machine, onOpen, onNew, onNavigate }: Props): JSX.Element {
  const live = sessions.filter((session) => !session.exited);
  const attention = live.filter(sessionNeedsYou);
  const running = live.filter((session) => session.working);
  const recent = [...sessions].sort((a, b) => b.lastDataAt - a.lastDataAt).slice(0, 5);
  return (
    <div className="home-view"><Stack>
      <PageHeader eyebrow={machine} title={`Good ${dayPart()}.`}
        description={attention.length ? `${attention.length} session${attention.length === 1 ? '' : 's'} need your attention.` : 'A little room for your next idea.'}
        actions={<Button onClick={onNew} variant="primary">＋ New session</Button>} />
      <section className="home-stat-grid">
        <Button variant="ghost" onClick={() => onNavigate('today')}><span>Needs you</span><strong>{attention.length}</strong><small>Waiting for direction</small></Button>
        <Button variant="ghost" onClick={() => onNavigate('fleet')}><span>Running now</span><strong>{running.length}</strong><small>Across {machine}</small></Button>
        <Button variant="ghost" onClick={() => onNavigate('usage')}><span>Live sessions</span><strong>{live.length}</strong><small>Usage and cost details</small></Button>
      </section>
      <Surface className="home-recent"><Section title="Recent sessions" actions={<Button variant="ghost" size="compact" onClick={() => onNavigate('today')}>Open Daily →</Button>}>
        <div className="home-session-list">
          {recent.map((session) => {
            const provider = normalizeProvider(session.tool);
            const status = classifySession(session);
            return <Button variant="ghost" key={session.id} onClick={() => onOpen(session.id)} title={status.label}><span className={`home-session-dot ${status.className}`} /><span className="home-session-copy"><strong>{resolvedSessionLabel(session)}</strong><small>{lastMessageLine(session) || session.idleDetail || session.description || session.cwd}</small></span>{provider ? <ProviderBadge provider={provider} compact /> : <span className="provider-badge is-shell is-compact">⌘ Shell</span>}<span className="home-session-time">{new Date(lastMessage(session).at || session.lastDataAt || session.createdAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</span></Button>;
          })}
          {recent.length === 0 ? <EmptyState title="Ready when you are" action={<Button onClick={onNew}>Start a session</Button>}>Your conversations will appear here.</EmptyState> : null}
        </div>
      </Section></Surface>
    </Stack></div>
  );
}

function dayPart(): string {
  const hour = new Date().getHours();
  return hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening';
}

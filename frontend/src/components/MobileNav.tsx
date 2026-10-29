import { ModalDialog } from './ui/ModalDialog';
import { PageHeader } from './ui/Layout';
import { Button } from './ui/Button';
import { useState, type ReactNode } from 'react';

type MobileLayout = 'home' | 'tabs' | 'today' | 'fleet' | 'search' | 'usage' | 'accounts' | 'settings' | 'connections';

interface Props {
  layoutMode: MobileLayout;
  showingSessionDetail: boolean;
  onLayoutChange: (mode: MobileLayout) => void;
  onShowSessions: () => void;
}

/**
 * Phone navigation deliberately mirrors a consumer app instead of shrinking
 * the desktop toolbar. Sessions is a real destination (the hierarchy), and a
 * selected conversation is pushed on top of it by App. Less-frequent
 * destinations live behind More so the five primary tap targets stay large.
 */
export function MobileNav({
  layoutMode,
  showingSessionDetail,
  onLayoutChange,
  onShowSessions
}: Props): JSX.Element {
  const [moreOpen, setMoreOpen] = useState(false);
  const moreActive = layoutMode === 'fleet'
    || layoutMode === 'usage'
    || layoutMode === 'accounts'
    || layoutMode === 'settings'
    || layoutMode === 'connections';

  const go = (mode: MobileLayout): void => {
    setMoreOpen(false);
    onLayoutChange(mode);
  };

  return (
    <>
      <nav className="mobile-nav" role="navigation" aria-label="Sessions">
        <MobileDestination label="Sessions" icon={<SessionsIcon />} active={layoutMode === 'tabs'} onClick={onShowSessions} detail={showingSessionDetail} />
        <MobileDestination label="Search" icon={<SearchIcon />} active={layoutMode === 'search'} onClick={() => go('search')} />
        <MobileDestination label="More" icon={<MoreIcon />} active={moreActive || moreOpen} onClick={() => setMoreOpen(true)} />
      </nav>

      {moreOpen ? (
        <ModalDialog className="dialog mobile-more-sheet" labelledBy="mobile-more-title" onClose={() => setMoreOpen(false)}>
            <div className="mobile-more-heading"><PageHeader title="More" titleId="mobile-more-title" level={2}
              actions={<Button variant="ghost" aria-label="Close" onClick={() => setMoreOpen(false)}>×</Button>} /></div>
            <div className="mobile-more-grid">
              <MoreDestination title="Usage" detail="Tokens, cost, and projects" icon={<UsageIcon />} onClick={() => go('usage')} />
              <MoreDestination title="Accounts & computers" detail="Sign-ins, computers, and connections" icon={<AccountsIcon />} onClick={() => go('accounts')} />
              <MoreDestination title="Settings" detail="Agents, access, and appearance" icon={<SettingsIcon />} onClick={() => go('settings')} />
              <MoreDestination title="Connections" detail="Pair and manage machines" icon={<ConnectionsIcon />} onClick={() => go('connections')} />
            </div>
            <a className="mobile-more-somewhere" href="https://somewhere.tech" target="_blank" rel="noreferrer">
              <img src="/somewhere-logo.svg" alt="" />
              <span><strong>somewhere.tech</strong><small>Cloud and backup for Sessions</small></span>
              <span aria-hidden>↗</span>
            </a>
        </ModalDialog>
      ) : null}
    </>
  );
}

function MobileDestination({
  label,
  icon,
  active,
  onClick,
  detail = false
}: {
  label: string;
  icon: ReactNode;
  active: boolean;
  onClick: () => void;
  detail?: boolean;
}): JSX.Element {
  return (
    <Button variant="ghost"
      type="button"
      className={`mn-btn mn-destination${active ? ' is-active' : ''}`}
      onClick={onClick}
      aria-label={detail ? `Back to ${label}` : `Open ${label}`}
      aria-current={active ? 'page' : undefined}
    >
      <span className="mn-destination-icon" aria-hidden>{icon}</span>
      <span>{label}</span>
      {detail ? <span className="mn-detail-dot" aria-hidden /> : null}
    </Button>
  );
}

function MoreDestination({ title, detail, icon, onClick }: { title: string; detail: string; icon: ReactNode; onClick: () => void }): JSX.Element {
  return (
    <Button variant="ghost" type="button" onClick={onClick}>
      <span className="mobile-more-icon" aria-hidden>{icon}</span>
      <span><strong>{title}</strong><small>{detail}</small></span>
      <span aria-hidden>›</span>
    </Button>
  );
}

function Icon({ children }: { children: ReactNode }): JSX.Element {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{children}</svg>;
}

function SessionsIcon(): JSX.Element { return <Icon><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M8 9h8M8 13h8M8 17h5"/></Icon>; }
function SearchIcon(): JSX.Element { return <Icon><circle cx="11" cy="11" r="7"/><path d="m20 20-4-4"/></Icon>; }
function MoreIcon(): JSX.Element { return <Icon><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></Icon>; }

function UsageIcon(): JSX.Element { return <Icon><path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/></Icon>; }
function AccountsIcon(): JSX.Element { return <Icon><circle cx="12" cy="8" r="3.5"/><path d="M5 20a7 7 0 0 1 14 0"/></Icon>; }
function SettingsIcon(): JSX.Element { return <Icon><circle cx="12" cy="12" r="3"/><path d="M19 12a7 7 0 0 0-.1-1l2-1.6-2-3.4-2.5 1A8 8 0 0 0 15 6l-.4-2.6h-4L10 6a8 8 0 0 0-1.4.8l-2.4-1-2 3.5 2 1.6a7 7 0 0 0 0 2.1l-2 1.6 2 3.4 2.4-1A8 8 0 0 0 10 18l.5 2.6h4L15 18a8 8 0 0 0 1.4-.8l2.5 1 2-3.5-2-1.6a7 7 0 0 0 .1-1.1Z"/></Icon>; }
function ConnectionsIcon(): JSX.Element { return <Icon><path d="M8.5 15.5 6 18a3 3 0 0 1-4-4l4-4a3 3 0 0 1 4 0"/><path d="m15.5 8.5 2.5-2.5a3 3 0 0 1 4 4l-4 4a3 3 0 0 1-4 0"/><path d="m8 16 8-8"/></Icon>; }

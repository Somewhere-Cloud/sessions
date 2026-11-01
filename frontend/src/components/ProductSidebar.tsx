import { FooterUsage, type FooterUsageProps } from './FooterUsage';
import { Button } from './ui/Button';
import { Suspense, lazy, type ReactNode } from 'react';

// Update awareness is not on the first-paint path: the first check waits
// 15 s anyway, so the cards load in their own chunk.
const SidebarUpdateCards = lazy(() => import('./SidebarUpdateCards').then((module) => ({ default: module.SidebarUpdateCards })));

export type ProductView = 'home' | 'tabs' | 'today' | 'search' | 'fleet' | 'usage' | 'accounts' | 'settings' | 'feedback';
export type ThemeMode = 'dark' | 'light';

interface Props {
  usageScope?: FooterUsageProps['usageScope'];
  active: ProductView;
  theme: ThemeMode;
  onNavigate: (view: ProductView) => void;
  onNewSession: () => void;
  onOpenCommandPalette: () => void;
  onToggleTheme: () => void;
}

const ITEMS: Array<{ id: ProductView; label: string; icon: ReactNode }> = [
  { id: 'tabs', label: 'Sessions', icon: <SessionsIcon /> },
  { id: 'search', label: 'Search', icon: <SearchIcon /> },
  { id: 'accounts', label: 'Accounts', icon: <AccountsIcon /> },
  { id: 'settings', label: 'Settings', icon: <SettingsIcon /> }
];

export function ProductSidebar({ active, theme, onNavigate, onNewSession, onOpenCommandPalette, onToggleTheme, usageScope }: Props): JSX.Element {
  return (
    <aside className="product-sidebar">
      <SidebarNavigation active={active} onNavigate={onNavigate} onNewSession={onNewSession} onOpenCommandPalette={onOpenCommandPalette} />

      <div className="product-sidebar-footer">
        <FooterUsage usageScope={usageScope} onOpenUsage={() => onNavigate('usage')} onOpenDaily={() => onNavigate('today')} />
        <Button variant="ghost"
          type="button"
          aria-label="Send feedback" title="Send feedback"
          className={`product-feedback-button${active === 'feedback' ? ' is-active' : ''}`}
          onClick={() => onNavigate('feedback')}
        >
          <span className="product-nav-icon" aria-hidden><FeedbackIcon /></span>
          <span>Send feedback</span>
        </Button>
        <Suspense fallback={null}><SidebarUpdateCards /></Suspense>
        <Button variant="ghost" type="button" className="theme-toggle" aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`} onClick={onToggleTheme} title={`Use ${theme === 'dark' ? 'light' : 'dark'} mode`}>
          <span className="product-nav-icon" aria-hidden>{theme === 'dark' ? <SunIcon /> : <MoonIcon />}</span>
          <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>
        </Button>
      </div>
    </aside>
  );
}

function Icon({ children }: { children: ReactNode }): JSX.Element {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">{children}</svg>;
}
function SessionsIcon(): JSX.Element { return <Icon><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M8 9h8M8 13h8M8 17h5"/></Icon>; }
function SearchIcon(): JSX.Element { return <Icon><circle cx="11" cy="11" r="7"/><path d="m20 20-4-4"/></Icon>; }
function AccountsIcon(): JSX.Element { return <Icon><circle cx="12" cy="8" r="3.5"/><path d="M5 20a7 7 0 0 1 14 0"/></Icon>; }
function FeedbackIcon(): JSX.Element { return <Icon><path d="M4 5.5h16v11H9l-5 4v-15Z"/><path d="M8 9h8M8 13h5"/></Icon>; }
function SettingsIcon(): JSX.Element { return <Icon><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1 .6 1.7 1.7 0 0 0-.4 1.1V21H9.6v-.1A1.7 1.7 0 0 0 8.5 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-.6-1 1.7 1.7 0 0 0-1.1-.4H3V9.6h.1A1.7 1.7 0 0 0 4.6 8.5a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-.6 1.7 1.7 0 0 0 .4-1.1V3h4v.1A1.7 1.7 0 0 0 15.5 4.6a1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.13.38.34.72.6 1 .3.3.68.5 1.1.6h.1v4h-.1A1.7 1.7 0 0 0 19.4 15Z"/></Icon>; }

function SidebarNavigation({ active, onNavigate, onNewSession, onOpenCommandPalette }: Pick<Props, 'active' | 'onNavigate' | 'onNewSession' | 'onOpenCommandPalette'>): JSX.Element {
  return <>
      <button type="button" className="product-brand" onClick={() => onNavigate('tabs')} aria-label="Sessions workspace">
        <span className="product-brand-mark"><img src="/somewhere-logo.svg" alt="" /></span>
        <span className="product-brand-name"><span>Somewhere</span><strong>Sessions</strong></span>
      </button>

      <Button variant="ghost" type="button" className="product-new-session" aria-label="New session" title="New session" onClick={onNewSession}>
        <span className="product-add-icon" aria-hidden><Icon><path d="M12 5v14M5 12h14" /></Icon></span><span>New Session</span>
      </Button>

      <Button variant="ghost" type="button" className="product-command-button" aria-label="Commands" title="Commands (⌘K)" onClick={onOpenCommandPalette}>
        <Icon><path d="m5 7 5 5-5 5M13 17h6" /></Icon>
        <span>Commands</span>
        <kbd>⌘K</kbd>
      </Button>

      <nav className="product-nav" aria-label="Sessions">
        {ITEMS.map((item) => (
          <Button variant="ghost"
            key={item.id}
            aria-label={item.label} title={item.label}
            type="button"
            className={`product-nav-item${active === item.id ? ' is-active' : ''}`}
            onClick={() => onNavigate(item.id)}
            aria-current={active === item.id ? 'page' : undefined}
          >
            <span className="product-nav-icon" aria-hidden>{item.icon}</span>
            <span>{item.label}</span>
          </Button>
        ))}
      </nav>

  </>;
}

function SunIcon(): JSX.Element { return <Icon><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" /></Icon>; }
function MoonIcon(): JSX.Element { return <Icon><path d="M20 15.5A8.5 8.5 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5Z" /></Icon>; }

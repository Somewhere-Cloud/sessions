import { FooterUsage, type FooterUsageProps } from './FooterUsage';
import { Button } from './ui/Button';
import { useEffect, useState, type ReactNode } from 'react';
import {
  checkForNativeUpdate,
  installNativeUpdate,
  isTauri,
  notifyNativeUpdate,
  notifyProviderUpdate,
  type NativeUpdateInfo
} from '../lib/tauriBridge';
import { fetchProviderStatuses, updateProvider, type ProviderStatus } from '../api/sessionsd';
import { serverDisplayName, useServers } from '../lib/servers';

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

const UPDATE_CHECK_KEY = 'sessions:native-update-check-at';
const UPDATE_NOTIFIED_KEY = 'sessions:native-update-notified-version';
const PROVIDER_UPDATE_NOTIFIED_KEY = 'sessions:provider-update-notified:';
const UPDATE_CHECK_INTERVAL = 6 * 60 * 60 * 1000;

export function ProductSidebar({ active, theme, onNavigate, onNewSession, onOpenCommandPalette, onToggleTheme, usageScope }: Props): JSX.Element {
  const activeServer = useServers((state) => state.servers.find((server) => server.id === state.activeId));
  const activeServerKey = activeServer
    ? `${activeServer.id}|${activeServer.scheme ?? 'http'}|${activeServer.host}|${activeServer.port}|${activeServer.token ?? ''}`
    : '';
  const providerTarget = activeServer ? serverDisplayName(activeServer, true) : 'this computer';
  const [updateInfo, setUpdateInfo] = useState<NativeUpdateInfo | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);
  const [updateError, setUpdateError] = useState<string | null>(null);
  const [providers, setProviders] = useState<ProviderStatus[]>([]);
  const [providerBusy, setProviderBusy] = useState<ProviderStatus['id'] | null>(null);
  const [providerUpdateError, setProviderUpdateError] = useState<string | null>(null);

  useEffect(() => {
    if (!isTauri()) return;
    let cancelled = false;
    const automaticCheck = async (): Promise<void> => {
      let last = 0;
      try { last = Number(window.localStorage.getItem(UPDATE_CHECK_KEY) ?? 0); } catch { /* ignore */ }
      if (Date.now() - last < UPDATE_CHECK_INTERVAL) return;
      try {
        const available = await checkForNativeUpdate();
        if (cancelled) return;
        try { window.localStorage.setItem(UPDATE_CHECK_KEY, String(Date.now())); } catch { /* ignore */ }
        setUpdateInfo(available);
        if (!available) return;
        let notified = '';
        try { notified = window.localStorage.getItem(UPDATE_NOTIFIED_KEY) ?? ''; } catch { /* ignore */ }
        if (notified !== available.version) {
          await notifyNativeUpdate(available).catch(() => { /* sidebar remains authoritative */ });
          try { window.localStorage.setItem(UPDATE_NOTIFIED_KEY, available.version); } catch { /* ignore */ }
        }
      } catch {
        // Automatic checks stay silent. Manual checks remain available in
        // Settings → Notifications & updates.
      }
    };
    const startup = window.setTimeout(() => void automaticCheck(), 1_500);
    const interval = window.setInterval(() => void automaticCheck(), UPDATE_CHECK_INTERVAL);
    return () => { cancelled = true; window.clearTimeout(startup); window.clearInterval(interval); };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setProviders([]);
    setProviderUpdateError(null);
    const timer = window.setTimeout(() => {
      void fetchProviderStatuses(controller.signal).then((statuses) => {
        setProviders(statuses);
        if (!isTauri()) return;
        for (const provider of statuses) {
          if (!provider.updateAvailable || !provider.latestVersion) continue;
          const key = `${PROVIDER_UPDATE_NOTIFIED_KEY}${activeServer?.id ?? 'unknown'}:${provider.id}`;
          let notified = '';
          try { notified = window.localStorage.getItem(key) ?? ''; } catch { /* ignore */ }
          if (notified === provider.latestVersion) continue;
          void notifyProviderUpdate(provider.name, provider.latestVersion)
            .then(() => { try { window.localStorage.setItem(key, provider.latestVersion!); } catch { /* ignore */ } })
            .catch(() => { /* sidebar remains authoritative */ });
        }
      }).catch(() => { /* older runtime */ });
    }, 1_000);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [activeServerKey, activeServer?.id]);

  const installUpdate = async (): Promise<void> => {
    if (!updateInfo || updateBusy) return;
    setUpdateBusy(true);
    setUpdateError(null);
    try {
      await installNativeUpdate();
    } catch (error) {
      setUpdateError(error instanceof Error ? error.message : 'Could not install the update');
      setUpdateBusy(false);
    }
  };
  const providerUpdate = providers.find((provider) => provider.updateAvailable);
  const installProviderUpdate = async (provider: ProviderStatus): Promise<void> => {
    if (providerBusy) return;
    setProviderBusy(provider.id);
    setProviderUpdateError(null);
    try {
      const result = await updateProvider(provider.id);
      setProviders((current) => current.map((item) => item.id === provider.id ? result.provider : item));
    } catch (error) {
      setProviderUpdateError(error instanceof Error ? error.message : `Could not update ${provider.name}`);
    } finally {
      setProviderBusy(null);
    }
  };

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
        {updateInfo ? (
          <div className="product-update-card">
            <div><span>Sessions {updateInfo.version}</span><strong>Update available</strong></div>
            <Button variant="ghost" type="button" disabled={updateBusy} onClick={() => void installUpdate()}>
              {updateBusy ? 'Updating…' : 'Update app'}
            </Button>
            <small className="product-update-safe">Your sessions will keep running during the update.</small>
            {updateError ? <small role="alert">{updateError}</small> : null}
          </div>
        ) : null}
        {providerUpdate ? (
          <div className="product-update-card is-provider">
            <div><span>{providerUpdate.name} {providerUpdate.latestVersion}</span><strong>Update available</strong></div>
            <Button variant="ghost"
              type="button"
              aria-label={`Update ${providerUpdate.name} on ${providerTarget}`}
              disabled={providerBusy !== null}
              onClick={() => void installProviderUpdate(providerUpdate)}
            >
              {providerBusy === providerUpdate.id ? 'Installing…' : 'Update'}
            </Button>
            <small>
              {providerBusy === providerUpdate.id
                ? `${providerTarget} · This can take a few minutes.`
                : `${providerTarget} · Running work keeps going.`}
            </small>
          </div>
        ) : null}
        {providerUpdateError ? <small className="product-update-error" role="alert">{providerUpdateError}</small> : null}
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

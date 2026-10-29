import { PageHeader, Section as SettingsSection, Status, Surface } from './ui/Layout';
import { Input, Select } from './ui/Field';
import { Button } from './ui/Button';
import { Suspense, lazy, useEffect, useRef, useState } from 'react';
import {
  fetchAISettings,
  fetchClaudeSettings,
  fetchOnboardingState,
  fetchProviderStatuses,
  updateAISettings,
  updateClaudeSettings,
  updateOnboardingPreference,
  updateProvider,
  type AIProvider,
  type ProviderStatus
} from '../api/sessionsd';
import type { ClaudeSettings } from '../types';
import { serverDisplayName, useServers } from '../lib/servers';
import {
  checkForNativeUpdate,
  getNativeSupportPreview,
  installNativeUpdate,
  isTauri,
  openSupportPage,
  type NativeUpdateInfo,
  type NativeUpdateProgress,
  type SupportPage,
  type SupportPreview
} from '../lib/tauriBridge';
import { copyText } from '../lib/copyText';
import { sizeLabel, type TextSize } from '../lib/textSize';
const ConnectionsView = lazy(() => import('./ConnectionsView').then((module) => ({ default: module.ConnectionsView })));
import type { ThemeMode } from './ProductSidebar';
import { SomewhereCard } from './SomewhereCard';
import { useSessions } from '../store/sessions';
import { FleetProviderUpdates } from './FleetProviderUpdates';
import { NotificationDeliveryStatus } from './NotificationDeliveryStatus';
import { NativeRuntimeStatusCard } from './NativeRuntimeStatusCard';
import { AutoArchiveCard } from './AutoArchiveCard';
import { IdleCompactionCard } from './IdleCompactionCard';

const StorageView = lazy(() => import('./StorageView').then((module) => ({ default: module.StorageView })));

type Section = 'general' | 'agents' | 'fleet' | 'cloud' | 'notifications' | 'support' | 'storage';

interface Props {
  clientOnly?: boolean;
  hostName?: string;
  theme: ThemeMode;
  onThemeChange: (theme: ThemeMode) => void;
  textSize: TextSize;
  onTextSizeChange: (size: TextSize) => void;
  initialSection?: Section;
}

export function SettingsView({ clientOnly = false, hostName, theme, onThemeChange, textSize, onTextSizeChange, initialSection = 'general' }: Props): JSX.Element {
  const activeServerId = useServers((state) => state.activeId);
  const activeServer = useServers((state) =>
    state.servers.find((server) => server.id === state.activeId)
  );
  const activeServerIsLocal = useServers((state) =>
    state.servers.find((server) => server.id === state.activeId)?.isDefault === true
  );
  const sessions = useSessions((state) => state.sessions);
  const workingAgents = activeServerIsLocal
    ? sessions.filter((session) => !session.exited && session.working).length
    : null;
  const liveRunners = activeServerIsLocal
    ? sessions.filter((session) => !session.exited).length
    : null;
  const native = isTauri();
  const providerUpdateTarget = activeServer ? serverDisplayName(activeServer, true) : 'this computer';
  const machineName = hostName || providerUpdateTarget;
  const [section, setSection] = useState<Section>(initialSection);
  const [aiProvider, setAIProvider] = useState<AIProvider>('codex');
  const [aiBusy, setAIBusy] = useState(false);
  const [aiAvailable, setAIAvailable] = useState(true);
  const [aiMessage, setAIMessage] = useState<string | null>(null);
  const [claudeSettings, setClaudeSettings] = useState<ClaudeSettings>({
    remoteControl: 'off', permissionMode: 'inherit', model: '', effort: 'inherit',
    chrome: 'inherit', somewhereMcp: 'inherit', remoteControlNamePrefix: ''
  });
  const [claudeBusy, setClaudeBusy] = useState(false);
  const [claudeAvailable, setClaudeAvailable] = useState(true);
  const [claudeMessage, setClaudeMessage] = useState<string | null>(null);
  const [delegatedAccess, setDelegatedAccess] = useState<'inherit' | 'autonomous'>('inherit');
  const [delegationBusy, setDelegationBusy] = useState(false);
  const [delegationAvailable, setDelegationAvailable] = useState(true);
  const [delegationMessage, setDelegationMessage] = useState<string | null>(null);
  const [updateInfo, setUpdateInfo] = useState<NativeUpdateInfo | null>(null);
  const [updateProgress, setUpdateProgress] = useState<NativeUpdateProgress | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);
  const [updateMessage, setUpdateMessage] = useState<string | null>(null);
  const [providerStatuses, setProviderStatuses] = useState<ProviderStatus[]>([]);
  const [providerBusy, setProviderBusy] = useState<ProviderStatus['id'] | null>(null);
  const [providerMessage, setProviderMessage] = useState<string | null>(null);
  const aiGeneration = useRef(0);
  const claudeGeneration = useRef(0);

  useEffect(() => {
    setSection(initialSection);
  }, [initialSection]);

  useEffect(() => {
    const controller = new AbortController();
    void fetchProviderStatuses(controller.signal).then(setProviderStatuses).catch(() => {
      if (!controller.signal.aborted) setProviderStatuses([]);
    });
    const nextClaude = claudeGeneration.current + 1;
    claudeGeneration.current = nextClaude;
    setClaudeBusy(false);
    setClaudeAvailable(true);
    setClaudeMessage(null);
    void fetchClaudeSettings(controller.signal)
      .then((settings) => {
        if (claudeGeneration.current === nextClaude) setClaudeSettings(settings);
      })
      .catch(() => {
        if (!controller.signal.aborted && claudeGeneration.current === nextClaude) {
          setClaudeAvailable(false);
          setClaudeMessage('Claude defaults require a current Sessions runtime.');
        }
      });
    setDelegationBusy(false);
    setDelegationAvailable(true);
    setDelegationMessage(null);
    void fetchOnboardingState(controller.signal)
      .then((value) => {
        setDelegatedAccess(value.delegatedAccess === 'autonomous' ? 'autonomous' : 'inherit');
        setDelegationAvailable(value.supported !== false);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setDelegatedAccess('inherit');
          setDelegationAvailable(false);
          setDelegationMessage('Delegated access requires a current Sessions runtime.');
        }
      });
    if (!native) return () => controller.abort();

    const nextAI = aiGeneration.current + 1;
    aiGeneration.current = nextAI;
    setAIBusy(false);
    setAIAvailable(true);
    setAIMessage(null);
    void fetchAISettings(controller.signal)
      .then((settings) => {
        if (aiGeneration.current === nextAI) setAIProvider(settings.provider);
      })
      .catch(() => {
        if (!controller.signal.aborted && aiGeneration.current === nextAI) {
          setAIAvailable(false);
          setAIMessage('AI search requires a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, [activeServerId, native]);

  const saveAIProvider = async (provider: AIProvider): Promise<void> => {
    if (clientOnly || !native || aiBusy || !aiAvailable) return;
    const previous = aiProvider;
    const generation = aiGeneration.current + 1;
    aiGeneration.current = generation;
    setAIBusy(true);
    setAIProvider(provider);
    setAIMessage(null);
    try {
      const saved = await updateAISettings({ provider });
      if (aiGeneration.current !== generation) return;
      setAIProvider(saved.provider);
      setAIMessage('Smart search provider saved.');
    } catch (error) {
      if (aiGeneration.current === generation) {
        setAIProvider(previous);
        setAIMessage(error instanceof Error ? error.message : 'Could not save smart search settings.');
      }
    } finally {
      if (aiGeneration.current === generation) setAIBusy(false);
    }
  };

  const saveClaudeSettings = async (next: ClaudeSettings): Promise<void> => {
    if (clientOnly || claudeBusy || !claudeAvailable) return;
    const previous = claudeSettings;
    const generation = claudeGeneration.current + 1;
    claudeGeneration.current = generation;
    setClaudeBusy(true);
    setClaudeSettings(next);
    setClaudeMessage(null);
    try {
      const saved = await updateClaudeSettings(next);
      if (claudeGeneration.current !== generation) return;
      setClaudeSettings(saved);
      setClaudeMessage('Claude defaults saved. New sessions will use them.');
    } catch (error) {
      if (claudeGeneration.current === generation) {
        setClaudeSettings(previous);
        setClaudeMessage(error instanceof Error ? error.message : 'Could not save Claude defaults.');
      }
    } finally {
      if (claudeGeneration.current === generation) setClaudeBusy(false);
    }
  };

  const saveRemoteControlPreference = async (enabled: boolean): Promise<void> => {
    if (clientOnly || claudeBusy || !claudeAvailable) return;
    const previous = claudeSettings;
    const generation = claudeGeneration.current + 1;
    claudeGeneration.current = generation;
    setClaudeBusy(true);
    setClaudeSettings({ ...previous, remoteControl: enabled ? 'on' : 'off' });
    setClaudeMessage(null);
    try {
      await updateOnboardingPreference(enabled ? 'enabled' : 'local-only', delegatedAccess);
      const saved = await fetchClaudeSettings();
      if (claudeGeneration.current !== generation) return;
      setClaudeSettings(saved);
      setClaudeMessage(enabled
        ? 'Remote Control will be available for new Claude sessions.'
        : 'New Claude sessions will stay local.');
    } catch (error) {
      if (claudeGeneration.current === generation) {
        setClaudeSettings(previous);
        setClaudeMessage(error instanceof Error ? error.message : 'Could not save Remote Control preference.');
      }
    } finally {
      if (claudeGeneration.current === generation) setClaudeBusy(false);
    }
  };

  const saveDelegatedAccess = async (access: 'inherit' | 'autonomous'): Promise<void> => {
    if (clientOnly || delegationBusy || !delegationAvailable) return;
    const previous = delegatedAccess;
    setDelegationBusy(true);
    setDelegatedAccess(access);
    setDelegationMessage(null);
    try {
      await updateOnboardingPreference(
        claudeSettings.remoteControl === 'on' ? 'enabled' : 'local-only',
        access
      );
      setDelegationMessage(access === 'autonomous'
        ? 'Agent-created task workers may use full access. Existing sessions are unchanged.'
        : 'Agent-created children inherit their manager’s permissions.');
    } catch (error) {
      setDelegatedAccess(previous);
      setDelegationMessage(error instanceof Error ? error.message : 'Could not save delegated access.');
    } finally {
      setDelegationBusy(false);
    }
  };

  const checkForUpdate = async (): Promise<void> => {
    if (!native || updateBusy) return;
    setUpdateBusy(true);
    setUpdateMessage(null);
    setUpdateProgress(null);
    try {
      const available = await checkForNativeUpdate();
      setUpdateInfo(available);
      setUpdateMessage(available ? `Sessions ${available.version} is available.` : 'Sessions is up to date.');
    } catch (error) {
      setUpdateInfo(null);
      setUpdateMessage(error instanceof Error ? error.message : 'Could not check for updates.');
    } finally {
      setUpdateBusy(false);
    }
  };

  const installUpdate = async (): Promise<void> => {
    if (!native || !updateInfo || updateBusy) return;
    setUpdateBusy(true);
    setUpdateMessage('Downloading update…');
    try {
      await installNativeUpdate((progress) => {
        setUpdateProgress(progress);
        if (progress.totalBytes) {
          const percent = Math.min(100, Math.round((progress.downloadedBytes / progress.totalBytes) * 100));
          setUpdateMessage(`Downloading update… ${percent}%`);
        }
      });
    } catch (error) {
      setUpdateMessage(error instanceof Error ? error.message : 'Could not install update.');
      setUpdateBusy(false);
    }
  };

  const installProvider = async (provider: ProviderStatus): Promise<void> => {
    if (clientOnly || providerBusy) return;
    setProviderBusy(provider.id);
    setProviderMessage(`Updating ${provider.name} on ${providerUpdateTarget} with its official updater…`);
    try {
      const result = await updateProvider(provider.id);
      setProviderStatuses((current) => current.map((item) => item.id === provider.id ? result.provider : item));
      setProviderMessage(`${provider.name} ${result.provider.version || ''} is ready on ${providerUpdateTarget}. Running agents were not restarted.`);
    } catch (error) {
      setProviderMessage(error instanceof Error ? error.message : `Could not update ${provider.name}.`);
    } finally {
      setProviderBusy(null);
    }
  };

  return (
    <div className="settings-view">
      <aside className="settings-sections">
        <PageHeader title="Settings" level={2} />
        {[
          ['general', 'General'],
          ['agents', 'Agents & models'],
          ['fleet', 'Fleet'],
          ['cloud', 'Cloud & backup'],
          ['storage', 'Storage'],
          ['notifications', 'Notifications & updates'],
          ['support', 'Help & feedback']
        ].map(([id, label]) => (
          <Button variant="ghost" type="button" key={id} aria-current={section === id ? 'page' : undefined} className={section === id ? 'is-active' : ''} onClick={() => setSection(id as Section)}>{label}</Button>
        ))}
      </aside>
      <main className="settings-panel">
        {section === 'general' ? (
          <GeneralSettings
            clientOnly={clientOnly}
            theme={theme}
            onThemeChange={onThemeChange}
            textSize={textSize}
            onTextSizeChange={onTextSizeChange}
          />
        ) : section === 'agents' ? (
          <AgentSettings
            clientOnly={clientOnly}
            hostName={machineName}
            native={native}
            aiProvider={aiProvider}
            aiBusy={aiBusy}
            aiAvailable={aiAvailable}
            aiMessage={aiMessage}
            claudeSettings={claudeSettings}
            claudeBusy={claudeBusy}
            claudeAvailable={claudeAvailable}
            claudeMessage={claudeMessage}
            delegatedAccess={delegatedAccess}
            delegationBusy={delegationBusy}
            delegationAvailable={delegationAvailable}
            delegationMessage={delegationMessage}
            onAIProvider={saveAIProvider}
            onClaudeSettings={saveClaudeSettings}
            onRemoteControl={saveRemoteControlPreference}
            onDelegatedAccess={saveDelegatedAccess}
            onClaudeDraft={setClaudeSettings}
          />
        ) : section === 'fleet' ? (
          <Suspense fallback={<p role="status">Loading connections…</p>}><ConnectionsView clientOnly={clientOnly} hostName={machineName} /></Suspense>
        ) : section === 'storage' ? (
          <Suspense fallback={<p role="status">Loading storage…</p>}><StorageView key={activeServerId} /></Suspense>
        ) : section === 'cloud' ? (
          <section className="settings-page settings-cloud-page">
            <PageHeader eyebrow={<>Somewhere account</>} title={<>Cloud & backup</>} description={<>Encrypted local-first backup is available now. Hosted library, search, and worker controls are clearly staged below.</>} />
            <SomewhereCard clientOnly={clientOnly} hostName={machineName} />
          </section>
        ) : section === 'notifications' ? (
          <NotificationSettings
            clientOnly={clientOnly}
            hostName={machineName}
            native={native}
            updateInfo={updateInfo}
            updateProgress={updateProgress}
            updateBusy={updateBusy}
            updateMessage={updateMessage}
            workingAgents={workingAgents}
            liveRunners={liveRunners}
            providers={providerStatuses}
            providerBusy={providerBusy}
            providerMessage={providerMessage}
            providerUpdateTarget={providerUpdateTarget}
            onCheck={checkForUpdate}
            onInstall={installUpdate}
            onProviderUpdate={installProvider}
          />
        ) : (
          <SupportSettings native={native} />
        )}
      </main>
    </div>
  );
}

function GeneralSettings({ clientOnly = false, theme, onThemeChange, textSize, onTextSizeChange }: Props): JSX.Element {
  return (
    <section className="settings-page">
      <PageHeader eyebrow={<>Sessions app</>} title={<>General</>} description={<>Choose how the operations inbox looks and behaves on {clientOnly ? 'this device' : 'this Mac'}.</>} />
      {!clientOnly && <NativeRuntimeStatusCard />}
      <Surface><SettingsSection title={<>View</>} description={<>Scale the complete interface, including navigation, conversations, settings, and controls.</>}>
        <div className="view-size-choice" role="radiogroup" aria-label="Interface size">
          {(['S', 'M', 'L'] as const).map((size) => (
            <Button
              type="button"
              role="radio"
              aria-checked={textSize === size}
              className={textSize === size ? 'is-active' : ''}
              key={size}
              onClick={() => onTextSizeChange(size)}
            >
              <span className={`view-size-glyph is-${size.toLowerCase()}`} aria-hidden>Aa</span>
              <span>{sizeLabel(size)}</span>
            </Button>
          ))}
        </div>
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Appearance</>}>
        <div className="theme-switch-row">
          <Button type="button" className={theme === 'light' ? 'is-active' : ''} onClick={() => onThemeChange('light')}>Light</Button>
          <Button
            type="button"
            className="theme-switch"
            role="switch"
            aria-label="Use dark appearance"
            aria-checked={theme === 'dark'}
            onClick={() => onThemeChange(theme === 'dark' ? 'light' : 'dark')}
          >
            <span />
          </Button>
          <Button type="button" className={theme === 'dark' ? 'is-active' : ''} onClick={() => onThemeChange('dark')}>Dark</Button>
        </div>
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Session workspace</>}>
        <label className="settings-toggle"><span><strong>Collapse finished children</strong><small>Keeps manager sessions readable by grouping completed delegates.</small></span><input type="checkbox" checked readOnly /></label>
        <label className="settings-toggle"><span><strong>Five pinned managers</strong><small>Pin the conversations you use to coordinate everything else.</small></span><input type="checkbox" checked readOnly /></label>
      </SettingsSection></Surface>
    </section>
  );
}

interface AgentSettingsProps {
  clientOnly: boolean;
  hostName: string;
  native: boolean;
  aiProvider: AIProvider;
  aiBusy: boolean;
  aiAvailable: boolean;
  aiMessage: string | null;
  onAIProvider: (provider: AIProvider) => Promise<void>;
  claudeSettings: ClaudeSettings;
  claudeBusy: boolean;
  claudeAvailable: boolean;
  claudeMessage: string | null;
  delegatedAccess: 'inherit' | 'autonomous';
  delegationBusy: boolean;
  delegationAvailable: boolean;
  delegationMessage: string | null;
  onClaudeSettings: (settings: ClaudeSettings) => Promise<void>;
  onRemoteControl: (enabled: boolean) => Promise<void>;
  onDelegatedAccess: (access: 'inherit' | 'autonomous') => Promise<void>;
  onClaudeDraft: (settings: ClaudeSettings) => void;
}

function HostChoice({ hostName }: { hostName: string }): JSX.Element {
  return <div className="settings-host-choice">Chosen on {hostName}</div>;
}

function AgentSettings(props: AgentSettingsProps): JSX.Element {
  return (
    <section className="settings-page">
      <PageHeader eyebrow={<>Local, explicit calls</>} title={<>Agents & models</>} description={<>Choose which already-authenticated local agent powers smart search.</>} />
      {props.clientOnly ? <HostChoice hostName={props.hostName} /> : null}
      {!props.native ? <Status live="off">These controls are available only in the signed Sessions app.</Status> : null}
      <Surface><SettingsSection title={<>Claude session defaults</>} description={<>Applied only to sessions launched by Sessions. Claude’s own settings files remain untouched.</>}>
        <label className="settings-select-row"><span><strong>Remote Control</strong><small>When enabled, new Claude sessions connect directly to Anthropic and also appear on claude.ai and mobile. Sessions is not a relay.</small></span><Select value={props.claudeSettings.remoteControl === 'on' ? 'on' : 'off'} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} onChange={(event) => void props.onRemoteControl(event.currentTarget.value === 'on')}><option value="on">Enabled for new Claude sessions</option><option value="off">Keep sessions local</option></Select></label>
        <label className="settings-select-row"><span><strong>Permission mode</strong><small>Claude default preserves the provider’s normal prompts. Bypass gives new Claude sessions full access.</small></span><Select value={props.claudeSettings.permissionMode} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} onChange={(event) => void props.onClaudeSettings({ ...props.claudeSettings, permissionMode: event.currentTarget.value as ClaudeSettings['permissionMode'] })}><option value="inherit">Claude default</option><option value="manual">Manual</option><option value="acceptEdits">Accept edits</option><option value="auto">Auto</option><option value="plan">Plan</option><option value="dontAsk">Don’t ask</option><option value="bypassPermissions">Bypass permissions</option></Select></label>
        <label className="settings-select-row"><span><strong>Model</strong><small>Leave blank to use Claude’s selected default model.</small></span><Input value={props.claudeSettings.model} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} maxLength={128} placeholder="Provider default" onChange={(event) => props.onClaudeDraft({ ...props.claudeSettings, model: event.currentTarget.value })} onBlur={() => void props.onClaudeSettings(props.claudeSettings)} onKeyDown={(event) => { if (event.key === 'Enter') event.currentTarget.blur(); }} /></label>
        <label className="settings-select-row"><span><strong>Effort</strong><small>Leave inherited unless you want every new Claude session to use the same effort.</small></span><Select value={props.claudeSettings.effort} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} onChange={(event) => void props.onClaudeSettings({ ...props.claudeSettings, effort: event.currentTarget.value as ClaudeSettings['effort'] })}><option value="inherit">Inherit from Claude</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="xhigh">Extra high</option><option value="max">Max</option></Select></label>
        <label className="settings-select-row"><span><strong>Chrome integration</strong><small>Explicitly enable or disable Claude in Chrome for Sessions launches.</small></span><Select value={props.claudeSettings.chrome} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} onChange={(event) => void props.onClaudeSettings({ ...props.claudeSettings, chrome: event.currentTarget.value as ClaudeSettings['chrome'] })}><option value="inherit">Inherit from Claude</option><option value="on">On</option><option value="off">Off</option></Select></label>
        <label className="settings-select-row"><span><strong>Somewhere MCP</strong><small>Adopts an existing equivalent registration; otherwise launches the local `somewhere mcp` adapter without copying its credential.</small></span><Select value={props.claudeSettings.somewhereMcp} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} onChange={(event) => void props.onClaudeSettings({ ...props.claudeSettings, somewhereMcp: event.currentTarget.value as ClaudeSettings['somewhereMcp'] })}><option value="inherit">Use Claude configuration</option><option value="ensure">Ensure enabled</option></Select></label>
        <label className="settings-select-row"><span><strong>Remote Control name prefix</strong><small>Optional label prefix for sessions visible on claude.ai.</small></span><Input value={props.claudeSettings.remoteControlNamePrefix} disabled={props.clientOnly || props.claudeBusy || !props.claudeAvailable} maxLength={64} placeholder="This Mac" onChange={(event) => props.onClaudeDraft({ ...props.claudeSettings, remoteControlNamePrefix: event.currentTarget.value })} onBlur={() => void props.onClaudeSettings(props.claudeSettings)} onKeyDown={(event) => { if (event.key === 'Enter') event.currentTarget.blur(); }} /></label>
        {props.claudeMessage ? <Status>{props.claudeMessage}</Status> : null}
      </SettingsSection></Surface>
      <AutoArchiveCard hostName={props.hostName} />
      <IdleCompactionCard clientOnly={props.clientOnly} hostName={props.hostName} showHostChoice={false} />
      <Surface><SettingsSection title={<>Delegated agent access</>} description={<>Applies only when one Sessions agent starts another. It never changes an existing session or lets a child escalate itself.</>}>
        <label className="settings-select-row"><span><strong>Child-agent permissions</strong><small>Inherited keeps each worker inside its manager’s access. Autonomous is an explicit opt-in for unattended delegated work.</small></span><Select value={props.delegatedAccess} disabled={props.clientOnly || props.delegationBusy || !props.delegationAvailable} onChange={(event) => void props.onDelegatedAccess(event.currentTarget.value as 'inherit' | 'autonomous')}><option value="inherit">Inherit manager permissions</option><option value="autonomous">Autonomous delegated work</option></Select></label>
        <Status live="off">Children stay open unless a caller creates an explicit bounded task. A final response never ends a runtime. Approval questions stay open under Waiting for you.</Status>
        {props.delegationMessage ? <Status>{props.delegationMessage}</Status> : null}
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Smart search</>}>
        <label className="settings-select-row">
          <span><strong>Planning provider</strong><small>The natural-language query is sent; Sessions then searches its local index.</small></span>
          <Select
            value={props.aiProvider}
            disabled={props.clientOnly || !props.native || props.aiBusy || !props.aiAvailable}
            onChange={(event) => void props.onAIProvider(event.currentTarget.value as AIProvider)}
          ><option value="codex">Codex</option><option value="claude">Claude</option></Select>
        </label>
        {props.aiMessage ? <Status live="off">{props.aiMessage}</Status> : null}
      </SettingsSection></Surface>
    </section>
  );
}

interface NotificationSettingsProps {
  clientOnly: boolean;
  hostName: string;
  native: boolean;
  updateInfo: NativeUpdateInfo | null;
  updateProgress: NativeUpdateProgress | null;
  updateBusy: boolean;
  updateMessage: string | null;
  workingAgents: number | null;
  liveRunners: number | null;
  providers: ProviderStatus[];
  providerBusy: ProviderStatus['id'] | null;
  providerMessage: string | null;
  providerUpdateTarget: string;
  onCheck: () => Promise<void>;
  onInstall: () => Promise<void>;
  onProviderUpdate: (provider: ProviderStatus) => Promise<void>;
}

function NotificationSettings(props: NotificationSettingsProps): JSX.Element {
  const serverId = useServers((state) => state.activeId);
  return (
    <section className="settings-page">
      <PageHeader eyebrow={<>Signed {props.clientOnly ? 'device' : 'desktop'} delivery</>} title={<>Notifications & updates</>} description={<>Sessions checks the signed release feed automatically, while installation always stays an explicit action.</>} />
      <NotificationDeliveryStatus key={serverId} serverId={serverId} hostName={props.providerUpdateTarget} />
      <Surface><SettingsSection title={<>Sessions updates</>}>
        <div className="settings-static-row">
          <span>
            <strong>{props.updateInfo ? `Sessions ${props.updateInfo.version} is ready` : 'Signed release channel'}</strong>
            <small>
              {props.workingAgents === null
                ? 'This updates the client. Agents and open sessions on every host continue independently.'
                : props.workingAgents > 0
                ? `${props.workingAgents} ${props.workingAgents === 1 ? 'agent is' : 'agents are'} working. They continue on their current runners; new sessions use the updated runner after the service refresh.`
                : `No agents are working. Update now without losing ${props.liveRunners === 1 ? 'your open session' : 'any open sessions'}.`}
            </small>
          </span>
          {props.updateInfo ? (
            <Button type="button" variant="primary" disabled={!props.native || props.updateBusy} onClick={() => void props.onInstall()}>
              {props.workingAgents === null
                ? 'Update client safely'
                : props.workingAgents > 0
                ? `Update safely · ${props.workingAgents} continue`
                : 'Update now'}
            </Button>
          ) : (
            <Button type="button" variant="ghost" disabled={!props.native || props.updateBusy} onClick={() => void props.onCheck()}>{props.updateBusy ? 'Checking…' : 'Check now'}</Button>
          )}
        </div>
        {!props.native ? <Status live="off">Update controls are available only in Sessions.app.</Status> : null}
        {props.updateMessage ? <Status>{props.updateMessage}</Status> : null}
        {props.updateBusy && props.updateProgress?.totalBytes ? <progress value={props.updateProgress.downloadedBytes} max={props.updateProgress.totalBytes} aria-label="Update download progress" /> : null}
        {props.updateInfo?.notes ? <p>{props.updateInfo.notes}</p> : null}
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Agent tools</>} description={<>Sessions reads local version metadata only. It does not contact provider update servers until you click an update button.</>}>
        {props.clientOnly ? <HostChoice hostName={props.hostName} /> : null}
        {props.providers.map((provider) => (
          <div className="settings-static-row" key={provider.id}>
            <span>
              <strong>{provider.name}</strong>
              <small>
                {!provider.installed
                  ? 'Not installed on this machine'
                  : provider.updateAvailable
                  ? `${provider.version || 'Unknown version'} installed · ${provider.latestVersion} available`
                  : `${provider.version || 'Installed'} · no locally reported update`}
              </small>
            </span>
            <Button type="button" className={provider.updateAvailable ? 'btn btn-primary' : 'btn btn-ghost'} disabled={props.clientOnly || !provider.installed || props.providerBusy !== null} onClick={() => void props.onProviderUpdate(provider)}>
              {props.providerBusy === provider.id
                ? `Updating on ${props.providerUpdateTarget}…`
                : provider.updateAvailable
                ? `Update ${provider.name} on ${props.providerUpdateTarget}`
                : `Check & update on ${props.providerUpdateTarget}`}
            </Button>
          </div>
        ))}
        {props.providerMessage ? <Status>{props.providerMessage}</Status> : null}
        <Status live="off">The action runs on {props.providerUpdateTarget} and replaces only that machine's CLI executable. Existing Claude and Codex processes continue unchanged; new sessions there use the updated version.</Status>
      </SettingsSection></Surface>
      <FleetProviderUpdates />
    </section>
  );
}

function formatSupportPreview(preview: SupportPreview): string {
  const diagnostics = preview.diagnostics;
  if (!diagnostics) return '';
  const daemon = diagnostics.daemon.reachable
    ? `reachable; ok=${diagnostics.daemon.ok}; version=${diagnostics.daemon.version ?? 'unknown'}; discovering=${diagnostics.daemon.discovering ?? false}; sessions=${diagnostics.daemon.sessions_loaded ?? 0}`
    : 'unreachable';
  return [
    'Sessions diagnostic preview',
    `Generated: ${diagnostics.generated_at}`,
    `CLI: ${diagnostics.cli_version}`,
    `Platform: ${diagnostics.os}/${diagnostics.arch}`,
    `Daemon: ${daemon}`,
    '',
    'Never included:',
    ...preview.excluded.map((item) => `- ${item}`),
    '',
    'Nothing was uploaded automatically.'
  ].join('\n');
}

function SupportSettings({ native }: { native: boolean }): JSX.Element {
  const [draft, setDraft] = useState('');
  const [preview, setPreview] = useState<SupportPreview | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [includePreview, setIncludePreview] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const loadPreview = async (): Promise<void> => {
    if (!native || previewBusy) return;
    setPreviewBusy(true);
    setMessage(null);
    try {
      const next = await getNativeSupportPreview();
      setPreview(next);
      setIncludePreview(true);
      setMessage('Diagnostic preview generated locally. Review it before including it.');
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not generate the diagnostic preview.');
    } finally {
      setPreviewBusy(false);
    }
  };

  const copyAndOpen = async (kind: Extract<SupportPage, 'feedback' | 'bug'>): Promise<void> => {
    const sections: string[] = [];
    if (draft.trim()) sections.push(draft.trim());
    if (includePreview && preview) sections.push(formatSupportPreview(preview));
    const copied = sections.length === 0 ? true : await copyText(sections.join('\n\n---\n\n'));
    try {
      await openSupportPage(kind);
      setMessage(
        sections.length === 0
          ? 'Ticket form opened. Nothing from Sessions was attached.'
          : copied
            ? 'Draft copied and ticket form opened. Review everything before submitting.'
            : 'Ticket form opened, but Sessions could not copy the draft. Copy it manually before submitting.'
      );
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not open the ticket form.');
    }
  };

  return (
    <section className="settings-page support-page">
      <PageHeader eyebrow={<>Help shape Sessions</>} title={<>Send feedback</>} description={<>Tell us what you love, what would make Sessions better, or what went wrong.</>} />
      <Surface><SettingsSection title={<>What would you like to share?</>}>
        <label className="support-draft">
          <span>Optional draft</span>
          <textarea
            value={draft}
            maxLength={4_000}
            placeholder="A feature you love, an idea, or what happened when something went wrong…"
            onChange={(event) => setDraft(event.currentTarget.value)}
          />
          <small>{draft.length.toLocaleString()} / 4,000 · This stays in the app until you copy it.</small>
        </label>
        <div className="support-actions">
          <Button type="button" variant="primary" onClick={() => void copyAndOpen('feedback')}>Share an idea or win</Button>
          <Button type="button" variant="ghost" onClick={() => void copyAndOpen('bug')}>Report a bug</Button>
        </div>
        <p className="support-privacy">Tickets are public GitHub issues. Sessions copies your draft to the clipboard and opens the form; it does not submit for you.</p>
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Optional diagnostic preview</>}>
        <div className="settings-static-row">
          <span><strong>Small and deliberately redacted</strong><small>Only versions, OS/architecture, daemon readiness, and a session count. No logs, paths, IDs, titles, commands, or content.</small></span>
          <Button type="button" variant="ghost" disabled={!native || previewBusy} onClick={() => void loadPreview()}>{previewBusy ? 'Generating…' : preview ? 'Refresh preview' : 'Generate preview'}</Button>
        </div>
        {!native ? <Status live="off">Diagnostic previews are available in the signed Sessions app. You can still open a ticket.</Status> : null}
        {preview ? (
          <>
            <label className="settings-toggle support-include">
              <span><strong>Include after review</strong><small>Add this preview when Sessions copies your ticket draft.</small></span>
              <input type="checkbox" checked={includePreview} onChange={(event) => setIncludePreview(event.currentTarget.checked)} />
            </label>
            <pre className="support-preview">{formatSupportPreview(preview)}</pre>
          </>
        ) : null}
      </SettingsSection></Surface>
      <Surface><SettingsSection title={<>Security issue</>}>
        <div className="settings-static-row">
          <span><strong>Report privately</strong><small>Use GitHub’s private vulnerability-reporting channel instead of a public support ticket.</small></span>
          <Button type="button" variant="ghost" onClick={() => void openSupportPage('security')}>Open private report</Button>
        </div>
      </SettingsSection></Surface>
      {message ? <Status>{message}</Status> : null}
    </section>
  );
}

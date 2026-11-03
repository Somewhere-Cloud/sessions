import { PageHeader, Surface } from './ui/Layout';
import { Button } from './ui/Button';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchLANState, requestLocalNetworkAccess, type LANState } from '../api/sessionsd';
import { connectionSettingsTarget } from '../lib/connectionSettingsTarget';
import { LOCAL_NETWORK_CLI_CHECK, localNetworkGuideMode, type LocalNetworkCheck } from '../lib/localNetworkGuide';
import { isLocalServer, serverDisplayName, useServers } from '../lib/servers';
import { isNativeMobileRuntime, isTauri, openLocalNetworkSettings } from '../lib/tauriBridge';

/** Permission belongs to the host, not necessarily the device displaying this guide. */
export function LocalNetworkGuide({ onState }: { onState?: (state: LANState) => void }): JSX.Element | null {
  useServers((state) => state.activeId);
  const target = connectionSettingsTarget();
  const local = isTauri() && !isNativeMobileRuntime() && isLocalServer(target);
  const name = local ? 'this Mac' : serverDisplayName(target, true);
  const [state, setState] = useState<LANState | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [lastCheck, setLastCheck] = useState<LocalNetworkCheck>('none');
  const [busy, setBusy] = useState(false);
  const sawUnproven = useRef(false);
  const checking = useRef(false);
  const onStateRef = useRef(onState);
  onStateRef.current = onState;
  const refresh = useCallback(async (signal: AbortSignal): Promise<void> => {
    const next = await fetchLANState(signal);
    if (signal.aborted) return;
    if (next.permission?.status === 'denied' || next.permission?.status === 'not-yet-asked' || next.permission?.status === 'blocked') sawUnproven.current = true;
    setState(next);
    onStateRef.current?.(next);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setState(null); setMessage(null); setLastCheck('none'); sawUnproven.current = false;
    const update = (): void => { void refresh(controller.signal).catch(() => {}); };
    update();
    window.addEventListener('focus', update);
    const interval = window.setInterval(update, 15_000);
    return () => { controller.abort(); window.removeEventListener('focus', update); window.clearInterval(interval); };
  }, [refresh, target.id]);
  const check = async (): Promise<void> => {
    if (checking.current) return;
    checking.current = true; setBusy(true); setMessage(null); setLastCheck('none');
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 8_000);
    try {
      // Only the local Mac's own daemon can browse for us; from a phone or
      // another machine this check just re-reads what the host last observed,
      // which proves nothing about this moment.
      const reached = local ? await requestLocalNetworkAccess(controller.signal) : null;
      await refresh(controller.signal);
      setLastCheck(reached === null ? 'none' : reached > 0 ? 'reached' : 'none-found');
      // An empty check is described by the not-found card itself.
      setMessage(reached === null ? 'Check complete. This re-read the host’s last observation.' : null);
    } catch {
      setLastCheck('failed');
      setMessage('Couldn’t confirm nearby access. You can still try your saved computer connection in Fleet. Your sessions have not been stopped.');
    } finally {
      window.clearTimeout(timeout); checking.current = false; setBusy(false);
    }
  };
  const openSettings = async (): Promise<void> => {
    try { await openLocalNetworkSettings(); setMessage('After turning on Sessions, come back here and choose Check again.'); }
    catch { setMessage('Open System Settings yourself, then follow the steps below.'); }
  };
  const checkAgain = <Button variant="ghost" type="button" disabled={busy} onClick={() => void check()}>{busy ? 'Checking…' : 'Check again'}</Button>;
  const mode = localNetworkGuideMode({ status: state?.permission?.status, sawUnproven: sawUnproven.current, lastCheck });
  if (mode === 'hidden') return null;
  if (mode === 'confirmed') return <div className="local-network-guide is-resolved" role="status">Nearby access is working on {name}. You can return to your projects.</div>;
  if (mode === 'previously-worked') return <div className="local-network-guide" role="status">
    <p>{name} last reported nearby access working. That is its most recent observation, not a live test — macOS does not let Sessions read the setting itself.</p>
    {message ? <p>{message}</p> : null}
    <div className="local-network-guide-actions">{checkAgain}</div>
  </div>;
  if (mode === 'unconfirmed' || mode === 'not-found') return <div className="local-network-guide" role="status">
    <p>{mode === 'not-found'
      ? `No nearby computer answered the check from ${name}. Computers that are off, asleep, or on another network don’t appear.`
      : `Nearby access hasn’t been confirmed on ${name} yet.`}</p>
    <p>If a computer you expect is on, run <code>{LOCAL_NETWORK_CLI_CHECK}</code> in Terminal on {name}. It checks each saved computer from outside Sessions and says so if macOS is blocking Sessions from the local network.</p>
    {message ? <p>{message}</p> : null}
    <div className="local-network-guide-actions">{checkAgain}</div>
  </div>;
  const endpoint = state?.permission?.endpoint;
  return <Surface role="region" className="local-network-guide" aria-label={`Nearby access on ${name}`}>
    <PageHeader level={3} title="macOS is blocking Sessions from the local network" />
    <p>A check from Terminal on <strong>{name}</strong> reached {endpoint ? <code>{endpoint}</code> : 'a nearby computer'}, but the Sessions background service could not. Your saved conversations stay here.</p>
    <ol>
      <li>On {name}, open <strong>System Settings → Privacy &amp; Security → Local Network</strong>.</li>
      <li>Turn on <strong>Sessions</strong>. If it is already on, turn it off and on again.</li>
      <li>Return to Sessions and choose <strong>Check again</strong>.</li>
    </ol>
    <div className="local-network-guide-actions">
      {local ? <Button type="button" onClick={() => void openSettings()}>Open System Settings</Button> : null}
      {checkAgain}
    </div>
    {message ? <p role="status">{message}</p> : null}
    <details><summary>Don’t see Sessions in the list?</summary>
      <p>{local ? 'Choose Check again to ask macOS for access. If a prompt appears, choose Allow.' : `Open Sessions on ${name}, go to Fleet, and choose Check again. Approve the macOS prompt there.`} If no prompt appears, reopen the Sessions app and try again. Closing the app does not stop your agents.</p>
    </details>
    <details><summary>Both computers use Tailscale?</summary>
      <p>Sessions prefers a Tailscale route when both computers have one, and macOS does not apply this setting to Tailscale. Your saved computer may still connect that way.</p>
    </details>
  </Surface>;
}

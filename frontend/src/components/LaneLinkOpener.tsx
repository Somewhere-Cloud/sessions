import { useEffect, useRef, useState } from 'react';
import { fetchServerMachineIdentity } from '../api/sessionsd';
import { laneLinkTarget, OPEN_LANE_EVENT, resolveLaneLinkServerId, UNKNOWN_LANE_MACHINE_NOTICE, type LaneLinkTarget } from '../lib/laneLink';
import { useServers, type ServerConfig } from '../lib/servers';
import { isTauri, onNativeLaneLink, takeNativeLaneLink } from '../lib/tauriBridge';
import { readWindowScope } from '../lib/windowScope';

const IDENTITY_TIMEOUT_MS = 5_000;

async function identify(server: ServerConfig): Promise<string | null> {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), IDENTITY_TIMEOUT_MS);
  try {
    return (await fetchServerMachineIdentity(server, controller.signal)).machineId;
  } finally {
    window.clearTimeout(timer);
  }
}

// LaneLinkOpener opens the lane a sessions://lane link names, exactly as
// choosing it in the navigator does: it switches to the owning machine when
// that is another configured one, then opens the session. Only the main window
// takes links, so a scoped window never consumes one meant for it.
export function LaneLinkOpener({ onOpen }: {
  onOpen: (serverId: string, sessionId: string) => void;
}): JSX.Element | null {
  const [notice, setNotice] = useState<string | null>(null);
  const onOpenRef = useRef(onOpen);
  useEffect(() => { onOpenRef.current = onOpen; }, [onOpen]);

  useEffect(() => {
    if (readWindowScope() !== null) return;
    let stopped = false;
    let unlisten: (() => void) | null = null;
    const open = async (target: LaneLinkTarget | null): Promise<void> => {
      if (!target || stopped) return;
      const { servers, activeId } = useServers.getState();
      const serverId = await resolveLaneLinkServerId(target, servers, activeId, identify);
      if (stopped) return;
      if (!serverId) {
        setNotice(UNKNOWN_LANE_MACHINE_NOTICE);
        return;
      }
      setNotice(null);
      onOpenRef.current(serverId, target.sessionId);
    };
    const take = async (): Promise<void> => open(laneLinkTarget(await takeNativeLaneLink().catch(() => null)));
    const clicked = (event: Event): void => { void open(laneLinkTarget((event as CustomEvent<unknown>).detail)); };
    window.addEventListener(OPEN_LANE_EVENT, clicked);
    if (isTauri()) {
      void onNativeLaneLink(() => { void take(); }).then((stop) => {
        if (stopped) stop();
        else unlisten = stop;
      }).catch(() => {});
      void take();
    }
    return () => {
      stopped = true;
      window.removeEventListener(OPEN_LANE_EVENT, clicked);
      unlisten?.();
    };
  }, []);

  if (!notice) return null;
  return (
    <section className="client-onboarding-note" role="status" aria-live="polite">
      <span>{notice}</span>
      <button type="button" className="btn" onClick={() => setNotice(null)}>Dismiss</button>
    </section>
  );
}

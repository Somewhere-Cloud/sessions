import { useEffect, useRef, useState } from 'react';
import { attentionArrivals, shouldMarkSeen } from '../lib/readState';
import { setSessionUnread } from '../store/sessionActions';
import { useSessions } from '../store/sessions';
import type { SessionInfo } from '../types';

// A burst of sessions finishing together becomes one toast.
const COALESCE_MS = 1_500;

function pageFocused(): boolean {
  return document.visibilityState === 'visible' && document.hasFocus();
}

/** Re-renders when the window gains or loses focus or visibility. */
function usePageFocus(): boolean {
  const [focused, setFocused] = useState(pageFocused);
  useEffect(() => {
    const update = (): void => setFocused(pageFocused());
    window.addEventListener('focus', update);
    window.addEventListener('blur', update);
    document.addEventListener('visibilitychange', update);
    return () => {
      window.removeEventListener('focus', update);
      window.removeEventListener('blur', update);
      document.removeEventListener('visibilitychange', update);
    };
  }, []);
  return focused;
}

/**
 * Stamp the session in view as seen: when it is opened, when the window comes
 * back to the front, and when a new turn lands while it is watched. An unread
 * mark the person just made on the session in view is left alone until they
 * open it again.
 */
function useMarkViewedSeen(viewing: SessionInfo | undefined, focused: boolean): void {
  const id = viewing?.id;
  const viewingRef = useRef(viewing);
  viewingRef.current = viewing;
  const openedRef = useRef<string | null>(null);
  const attentionAt = viewing?.attentionAt;
  const neverSeen = viewing?.seenAt == null;
  const supported = viewing?.unread !== undefined;
  useEffect(() => {
    const current = viewingRef.current;
    if (!id || !focused || !current) return;
    const opened = openedRef.current !== id;
    openedRef.current = id;
    if (!shouldMarkSeen(current) || (current.markedUnread && !opened)) return;
    void setSessionUnread(id, false).catch(() => { /* the next open tries again */ });
  }, [id, focused, attentionAt, neverSeen, supported]);
}

/**
 * Unread bookkeeping and in-app attention toasts for the active computer.
 * Toasts appear only while this window is in front: in the background the
 * daemon's desktop notification already spoke, and the unread dot is what is
 * waiting on return. A kind the person switched off for desktop notifications
 * stays quiet here too.
 */
export function useSessionAttention(viewingId: string | null, onOpen: (id: string) => void): void {
  const sessions = useSessions((state) => state.sessions);
  const serverId = useSessions((state) => state.serverId);
  const focused = usePageFocus();
  const viewing = viewingId ? sessions.find((session) => session.id === viewingId) : undefined;
  useMarkViewedSeen(viewing, focused);

  const markersRef = useRef<{ serverId: string | null; markers: Map<string, number> } | null>(null);
  const pendingRef = useRef<Map<string, SessionInfo>>(new Map());
  const timerRef = useRef<number | null>(null);
  const latest = useRef({ viewingId, onOpen });
  latest.current = { viewingId, onOpen };

  useEffect(() => {
    const previous = markersRef.current?.serverId === serverId ? markersRef.current.markers : null;
    const { arrivals, markers } = attentionArrivals(previous, sessions, viewingId);
    markersRef.current = { serverId, markers };
    if (arrivals.length === 0) return;
    for (const session of arrivals) pendingRef.current.set(session.id, session);
    if (timerRef.current !== null) return;
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      const ids = [...pendingRef.current.keys()];
      pendingRef.current.clear();
      if (!pageFocused()) return;
      void import('./attentionFlush').then(({ flushAttention }) => flushAttention(
        ids, serverId, () => latest.current.viewingId, pageFocused, (id) => latest.current.onOpen(id)
      )).catch(() => { /* the unread dot still says it */ });
    }, COALESCE_MS);
  }, [serverId, sessions, viewingId]);

  useEffect(() => () => {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current);
  }, []);
}

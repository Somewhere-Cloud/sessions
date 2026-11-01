import { fetchNotificationDeliveryStatus } from '../api/sessionsd';
import { attentionToast, toastWorthy, type AttentionSwitches } from '../lib/attentionToast';
import { useSessions } from '../store/sessions';
import { ATTENTION_TOAST_MS, showToast } from '../store/toasts';
import type { SessionInfo } from '../types';

// Loaded the first time a session other than the one in view moves.

async function readSwitches(serverId: string | null): Promise<AttentionSwitches | null> {
  if (!serverId) return null;
  try {
    return (await fetchNotificationDeliveryStatus(serverId)).notify;
  } catch {
    return null;
  }
}

/** Shows one toast for a coalesced burst, if anything in it still deserves one. */
export async function flushAttention(
  ids: string[],
  serverId: string | null,
  viewingId: () => string | null,
  stillFocused: () => boolean,
  open: (id: string) => void
): Promise<void> {
  const switches = await readSwitches(serverId);
  const state = useSessions.getState();
  if (state.serverId !== serverId || !stillFocused()) return;
  const pending = ids
    .map((id) => state.sessions.find((session) => session.id === id))
    .filter((session): session is SessionInfo => Boolean(session));
  const toast = attentionToast(toastWorthy(pending, viewingId(), switches));
  if (!toast) return;
  showToast({
    kind: 'attention', message: toast.message, detail: toast.detail, durationMs: ATTENTION_TOAST_MS,
    action: { label: 'Open', run: () => open(toast.target.id) }
  });
}

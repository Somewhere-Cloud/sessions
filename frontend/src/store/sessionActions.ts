import { updateReadState } from '../api/sessionsd/readState';
import type { SessionInfo } from '../types';
import type { SetAsideReturn } from '../api/sessionsd';
import { useSessions } from './sessions';

// The sidebar's reversible actions, each followed by an Undo that calls the
// daemon's own inverse operation. Only a person's click comes through here;
// an agent or the CLI changing the same mark never raises a toast. The toast
// itself is in ./undoOffers, loaded on first use.

function offer(message: (label: string) => string, id: string, reverse: () => Promise<unknown>, detail?: string): void {
  const serverId = useSessions.getState().serverId;
  void import('./undoOffers').then(({ offerUndo }) => offerUndo(serverId, message, id, reverse, detail));
}

/** Pin or unpin, then offer Undo. Signature matches the store action. */
export async function pinWithUndo(id: string, pinned: boolean): Promise<void> {
  await useSessions.getState().updatePinned(id, pinned);
  offer((label) => `${pinned ? 'Pinned' : 'Unpinned'} ${label}`, id, () => useSessions.getState().updatePinned(id, !pinned));
}

/** Set aside or restore, then offer Undo. Signature matches the store action. */
export async function setAsideWithUndo(id: string, setAside: boolean, until?: SetAsideReturn): Promise<void> {
  await useSessions.getState().updateSetAside(id, setAside, until);
  offer(
    (label) => `${setAside ? 'Set aside' : 'Restored'} ${label}`, id,
    () => useSessions.getState().updateSetAside(id, !setAside),
    setAside ? 'It keeps running. Drafts stay saved.' : undefined
  );
}

/** Closed-record archive has no daemon inverse, so it says where the record went instead. */
export function noteArchived(session: SessionInfo): void {
  void import('./undoOffers').then((module) => module.noteArchived(session));
}

const readStateUnsupported = new Set<string>();

/**
 * Seen (unread=false) or "Mark as unread" (unread=true), committed from the
 * daemon's answer. A daemon that predates read state is remembered so a client
 * does not ask it again on every open.
 */
export async function setSessionUnread(id: string, unread: boolean): Promise<boolean> {
  const serverId = useSessions.getState().serverId;
  if (!serverId || readStateUnsupported.has(serverId)) return false;
  const stored = await updateReadState(id, unread, serverId);
  if (!stored) {
    readStateUnsupported.add(serverId);
    return false;
  }
  useSessions.setState((state) => state.serverId !== serverId ? state : {
    sessions: state.sessions.map((session) => session.id === id
      ? { ...session, unread: stored.unread, seenAt: stored.seenAt, attentionAt: stored.attentionAt, markedUnread: unread }
      : session)
  });
  return true;
}

/** Mark as unread from the row menu, with Undo that marks it seen again. */
export async function markUnreadWithUndo(id: string): Promise<void> {
  if (!await setSessionUnread(id, true)) {
    throw new Error('This computer does not keep unread marks yet. Update Sessions there to use them.');
  }
  offer((label) => `Marked ${label} unread`, id, () => setSessionUnread(id, false));
}

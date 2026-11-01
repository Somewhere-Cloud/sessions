import { quietSessionTitle, resolvedSessionLabel } from '../lib/tabLabels';
import type { SessionInfo } from '../types';
import { useSessions } from './sessions';
import { showToast, UNDO_TOAST_MS } from './toasts';

// The words and the Undo wiring for the sidebar's reversible actions. Loaded
// on the first such action, so the entry bundle carries only the actions.

function labelOf(session: SessionInfo | undefined): string {
  return session ? quietSessionTitle(resolvedSessionLabel(session)) : 'Session';
}

function labelFor(id: string): string {
  return labelOf(useSessions.getState().sessions.find((candidate) => candidate.id === id));
}

export function offerUndo(
  serverId: string | null,
  message: (label: string) => string,
  id: string,
  reverse: () => Promise<unknown>,
  detail?: string
): void {
  showToast({
    kind: 'undo', message: message(labelFor(id)), detail, durationMs: UNDO_TOAST_MS,
    action: {
      label: 'Undo',
      run: async () => {
        // Undo belongs to the computer the action was taken on.
        if (useSessions.getState().serverId !== serverId) throw new Error('Switch back to that computer to undo this.');
        await reverse();
      }
    }
  });
}

export function noteArchived(session: SessionInfo): void {
  showToast({
    kind: 'notice', message: `Archived ${labelOf(session)}`,
    detail: 'The conversation stays in History.', durationMs: UNDO_TOAST_MS
  });
}

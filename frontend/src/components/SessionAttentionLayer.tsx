import { lazy, Suspense } from 'react';
import { useSessionAttention } from '../hooks/useSessionAttention';
import { useToasts } from '../store/toasts';

// The toast's markup and styles load the first time there is a toast to show,
// so the entry bundle carries only the store and the bookkeeping hook.
const ToastLayer = lazy(() => import('./ToastLayer').then((module) => ({ default: module.ToastLayer })));

/** Unread bookkeeping for the session in view, plus the app's one toast slot. */
export function SessionAttentionLayer({ viewingId, onOpen }: {
  viewingId: string | null;
  onOpen: (id: string) => void;
}): JSX.Element | null {
  useSessionAttention(viewingId, onOpen);
  const showing = useToasts((state) => state.toast !== null);
  return showing ? <Suspense fallback={null}><ToastLayer /></Suspense> : null;
}

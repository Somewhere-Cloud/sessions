import { useEffect, useRef, useState } from 'react';
import { showToast, UNDO_TOAST_MS, useToasts, type Toast } from '../store/toasts';
import '../styles/toasts.css';

function typingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || Boolean(target.closest('input, textarea, select, [contenteditable="true"], .xterm'));
}

/** Runs a toast's action once, and turns a failure into its own calm notice. */
function useToastAction(toast: Toast | null): [boolean, () => void] {
  const dismiss = useToasts((state) => state.dismiss);
  const [busy, setBusy] = useState(false);
  useEffect(() => setBusy(false), [toast?.id]);
  const run = (): void => {
    if (!toast?.action || busy) return;
    setBusy(true);
    Promise.resolve(toast.action.run()).then(() => dismiss(toast.id), (error: unknown) => {
      showToast({
        kind: 'notice', durationMs: UNDO_TOAST_MS,
        message: toast.kind === 'undo' ? 'Could not undo that' : 'Could not open that session',
        detail: error instanceof Error ? error.message : undefined
      });
    });
  };
  return [busy, run];
}

/**
 * The one toast slot. It dismisses itself after the toast's duration, holds
 * still while the pointer or keyboard focus is on it, closes on Escape, and
 * Cmd/Ctrl+Z runs an Undo when focus is not in a text field or terminal.
 */
export function ToastLayer(): JSX.Element | null {
  const toast = useToasts((state) => state.toast);
  const dismiss = useToasts((state) => state.dismiss);
  const [held, setHeld] = useState(false);
  const [busy, run] = useToastAction(toast);
  const runRef = useRef(run);
  runRef.current = run;

  useEffect(() => {
    if (!toast || held) return;
    const timer = window.setTimeout(() => dismiss(toast.id), toast.durationMs);
    return () => window.clearTimeout(timer);
  }, [toast, held, dismiss]);

  useEffect(() => {
    if (!toast) return;
    const onKey = (event: KeyboardEvent): void => {
      if (event.defaultPrevented) return;
      if (event.key === 'Escape' && !document.querySelector('[role="dialog"]')) {
        dismiss(toast.id);
        return;
      }
      if (toast.kind === 'undo' && (event.metaKey || event.ctrlKey) && !event.shiftKey && event.key.toLowerCase() === 'z'
        && !typingTarget(event.target)) {
        event.preventDefault();
        runRef.current();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [toast, dismiss]);

  useEffect(() => setHeld(false), [toast?.id]);
  if (!toast) return null;
  return (
    <div className={`app-toast is-${toast.kind}`} role="status" aria-live="polite"
      onMouseEnter={() => setHeld(true)} onMouseLeave={() => setHeld(false)}
      onFocus={() => setHeld(true)} onBlur={() => setHeld(false)}>
      <span className="app-toast-copy">
        <span className="app-toast-message">{toast.message}</span>
        {toast.detail ? <span className="app-toast-detail">{toast.detail}</span> : null}
      </span>
      {toast.action ? (
        <button type="button" className="app-toast-action" disabled={busy} onClick={run}
          title={toast.kind === 'undo' ? 'Undo (⌘Z)' : undefined}>
          {busy ? 'Working…' : toast.action.label}
        </button>
      ) : null}
      <button type="button" className="app-toast-dismiss" aria-label="Dismiss" title="Dismiss (Esc)" onClick={() => dismiss(toast.id)}>×</button>
    </div>
  );
}

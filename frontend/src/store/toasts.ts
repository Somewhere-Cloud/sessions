import { create } from 'zustand';

// One small toast at a time. A person's own reversible action owns the slot:
// its Undo is the only way back short of finding the row again, so a newer
// undo toast replaces whatever is showing, while a notice about another
// session never pushes an Undo off screen. That notice is not lost either --
// the row stays unread in the sidebar until it is opened.

export type ToastKind = 'undo' | 'notice' | 'attention';

export interface ToastAction {
  label: string;
  run: () => void | Promise<void>;
}

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
  detail?: string;
  action?: ToastAction;
  durationMs: number;
}

export const UNDO_TOAST_MS = 6_000;
export const ATTENTION_TOAST_MS = 6_000;

interface ToastState {
  toast: Toast | null;
  show: (toast: Omit<Toast, 'id'>) => number | null;
  dismiss: (id?: number) => void;
}

let nextToastId = 1;

export const useToasts = create<ToastState>((set, get) => ({
  toast: null,
  show: (toast) => {
    const current = get().toast;
    if (toast.kind === 'attention' && current?.kind === 'undo') return null;
    const id = nextToastId++;
    set({ toast: { ...toast, id } });
    return id;
  },
  dismiss: (id) => {
    const current = get().toast;
    if (!current || (id !== undefined && current.id !== id)) return;
    set({ toast: null });
  }
}));

export function showToast(toast: Omit<Toast, 'id'>): number | null {
  return useToasts.getState().show(toast);
}

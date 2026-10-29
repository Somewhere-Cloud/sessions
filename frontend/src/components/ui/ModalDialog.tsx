import { useSurfaceTheme } from './useSurfaceTheme';
import { createContext, useEffect, useId, useRef, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
  className: string;
  labelledBy: string;
  onClose: () => void;
  dismissible?: boolean;
  dismissOnBackdrop?: boolean;
}

// Portaled controls can stay outside clipping ancestors while sharing modal focus.
export const ModalScopeContext = createContext<string | undefined>(undefined);

// Only the foremost shared modal owns document-level keyboard and focus events.
const modalStack: HTMLElement[] = [];
const focusSelector = 'button, input, select, textarea, a[href], [tabindex], [contenteditable="true"], summary';

function scopeRoots(root: HTMLElement, scope: string): HTMLElement[] {
  return [root, ...Array.from(document.querySelectorAll<HTMLElement>('[data-modal-owner]'))
    .filter((element) => element.dataset.modalOwner === scope)];
}

function tabStops(roots: HTMLElement[]): HTMLElement[] {
  return roots.flatMap((root) => Array.from(root.querySelectorAll<HTMLElement>(focusSelector))
    .filter((element) => element.tabIndex >= 0 && !element.matches(':disabled')
      && !element.closest('[hidden], [inert]') && visible(element, root)))
    .sort((left, right) => (left.tabIndex || Infinity) - (right.tabIndex || Infinity));
}

function visible(element: HTMLElement, root: HTMLElement): boolean {
  for (let current: HTMLElement | null = element; current; current = current.parentElement) {
    if (current.tagName === 'DETAILS' && !current.hasAttribute('open')) {
      const summary = Array.from(current.children).find((child) => child.tagName === 'SUMMARY');
      if (!summary?.contains(element)) return false;
    }
    const style = getComputedStyle(current);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    if (current === root) return true;
  }
  return false;
}

/** Mounted while open. Keeps Sessions presentation separate from modal behavior. */
export function ModalDialog({ children, className, labelledBy, onClose, dismissible = true, dismissOnBackdrop = true }: Props): JSX.Element {
  const ref = useRef<HTMLElement>(null);
  const themeStyle = useSurfaceTheme(ref);
  const scope = useId();
  const current = useRef({ onClose, dismissible });
  current.current = { onClose, dismissible };
  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    modalStack.push(root);
    // Focus the review itself rather than a potentially destructive action.
    root.focus();
    const foremost = (): boolean => modalStack[modalStack.length - 1] === root;
    const keepFocus = (event: FocusEvent): void => {
      if (foremost() && event.target instanceof Node && !scopeRoots(root, scope).some((element) => element.contains(event.target as Node))) root.focus();
    };
    const onKey = (event: KeyboardEvent): void => {
      if (!foremost() || event.defaultPrevented) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        if (current.current.dismissible) current.current.onClose();
      }
      if (event.key !== 'Tab') return;
      const stops = tabStops(scopeRoots(root, scope));
      const first = stops[0];
      const last = stops[stops.length - 1];
      const active = document.activeElement;
      if (!first || !stops.includes(active as HTMLElement) || (event.shiftKey ? active === first : active === last)) {
        event.preventDefault();
        (event.shiftKey ? last ?? root : first ?? root).focus();
      }
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('focusin', keepFocus);
    return () => {
      const wasForemost = foremost();
      modalStack.splice(modalStack.indexOf(root), 1);
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('focusin', keepFocus);
      if (wasForemost && previous?.isConnected) previous.focus();
    };
  }, [scope]);
  return <div className="dialog-backdrop" style={themeStyle} onClick={(event) => event.stopPropagation()}
    onMouseDown={(event) => {
      if (event.target === event.currentTarget && dismissOnBackdrop && dismissible && modalStack[modalStack.length - 1] === ref.current) onClose();
    }}>
    <section ref={ref} className={className} data-modal-dialog role="dialog" aria-modal="true" aria-labelledby={labelledBy} tabIndex={-1}>
      <ModalScopeContext.Provider value={scope}>{children}</ModalScopeContext.Provider>
    </section>
  </div>;
}

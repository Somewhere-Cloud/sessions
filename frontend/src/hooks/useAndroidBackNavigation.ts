import { useEffect, useRef } from 'react';
import { registerAndroidBackHandler } from '../lib/tauriBridge';
import { dismissForemostModal } from '../components/ui/ModalDialog';

// App navigation is state rather than browser history. Keep the current layer
// decision behind one stable native registration. Any open shared modal (More,
// automation editor, row menus, confirmations) closes first, as Escape would;
// only then does App state decide between leaving a view and the root exit.
export function useAndroidBackNavigation(handleBack: () => boolean): void {
  const handlerRef = useRef(handleBack);
  handlerRef.current = handleBack;

  useEffect(() => registerAndroidBackHandler(() => {
    if (dismissForemostModal()) return true;
    return handlerRef.current();
  }), []);
}

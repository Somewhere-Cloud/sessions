import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { FIND_IN_CONVERSATION_EVENT, isFindShortcut } from '../lib/conversationFind';

export interface ConversationFind {
  open: boolean;
  // Changes each time find is asked for, so an open bar takes focus again.
  request: number;
  close: () => void;
}

// useConversationFind opens a conversation's find bar on Mod+F or the command
// palette, only for the conversation on screen. A second Mod+F typed in the
// open bar is left to the browser, so its own find is one keystroke away.
export function useConversationFind(scope: RefObject<HTMLElement | null>): ConversationFind {
  const [open, setOpen] = useState(false);
  const [request, setRequest] = useState(0);
  const returnFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const onScreen = (): boolean => Boolean(scope.current && scope.current.getClientRects().length > 0);
    const show = (): void => {
      const active = document.activeElement;
      if (active instanceof HTMLElement && !active.closest('.conversation-find')) returnFocus.current = active;
      setOpen(true);
      setRequest((current) => current + 1);
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.defaultPrevented || !isFindShortcut(event) || !onScreen()) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest('.terminal-host') || target?.closest('.conversation-find')) return;
      event.preventDefault();
      show();
    };
    const onRequest = (): void => { if (onScreen()) show(); };
    window.addEventListener('keydown', onKeyDown);
    window.addEventListener(FIND_IN_CONVERSATION_EVENT, onRequest);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener(FIND_IN_CONVERSATION_EVENT, onRequest);
    };
  }, [scope]);
  const close = useCallback((): void => {
    setOpen(false);
    const target = returnFocus.current;
    returnFocus.current = null;
    if (target?.isConnected) target.focus();
  }, []);
  return { open, request, close };
}

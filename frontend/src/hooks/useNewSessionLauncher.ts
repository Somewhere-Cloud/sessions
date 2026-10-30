import { useCallback, useState } from 'react';

// Reopening a draft requests focus without resetting its context or starting work.
export function useNewSessionLauncher(isOpen: boolean, onOpen: (opening: boolean) => void) {
  const [focusRequest, setFocusRequest] = useState(0);
  const openNewSession = useCallback((): void => {
    setFocusRequest((request) => request + 1);
    onOpen(!isOpen && focusRequest === 0);
  }, [isOpen, focusRequest, onOpen]);
  return { focusRequest, openNewSession };
}

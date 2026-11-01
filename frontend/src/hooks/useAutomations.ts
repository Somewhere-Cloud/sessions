import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchAutomations, type AutomationsListing } from '../api/sessionsd/automations';
import { useServers } from '../lib/servers';

const REFRESH_MS = 15_000;

export interface AutomationsState {
  listing: AutomationsListing | null;
  // A runtime without automations, or one that cannot be read right now,
  // leaves the section hidden rather than alarming.
  unavailable: boolean;
  refresh: () => void;
}

/** The active computer's automations, kept current while mounted. */
export function useAutomations(): AutomationsState {
  const activeId = useServers((state) => state.activeId);
  const [listing, setListing] = useState<AutomationsListing | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [generation, setGeneration] = useState(0);
  const current = useRef(activeId);
  current.current = activeId;
  const refresh = useCallback(() => setGeneration((value) => value + 1), []);

  useEffect(() => {
    setListing(null);
    setUnavailable(false);
  }, [activeId]);

  useEffect(() => {
    const controller = new AbortController();
    const requestedFor = activeId;
    const load = (): void => {
      void fetchAutomations(controller.signal).then((next) => {
        if (controller.signal.aborted || current.current !== requestedFor) return;
        setListing(next);
        setUnavailable(false);
      }).catch(() => {
        if (!controller.signal.aborted && current.current === requestedFor) setUnavailable(true);
      });
    };
    load();
    const timer = window.setInterval(load, REFRESH_MS);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [activeId, generation]);

  return { listing, unavailable, refresh };
}

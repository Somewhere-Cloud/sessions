import { useEffect } from 'react';
import { startAppUpdateWatcher, useAppUpdate } from '../lib/appUpdate';

/**
 * Phones have no sidebar, so the app-update schedule starts here (loaded
 * after first paint), and More carries a dot while an update waits. The card
 * itself is at the top of the More sheet.
 */
export function MobileUpdateDot(): JSX.Element | null {
  useEffect(() => startAppUpdateWatcher(), []);
  const waiting = useAppUpdate((state) => state.info !== null && state.phase !== 'current');
  return waiting ? <span className="mn-detail-dot" role="status" aria-label="Sessions update available" /> : null;
}

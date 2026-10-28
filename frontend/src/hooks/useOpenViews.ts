import { useCallback, useEffect, useState } from 'react';
import type { SessionInfo } from '../types';
import { readOpenViews, migrateOpenViews, writeOpenViews } from '../lib/openViews';

// Selection is separate from retained views. Missing entries can be temporarily
// absent during refresh/create; keep explicit intent until the user closes it.
export function useOpenViews(scope: string, inventory: SessionInfo[], hydrated: boolean, localMain: boolean) {
  const [views, setViews] = useState(() => ({ scope, ids: readOpenViews(scope) }));
  const ids = views.scope === scope ? views.ids : readOpenViews(scope);
  const update = useCallback((change: (current: string[]) => string[]): void => {
    setViews((current) => {
      const before = current.scope === scope ? current.ids : readOpenViews(scope);
      const next = change(before);
      if (current.scope === scope && before.length === next.length && before.every((id, index) => id === next[index])) return current;
      writeOpenViews(scope, next);
      return { scope, ids: next };
    });
  }, [scope]);
  useEffect(() => {
    if (!hydrated) return;
    const restored = migrateOpenViews(scope, inventory, localMain);
    update((current) => [...new Set([...restored, ...current])].slice(-12));
  }, [scope, inventory, hydrated, localMain, update]);
  return [ids, update] as const;
}

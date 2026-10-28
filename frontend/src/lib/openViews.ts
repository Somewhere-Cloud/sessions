import type { SessionInfo } from '../types';
import { isAgentLedChild } from './workingSet';
import { isTauri } from './tauriBridge';
import { randomUUID } from './uuid';

const LEGACY_KEY = 'sessions:open-tabs:v1';

// Configured endpoint identity stays stable when a machine probe arrives. Never
// resolve a session UUID across endpoints. Query scopes separate native windows;
// unscoped browser windows get their own reload-stable sessionStorage identity.
export function openViewsKey(serverId: string, search = window.location.search): string {
  const params = new URLSearchParams(search);
  const scope = ['mode', 'session', 'server', 'tool'].map((key) => [key, params.get(key)]);
  let windowId = JSON.stringify(scope);
  if (scope.every(([, value]) => !value)) {
    windowId = 'native-main';
    if (!isTauri()) {
      try {
        windowId = window.sessionStorage.getItem('sessions:window-id') ?? randomUUID();
        window.sessionStorage.setItem('sessions:window-id', windowId);
      } catch { windowId = 'storage-unavailable'; }
    }
  }
  return `sessions:open-views:v2:${encodeURIComponent(serverId)}:${encodeURIComponent(windowId)}`;
}

function parseIds(value: string | null): string[] {
  try {
    const parsed: unknown = JSON.parse(value ?? '[]');
    return Array.isArray(parsed) ? [...new Set(parsed.filter((id): id is string => typeof id === 'string'))].slice(-12) : [];
  } catch { return []; }
}

export function readOpenViews(key: string): string[] {
  try { return parseIds(window.localStorage.getItem(key)); } catch { return []; }
}

// Legacy IDs carry no host/window evidence. Offer migration only for the original
// local main window, once inventory is authoritative; never import on a peer.
export function migrateOpenViews(key: string, inventory: SessionInfo[], localMain: boolean): string[] {
  const current = readOpenViews(key);
  try {
    if (window.localStorage.getItem(key) !== null || !localMain) return current;
    const known = new Set(inventory.map((session) => session.id));
    const migrated = parseIds(window.localStorage.getItem(LEGACY_KEY)).filter((id) => known.has(id));
    window.localStorage.setItem(key, JSON.stringify(migrated));
    window.localStorage.removeItem(LEGACY_KEY);
    return migrated;
  } catch { return current; }
}

export function writeOpenViews(key: string, ids: string[]): void {
  try { window.localStorage.setItem(key, JSON.stringify(ids)); } catch { /* non-fatal */ }
}

export function retainViewedSession(session: SessionInfo): boolean {
  // Unknown provenance and broken lineage remain reachable in the navigator;
  // known agent children require an explicit keep/pin/promotion even if orphaned.
  return !isAgentLedChild(session);
}

// Configured endpoint IDs, not daemon UUIDs: this is the same identity used
// by the client credential store and per-machine open views.
export type SingleWindow = { sessionId: string; machine: string | null; error?: string };

export function readSingleWindow(search = typeof window === 'undefined' ? '' : window.location.search): SingleWindow | null {
  const params = new URLSearchParams(search);
  if (!params.getAll('mode').includes('single')) return null;
  const sessionId = params.get('session') ?? '';
  const machine = params.get('machine');
  const entries = [...params.entries()];
  const valid = entries.length === (machine === null ? 2 : 3)
    && entries.every(([key]) => ['session', 'mode', 'machine'].includes(key))
    && new Set(entries.map(([key]) => key)).size === entries.length
    && validWindowIdentity(sessionId)
    && (machine === null || validWindowIdentity(machine));
  return { sessionId, machine, ...(!valid ? { error: 'This session window link is invalid. Reopen the session from its machine in the main Sessions window.' } : {}) };
}

export function validWindowIdentity(value: string): boolean {
  return value.length > 0 && new TextEncoder().encode(value).length <= 256
    && value.trim() === value && !/[\u0000-\u001f\u007f-\u009f]/.test(value);
}

export function singleWindowMachine<T extends { id: string; isDefault?: boolean; host: string; directoryOnly?: boolean }>(
  single: SingleWindow, servers: T[]
): T | undefined {
  if (single.error) return undefined;
  // Old two-field links never carried host evidence. Their supported scope
  // is the configured local default, never the last globally selected peer.
  return single.machine === null
    ? servers.find((server) => server.isDefault && ['localhost', '127.0.0.1', '::1', '[::1]'].includes(server.host))
    : servers.find((server) => server.id === single.machine && !server.directoryOnly);
}

import type { ServerConfig } from './servers';

// A lane link names one session and, optionally, the stable identity of the
// machine that owns it: sessions://lane/<session-id>?machine=<machine-id>
// (runtime/internal/lanelink). The native shell parses the URL; this checks
// what it handed over again, so the viewer never acts on anything but two
// UUID-shaped identifiers.
export interface LaneLinkTarget {
  sessionId: string;
  machineId?: string;
}

const UUID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const UNKNOWN_LANE_MACHINE_NOTICE =
  'That lane is on a machine this app is not connected to yet. Pair it in Connections, then open the link again.';

export function laneLinkTarget(value: unknown): LaneLinkTarget | null {
  if (!value || typeof value !== 'object') return null;
  const { sessionId, machineId } = value as { sessionId?: unknown; machineId?: unknown };
  if (typeof sessionId !== 'string' || !UUID_SHAPE.test(sessionId)) return null;
  if (machineId === undefined || machineId === null) return { sessionId: sessionId.toLowerCase() };
  if (typeof machineId !== 'string' || !UUID_SHAPE.test(machineId)) return null;
  return { sessionId: sessionId.toLowerCase(), machineId: machineId.toLowerCase() };
}

// The machines whose identity this app already knows answer immediately.
// Machines it has not identified yet -- usually this Mac's own daemon -- are
// asked, one at a time, through the same identity read the fleet uses. No
// match means the link belongs to a machine that is not configured here.
export async function resolveLaneLinkServerId(
  target: LaneLinkTarget,
  servers: ServerConfig[],
  activeId: string | null,
  identify: (server: ServerConfig) => Promise<string | null>
): Promise<string | null> {
  if (!target.machineId) {
    return activeId ?? servers.find((server) => server.isDefault)?.id ?? servers[0]?.id ?? null;
  }
  const wanted = target.machineId;
  const known = servers.find((server) => server.machineId?.toLowerCase() === wanted);
  if (known) return known.id;
  const unidentified = servers.filter((server) => !server.machineId && !server.directoryOnly);
  for (const server of unidentified) {
    const machineId = await identify(server).catch(() => null);
    if (machineId?.toLowerCase() === wanted) return server.id;
  }
  return null;
}

const LANE_LINK_PATTERN = /sessions:\/\/lane\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\?machine=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}))?/gi;

/** Every distinct lane link in a block of text, such as a command's output
 *  or a delegated lane's report, in the order they appear. */
export function findLaneLinks(text: string | undefined): LaneLinkTarget[] {
  if (!text) return [];
  const seen = new Set<string>();
  const targets: LaneLinkTarget[] = [];
  for (const match of text.matchAll(LANE_LINK_PATTERN)) {
    const target = laneLinkTarget({ sessionId: match[1], machineId: match[2] });
    if (!target || seen.has(target.sessionId)) continue;
    seen.add(target.sessionId);
    targets.push(target);
  }
  return targets;
}

// A lane link clicked inside the app takes the same path as one macOS hands
// over: LaneLinkOpener resolves the machine and opens the session.
export const OPEN_LANE_EVENT = 'sessions:open-lane';

export function requestOpenLane(target: LaneLinkTarget): void {
  window.dispatchEvent(new CustomEvent<LaneLinkTarget>(OPEN_LANE_EVENT, { detail: target }));
}

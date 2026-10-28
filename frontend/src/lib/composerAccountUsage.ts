import type { AccountProfile, AccountUsage } from '../api/sessionsd';
import type { SessionTool } from '../types';
import { rollUpAccounts, type AccountGroup } from './accountRollup';
import { windowName } from './accountUsageDisplay';

export interface ComposerUsageScope {
  sessionId: string;
  machineId?: string;
  provider: SessionTool;
  profile?: string;
  configDir?: string;
}

/** Resolve only the executing session's recorded home, never a preference. */
export function sessionAccount(scope: ComposerUsageScope, profiles: AccountProfile[]): AccountProfile | undefined {
  const tool = scope.provider === 'codex' ? 'codex' : scope.provider === 'claude-code' ? 'claude' : undefined;
  if (!scope.machineId || !tool) return undefined;
  const matches = profiles.filter((profile) => profile.tool === tool && profile.name === (scope.profile ?? '')
    && (scope.profile ? !profile.default_profile : profile.default_profile)
    && (!scope.configDir || profile.path === scope.configDir));
  return matches.length === 1 ? matches[0] : undefined;
}

/** One placement only: email and readings on other hosts never select quota. */
export function composerAccountGroup(
  machineId: string, machineName: string, profile: AccountProfile, usage: AccountUsage[] | null, now: number
): AccountGroup {
  const readings = usage?.map((item) => item.read_at && now - item.read_at > 60_000
    ? { ...item, stale: true, message: item.stale ? item.message : 'This snapshot is older than one minute. Refresh for a current reading.' } : item) ?? null;
  return rollUpAccounts([{ serverId: machineId, machineName, profiles: [profile], usage: readings }])[0];
}

export function compactLimits(group?: AccountGroup): string {
  const reading = group?.reading?.usage;
  if (!reading) return group?.tool === 'claude' ? 'Limits unavailable' : 'Limits unknown';
  const buckets = reading.buckets ?? [];
  const prefix = reading.stale ? 'Last known · ' : '';
  if (buckets.length > 1) return `${prefix}${buckets.length} independent limits`;
  const windows = buckets[0]?.windows ?? [];
  if (!windows.length) return 'No limits reported';
  const first = windows[0];
  return `${prefix}${windowName(first)} ${Math.max(0, Math.min(100, first.used_percent))}% used${windows.length > 1 ? ` · +${windows.length - 1} window` : ''}`;
}

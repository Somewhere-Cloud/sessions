import { useCallback, useSyncExternalStore } from 'react';
import { fetchAccountUsage, fetchProfiles, type AccountProfile, type AccountUsage } from '../api/sessionsd';
import { sessionAccount, type ComposerUsageScope } from '../lib/composerAccountUsage';

interface Reading {
  profile?: AccountProfile;
  usage?: AccountUsage[] | null;
  error?: string;
  loading: boolean;
}

// Entries exist only while mounted consumers own them. Sharing the complete
// executing scope avoids duplicate composer/footer reads without retaining logins.
interface Entry {
  reading: Reading;
  listeners: Set<() => void>;
  request?: AbortController;
}
const entries = new Map<string, Entry>();
const pending: Reading = { loading: true };

async function read(scope: ComposerUsageScope, entry: Entry, refresh = false) {
  entry.request?.abort();
  const controller = new AbortController();
  entry.request = controller;
  const { signal } = controller;
  const publish = (reading: Reading) => {
    if (signal.aborted) return;
    entry.reading = reading;
    entry.listeners.forEach((listener) => listener());
  };
  publish(pending);
  const { machineId, provider } = scope;
  if (!machineId || provider === 'terminal') {
    publish({ loading: false, error: 'The executing account is not known.' });
    return;
  }
  try {
    const profiles = await fetchProfiles(signal, machineId, true);
    if (signal.aborted) return;
    const profile = sessionAccount(scope, profiles);
    if (!profile) {
      publish({ loading: false, error: 'This session’s provider home could not be matched on its computer. Check Accounts there.' });
      return;
    }
    const usage = await fetchAccountUsage(machineId, { tool: profile.tool, name: profile.name, exact: true, refresh, signal });
    publish({ profile, usage, loading: false });
  } catch (error) {
    publish({ loading: false, error: error instanceof Error ? error.message : 'Could not read account limits. Refresh to try again.' });
  }
}

export function useComposerAccountUsage(scope: ComposerUsageScope) {
  const key = JSON.stringify([scope.sessionId, scope.machineId, scope.provider, scope.profile, scope.configDir]);
  const subscribe = useCallback((listener: () => void) => {
    let entry = entries.get(key);
    if (!entry) {
      entry = { reading: pending, listeners: new Set() };
      entries.set(key, entry);
    }
    entry.listeners.add(listener);
    if (!entry.request) {
      const [sessionId, machineId, provider, profile, configDir] = JSON.parse(key);
      void read({ sessionId, machineId, provider, profile, configDir }, entry);
    }
    return () => {
      entry.listeners.delete(listener);
      if (!entry.listeners.size) {
        entry.request?.abort();
        entries.delete(key);
      }
    };
  }, [key]);
  const snapshot = useCallback(() => entries.get(key)?.reading ?? pending, [key]);
  const reading = useSyncExternalStore(subscribe, snapshot);
  return { ...reading, refresh: () => {
    const entry = entries.get(key);
    if (entry && !entry.reading.loading) void read(scope, entry, true);
  } };
}

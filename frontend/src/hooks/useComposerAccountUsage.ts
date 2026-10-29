import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchAccountUsage, fetchProfiles, type AccountProfile, type AccountUsage } from '../api/sessionsd';
import { sessionAccount, type ComposerUsageScope } from '../lib/composerAccountUsage';

interface Reading {
  profile?: AccountProfile;
  usage?: AccountUsage[] | null;
  error?: string;
  loading: boolean;
}

/** Mount this hook in a component keyed by the complete executing scope. */
export function useComposerAccountUsage(scope: ComposerUsageScope) {
  const { sessionId, machineId, provider, profile: profileName, configDir } = scope;
  const [reading, setReading] = useState<Reading>({ loading: true });
  const request = useRef<AbortController>();
  const read = useCallback(async (refresh = false) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const { signal } = controller;
    // Clear old identity even on refresh: a login may have changed meanwhile.
    setReading({ loading: true });
    if (!machineId || provider === 'terminal') {
      setReading({ loading: false, error: 'The executing account is not known.' });
      return;
    }
    try {
      const profiles = await fetchProfiles(signal, machineId, true);
      if (signal.aborted) return;
      const profile = sessionAccount({ sessionId, machineId, provider, profile: profileName, configDir }, profiles);
      if (!profile) {
        setReading({ loading: false, error: 'This session’s provider home could not be matched on its computer. Check Accounts there.' });
        return;
      }
      const usage = await fetchAccountUsage(machineId, { tool: profile.tool, name: profile.name, exact: true, refresh, signal });
      if (!signal.aborted) setReading({ profile, usage, loading: false });
    } catch (error) {
      if (!signal.aborted) setReading({ loading: false, error: error instanceof Error ? error.message : 'Could not read account limits. Refresh to try again.' });
    }
  }, [sessionId, machineId, provider, profileName, configDir]);
  useEffect(() => {
    void read();
    return () => request.current?.abort();
  }, [read]);
  return { ...reading, refresh: () => void read(true) };
}

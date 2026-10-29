import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useState } from 'react';
import { getNativeRuntimeStatus, isTauri, type NativeRuntimeStatus } from '../lib/tauriBridge';

// Native installation state is independent of daemon connectivity. A healthy
// rollback must not conceal the failed update that caused it.
export function NativeRuntimeStatusCard(): JSX.Element | null {
  const [status, setStatus] = useState<NativeRuntimeStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const native = isTauri();
  useEffect(() => {
    if (!native) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = async (): Promise<void> => {
      try {
        const next = await getNativeRuntimeStatus();
        if (!disposed) { setStatus(next); setError(null); }
      } catch (cause) {
        if (!disposed) setError(cause instanceof Error ? cause.message : String(cause || 'Could not read native service status.'));
      } finally {
        if (!disposed) timer = setTimeout(() => { void read(); }, 5000);
      }
    };
    void read();
    return () => { disposed = true; clearTimeout(timer); };
  }, [native]);
  if (!native) return null;
  return (
    <Surface className="settings-card settings-status-card" aria-label="Local background service">
      <PageHeader level={2} title="Background service on this computer" />
      <p>This is the app’s local installation status, even when you are viewing another computer.</p>
      <div role="status" aria-live="polite">
        {error ? <p>Installation status unavailable: {error}</p> : <>
          <p>{status?.detail || 'Reading installation status…'}</p>
          {status?.runtimeVersion && <p>Runtime: <code>{status.runtimeVersion}</code></p>}
        </>}
      </div>
      <p>Checking this status does not restart the service or any lane.</p>
    </Surface>
  );
}

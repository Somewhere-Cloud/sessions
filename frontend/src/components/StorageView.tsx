import { useEffect, useRef, useState } from 'react';
import { fetchStoragePreview, type StorageBytes, type StorageReport, type StorageRoot } from '../api/sessionsd/storage';
import { serverDisplayName, useServers } from '../lib/servers';
import './StorageView.css';

function bytes(value: number | null): string {
  if (value === null) return 'Unknown';
  if (value < 1024) return `${value} B`;
  const unit = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4);
  return `${(value / 1024 ** unit).toFixed(1)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`;
}
function StorageSize({ value }: { value: StorageBytes }): JSX.Element {
  return <p className="storage-size">{bytes(value.logical)} logical · {bytes(value.allocated)} allocated · {value.confidence === 'observed' ? 'Observed' : 'Partial reading'}
    {value.shared_logical > 0 && <> · {bytes(value.shared_logical)} in shared hardlinks</>}
    {value.symlinks > 0 && <> · {value.symlinks} symlinks skipped</>}
  </p>;
}
export function StorageView(): JSX.Element {
  const activeId = useServers((state) => state.activeId);
  const server = useServers((state) => state.servers.find((item) => item.id === state.activeId));
  const [report, setReport] = useState<StorageReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const request = useRef<AbortController | null>(null);
  useEffect(() => {
    setReport(null);
    setMessage('');
    setBusy(false);
    return () => request.current?.abort();
  }, [activeId]);
  const scan = async (): Promise<void> => {
    if (!activeId) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setMessage('');
    setReport(null);
    try {
      const next = await fetchStoragePreview(activeId, controller.signal);
      if (!controller.signal.aborted) setReport(next);
    } catch (error) {
      if (!controller.signal.aborted) setMessage(error instanceof Error ? error.message : 'Storage scan could not finish. Retry on the selected computer.');
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  };
  return <section className="settings-page storage-view">
    <span className="settings-kicker">Selected computer</span>
    <h1>Storage</h1>
    <p>Inspect known repositories on {server ? serverDisplayName(server, true) : 'this computer'}. Source, external worktrees and unknown data are kept. This preview changes no files.</p>
    <div className="storage-controls">
      <button type="button" className="btn-primary" disabled={busy} onClick={() => void scan()}>{busy ? 'Scanning…' : 'Scan storage'}</button>
      {busy && <button type="button" onClick={() => { request.current?.abort(); setBusy(false); setMessage('Scan canceled. The host may need time to finish a filesystem read.'); }}>Cancel scan</button>}
    </div>
    {message && <p role="status">{message}</p>}
    {report && <>
      <div className="settings-card" aria-label="Storage summary">
        <h2>{report.machine_name}</h2>
        <p className="storage-path">Machine {report.machine_id} · {new Date(report.scanned_at).toLocaleString()}</p>
        <StorageSize value={report.totals} />
        <p>{report.complete ? 'Repository scan completed.' : 'Some storage could not be measured. Totals are a lower bound.'} Allocated space is not a promise of freed space.</p>
      </div>
      {report.roots.length === 0 && <p>{report.complete ? 'No known Git repositories found on this computer.' : 'No repository measurements available. See scope and limits.'}</p>}
      {report.roots.map((root) => <StorageRootCard key={root.path} root={root} />)}
      <details className="settings-card"><summary>Scope and limits</summary>
        {[...report.issues, ...report.limitations].map((item, index) => <p key={index}>{item}</p>)}
      </details>
    </>}
  </section>;
}
function StorageRootCard({ root }: { root: StorageRoot }): JSX.Element {
  return <article className="settings-card storage-root">
    <h2 className="storage-path">{root.path}</h2>
    <p>{root.ownership === 'known' ? 'Ownership recorded by Sessions' : 'Ownership unknown · external worktree kept'}</p>
    <StorageSize value={root.bytes} />
    <p>Git changes: {root.git.dirty} · Untracked: {root.git.untracked} · Unpushed: {root.git.unpushed}</p>
    {root.references.length > 0 && <details><summary>{root.references.length} live or unresolved runner references · Kept</summary>
      <ul>{root.references.map((reference) => <li key={reference.session} className="storage-path">{reference.session} · PID {reference.pid || 'unknown'} · {reference.path}</li>)}</ul>
    </details>}
    <p>Source and unknown data are kept. Worktree teardown is outside this preview.</p>
    <dl className="storage-categories">{Object.entries(root.categories).map(([category, value]) => <div key={category}><dt>{category}</dt><dd>{bytes(value.logical)} logical · {bytes(value.allocated)} allocated</dd></div>)}</dl>
    {root.candidates.map((candidate) => <div className="storage-candidate" key={candidate.path}>
      <strong>{candidate.action === 'potentially-reclaimable' ? 'Potentially reclaimable · review required' : 'Kept'} · {candidate.category}</strong>
      <p className="storage-path">{candidate.path}</p>
      <StorageSize value={candidate.bytes} />
      {candidate.reasons.map((reason, index) => <p key={index}>{reason}</p>)}
      {candidate.restore && <p>Restore suggestion: {candidate.restore}</p>}
    </div>)}
    {root.issues.map((issue, index) => <p key={index}>{issue}</p>)}
  </article>;
}

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
  const [sort, setSort] = useState<'size' | 'name'>('size');
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
    <p>See storage on {server ? serverDisplayName(server, true) : 'this computer'}. Read-only · Files are kept.</p>
    <div className="storage-controls">
      <button type="button" className="btn-primary" disabled={busy} onClick={() => void scan()}>{busy ? 'Scanning…' : 'Scan storage'}</button>
      {busy && <button type="button" onClick={() => { request.current?.abort(); setBusy(false); setMessage('Scan canceled. The host may need time to finish a filesystem read.'); }}>Cancel scan</button>}
    </div>
    {message && <p role="status">{message}</p>}
    {report && <>
      <StorageSummary report={report} />
      {report.roots.length === 0 ? <p>{report.complete ? 'No known Git repositories found on this computer.' : 'No repository measurements available. See scan details.'}</p> : <>
        <div className="storage-list-heading"><h2>Repositories</h2><label>Sort <select value={sort} onChange={(event) => setSort(event.target.value as 'size' | 'name')}>
          <option value="size">Largest measured first</option><option value="name">Name</option>
        </select></label></div>
        <div className="storage-repositories">{sortedRoots(report.roots, sort).map((root) => <StorageRootCard key={root.path} root={root} />)}</div>
      </>}
    </>}
  </section>;
}
function shortName(path: string): string {
  const parts = path.replace(/[\\/]+$/, '').split(/[\\/]/);
  return parts[parts.length - 1] || path;
}
function measuredSize(value: StorageBytes): string {
  if (value.confidence === 'partial' && value.files === 0) return 'Not measured';
  return `${value.confidence === 'partial' ? 'At least ' : ''}${bytes(value.logical)}`;
}
function sortedRoots(roots: StorageRoot[], sort: 'size' | 'name'): StorageRoot[] {
  return [...roots].sort((a, b) => {
    if (sort === 'size') {
      const knownA = a.bytes.confidence === 'observed' || a.bytes.files > 0;
      const knownB = b.bytes.confidence === 'observed' || b.bytes.files > 0;
      if (knownA !== knownB) return knownA ? -1 : 1;
      if (a.bytes.logical !== b.bytes.logical) return b.bytes.logical - a.bytes.logical;
    }
    return shortName(a.path).localeCompare(shortName(b.path)) || a.path.localeCompare(b.path);
  });
}
function repositoryStatus(root: StorageRoot): string {
  if (root.bytes.confidence === 'partial') return root.bytes.files === 0 ? 'Could not be measured' : 'Partly measured';
  if (root.references.length > 0) return 'May still be in use';
  if (root.ownership === 'unknown') return 'Owner not recorded';
  if (root.git.dirty === 'yes' || root.git.untracked === 'yes') return 'Contains local changes';
  if (root.git.unpushed === 'yes') return 'Contains unpushed work';
  if (Object.values(root.git).some((value) => value === 'unknown')) return 'Needs a closer look';
  if (root.candidates.some((candidate) => candidate.action === 'potentially-reclaimable')) return 'Review available';
  return 'Kept';
}
function StorageSummary({ report }: { report: StorageReport }): JSX.Element {
  return <div className="settings-card storage-overview" aria-label="Storage summary">
    <span className="storage-summary-label">Measured files on {report.machine_name}</span>
    <strong className="storage-summary-size">{measuredSize(report.totals)}</strong>
    <p>{report.roots.length} {report.roots.length === 1 ? 'repository' : 'repositories'} · {report.complete ? 'Scan complete' : 'Some measurements incomplete'}</p>
    <details className="storage-scan-details"><summary>Scan details</summary>
      <p className="storage-path">Machine {report.machine_id} · {new Date(report.scanned_at).toLocaleString()}</p>
      <StorageSize value={report.totals} />
      <p>Sizes describe measured files, not space that can necessarily be freed. Shared repository sizes may overlap; the total comes from the host, not the sum of these rows.</p>
      {[...report.issues, ...report.limitations].map((item, index) => <p key={index}>{item}</p>)}
    </details>
  </div>;
}
function StorageRootCard({ root }: { root: StorageRoot }): JSX.Element {
  return <details className="storage-repository">
    <summary className="storage-row">
      <span className="storage-repository-name">{shortName(root.path)}</span>
      <span className="storage-row-size">{measuredSize(root.bytes)}</span>
      <span className="storage-row-status">{repositoryStatus(root)}</span>
      <span className="storage-row-toggle" aria-hidden="true">⌄</span>
    </summary>
    <div className="storage-repository-details">
      <p className="storage-path">{root.path}</p>
      <p>{root.ownership === 'known' ? 'Ownership recorded by Sessions' : 'Ownership unknown · external worktree kept'}</p>
      <StorageSize value={root.bytes} />
      <p>Git changes: {root.git.dirty} · Untracked: {root.git.untracked} · Unpushed: {root.git.unpushed}</p>
      {root.references.length > 0 && <details><summary>{root.references.length} live or unresolved runner references · Kept</summary>
        <ul>{root.references.map((reference) => <li key={reference.session} className="storage-path">{reference.session} · PID {reference.pid || 'unknown'} · {reference.path}</li>)}</ul>
      </details>}
      <p>Source and unknown data are kept. This preview offers no worktree removal.</p>
      <dl className="storage-categories">{Object.entries(root.categories).map(([category, value]) => <div key={category}><dt>{category}</dt><dd>{bytes(value.logical)} logical · {bytes(value.allocated)} allocated</dd></div>)}</dl>
      {root.candidates.map((candidate) => <div className="storage-candidate" key={candidate.path}>
        <strong>{candidate.action === 'potentially-reclaimable' ? 'Potentially reclaimable · review required' : 'Kept'} · {candidate.category}</strong>
        <p className="storage-path">{candidate.path}</p>
        <StorageSize value={candidate.bytes} />
        {candidate.reasons.map((reason, index) => <p key={index}>{reason}</p>)}
        {candidate.restore && <p>Restore suggestion: {candidate.restore}</p>}
      </div>)}
      {root.issues.map((issue, index) => <p key={index}>{issue}</p>)}
    </div>
  </details>;
}

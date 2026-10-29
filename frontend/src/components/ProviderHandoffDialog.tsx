import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { SessionInfo } from '../types';
import { handoffRequest, type HandoffRecord, type HandoffRequest } from '../api/sessionsd/handoffs';
import { randomUUID } from '../lib/uuid';
import { getActiveServer } from '../lib/servers';
import { ModalDialog } from './ui/ModalDialog';

export function ProviderHandoffDialog({ session, serverId, onOpen, onClose }: {
  session: SessionInfo; serverId: string; onOpen: (id: string) => void; onClose: () => void;
}): JSX.Element {
  const [choice, setChoice] = useState<HandoffRequest>({ operationId: randomUUID(), sourceSessionId: session.id,
    destinationProvider: session.tool === 'codex' ? 'claude' : 'codex', model: '', effort: '' });
  const [preview, setPreview] = useState<HandoffRecord | null>(null);
  const [receipt, setReceipt] = useState<HandoffRecord | null>(null);
  const [saved, setSaved] = useState<HandoffRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [text, setText] = useState('');
  const lock = useRef(false);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let live = true;
    void handoffRequest<HandoffRecord[]>(`?sessionId=${encodeURIComponent(session.id)}`, serverId)
      .then((records) => { if (live) { setSaved(records); setLoaded(true); } })
      .catch((reason) => { if (live) setError(String(reason)); });
    return () => { live = false; };
  }, [serverId, session.id]);
  const run = async (action: () => Promise<void>): Promise<void> => {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError('');
    try { await action(); } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); }
    finally { lock.current = false; setBusy(false); }
  };
  const refresh = async (): Promise<void> => {
    if (receipt) setReceipt(await handoffRequest<HandoffRecord>(`/${receipt.request.operationId}`, serverId));
  };
  const start = async (): Promise<void> => {
    // Keep the committed operation locally even if its HTTP answer is lost.
    setReceipt({ ...preview!, request: choice, creation: 'unknown', context: 'pending',
      recovery: 'Read status for this operation before creating another switch.' });
    setReceipt(await handoffRequest<HandoffRecord>('', serverId, choice));
  };
  const openSegment = async (id: string): Promise<void> => {
    if (getActiveServer().id !== serverId) throw new Error('Select the originating computer before opening this segment.');
    const { useSessions } = await import('../store/sessions');
    await useSessions.getState().refresh(serverId);
    const current = useSessions.getState();
    if (getActiveServer().id !== serverId || current.serverId !== serverId || !current.sessions.some((row) => row.id === id)) {
      throw new Error('This segment has not been listed on its original computer yet. Reconnect and read status.');
    }
    onOpen(id); onClose();
  };
  const ready = receipt?.creation === 'created' && ['applied', 'staged-for-first-request', 'sent-with-request'].includes(receipt.context ?? '');
  const canSend = ready && ['not-sent', 'not-delivered'].includes(receipt?.prompt?.status ?? '') && (receipt?.prompt?.retry ?? false);
  return createPortal(<ModalDialog className="dialog" labelledBy="handoff-title" onClose={onClose} dismissOnBackdrop={false}>
    <h2 id="handoff-title">Switch provider</h2>
    <p>The source stays available. This starts a fresh provider segment with a bounded snapshot. Unsent drafts and queued work stay with their source.</p>
    {!loaded ? <p role="status">Reading saved handoffs…</p> : null}
    {saved.length ? <details><summary>Saved switches ({saved.length})</summary>{saved.map((item) => <button className="btn" key={item.request.operationId}
      onClick={() => { setReceipt(item); setPreview(null); }}>Inspect {item.request.destinationProvider} · {item.creation}</button>)}</details> : null}
    {!receipt ? <>
      <p>Target provider: <strong>{choice.destinationProvider === 'codex' ? 'Codex' : 'Claude'}</strong></p>
      <label>Exact model ID (blank uses default)<input value={choice.model} disabled={busy} onChange={(event) => { setChoice({ ...choice, model: event.target.value }); setPreview(null); }} /></label>
      <label>Effort<select value={choice.effort} disabled={busy} onChange={(event) => { setChoice({ ...choice, effort: event.target.value }); setPreview(null); }}>
        <option value="">Automatic</option>{['low', 'medium', 'high', 'xhigh', 'max'].map((value) => <option key={value}>{value}</option>)}
      </select></label>
      {preview ? <><p>{preview.coverage.included} messages · {preview.coverage.omitted} omitted · {preview.coverage.bytes} text bytes</p><p>{preview.coverage.limitation}</p></> : null}
      <button className="btn" disabled={busy || !loaded} onClick={() => void run(async () => { setPreview(await handoffRequest<HandoffRecord>('/preview', serverId, choice)); })}>Review switch</button>
      {preview ? <button className="btn btn-primary" disabled={busy} onClick={() => void run(start)}>Prepare {choice.destinationProvider} segment</button> : null}
    </> : <>
      <HandoffProgress receipt={receipt} />
      <button className="btn" disabled={busy} onClick={() => void run(refresh)}>Read status</button>
      {canSend ? <><label>First request<textarea value={text} onChange={(event) => setText(event.target.value)} disabled={busy} /></label>
        <button className="btn btn-primary" disabled={busy || !text.trim()} onClick={() => void run(async () => {
          try { await handoffRequest(`/${receipt.request.operationId}/prompt`, serverId, { text }); }
          finally { await refresh(); }
        })}>Send first request</button></> : null}
      {receipt.request.sourceSessionId !== session.id ? <button className="btn" disabled={busy} onClick={() => void run(() => openSegment(receipt.request.sourceSessionId))}>Open source segment</button> : null}
      {ready && receipt.targetSessionId ? <button className="btn" disabled={busy} onClick={() => void run(() => openSegment(receipt.targetSessionId!))}>Open target segment</button> : null}
    </>}
    {busy ? <p role="status">Working…</p> : null}
    {error ? <p role="alert">{error}</p> : null}
    <div className="dialog-actions"><button className="btn" onClick={onClose}>Close</button></div>
  </ModalDialog>, document.body);
}

function HandoffProgress({ receipt }: { receipt: HandoffRecord }): JSX.Element {
  return <section aria-label="Provider switch progress" aria-live="polite">
    <p>Target: {receipt.request.destinationProvider} · {receipt.artifact.destinationModel}</p>
    <p>Creation: {receipt.creation} · Context: {receipt.context ?? 'pending'}</p>
    <p>First request for this switch: {receipt.prompt?.status ?? 'not-sent'} · Response: {receipt.response ?? 'not-observed'}</p>
    <p>{receipt.coverage.limitation}</p>
    <p>Turn activity: {receipt.activity ?? 'not-observed'}. Messages sent directly in the target have their own delivery receipts.</p>
    <p>{receipt.coverage.included} messages transferred · {receipt.coverage.omitted} omitted</p>
    <p>Source history: {receipt.artifact.sourceHistoryId}</p>
    <p>Operation: <code>{receipt.request.operationId}</code></p>
    {receipt.nativeTargetId ? <p>Native target: <code>{receipt.nativeTargetId}</code></p> : null}
    {receipt.recovery ? <p>{receipt.recovery}</p> : null}
    {receipt.error ? <p role="alert">{receipt.error}</p> : null}
  </section>;
}

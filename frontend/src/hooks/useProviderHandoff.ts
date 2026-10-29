import { useCallback, useEffect, useRef, useState } from 'react';
import type { SessionInfo } from '../types';
import { handoffRequest, type HandoffRecord, type HandoffRequest } from '../api/sessionsd/handoffs';
import { useHandoffStatus } from './useHandoffStatus';
import { daemonErrorDisplay } from '../api/sessionsd/core';
import { randomUUID } from '../lib/uuid';
import { getActiveServer } from '../lib/servers';

export function useProviderHandoff(session: SessionInfo, serverId: string, onOpen: (id: string) => void, onClose: () => void) {
  const [choice, setChoice] = useState<HandoffRequest>({ operationId: randomUUID(), sourceSessionId: session.id,
    destinationProvider: session.tool === 'codex' ? 'claude' : 'codex', model: '', effort: '' });
  const [captureUnconfirmed, setCaptureUnconfirmed] = useState(false);
  const [preview, setPreview] = useState<HandoffRecord | null>(null);
  const [receipt, setReceipt] = useState<HandoffRecord | null>(null);
  const [saved, setSaved] = useState<HandoffRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [errorDetails, setErrorDetails] = useState('');
  const showError = (reason: unknown): void => {
    const detail = reason instanceof Error ? reason.message : String(reason);
    const message = daemonErrorDisplay(reason) ?? detail;
    setError(message); setErrorDetails(message === detail ? '' : detail);
  };
  const [text, setText] = useState('');
  const lock = useRef(false);
  const revision = useRef(0);
  const acceptStatus = useCallback((record: HandoffRecord): void => {
    setReceipt(record); setCaptureUnconfirmed(false);
    setSaved((records) => records.some((item) => item.request.operationId === record.request.operationId)
      ? records.map((item) => item.request.operationId === record.request.operationId ? record : item) : [record, ...records]);
  }, []);
  const statusNotice = useHandoffStatus(receipt?.request.operationId, serverId, lock, revision, acceptStatus);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let live = true;
    void handoffRequest<HandoffRecord[]>(`?sessionId=${encodeURIComponent(session.id)}`, serverId)
      .then((records) => { if (live) { setSaved(records); setLoaded(true); } })
      .catch((reason) => { if (live) { setError(daemonErrorDisplay(reason) ?? String(reason)); setErrorDetails(String(reason)); } });
    return () => { live = false; };
  }, [serverId, session.id]);
  const run = async (action: () => Promise<void>): Promise<void> => {
    if (lock.current) return;
    revision.current += 1;
    lock.current = true; setBusy(true); setError(''); setErrorDetails('');
    try { await action(); } catch (reason) { showError(reason); }
    finally { lock.current = false; setBusy(false); }
  };
  const refresh = async (): Promise<void> => {
    if (receipt) {
      acceptStatus(await handoffRequest<HandoffRecord>(`/${receipt.request.operationId}`, serverId));
    }
  };
  const start = async (): Promise<void> => {
    // Keep the committed operation locally even if its HTTP answer is lost.
    setCaptureUnconfirmed(true);
    setReceipt({ ...preview!, request: choice, creation: 'unknown', context: 'pending',
      recovery: 'Read status for this operation before creating another switch.' });
    acceptStatus(await handoffRequest<HandoffRecord>('', serverId, choice));
  };
  const sendFirstRequest = async (): Promise<void> => {
    if (!receipt) return;
    // Until a new receipt proves delivery, never offer another send from stale state.
    acceptStatus({ ...receipt, prompt: { status: 'unknown', retry: false } });
    let refusal: unknown;
    try { await handoffRequest(`/${receipt.request.operationId}/prompt`, serverId, { text }); }
    catch (reason) { refusal = reason; }
    try { await refresh(); }
    catch (reason) {
      if (refusal) throw new Error(`${daemonErrorDisplay(refusal) ?? String(refusal)}\nStatus check also failed: ${daemonErrorDisplay(reason) ?? String(reason)}`);
      throw reason;
    }
    if (refusal) throw refusal;
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
  return { statusNotice, captureUnconfirmed, setCaptureUnconfirmed, choice, setChoice, preview, setPreview, receipt, setReceipt, saved, busy, error, errorDetails, text, setText, loaded, run, refresh, start, sendFirstRequest, openSegment, ready, canSend };
}

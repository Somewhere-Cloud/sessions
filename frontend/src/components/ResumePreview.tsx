import { useEffect, useState } from 'react';
import { fetchServerHistoryTranscript, type HistoryMessage, type ResumableSession } from '../api/sessionsd';
import { harnessDisplay } from '../lib/harnessContent';
import { abortBudget } from '../lib/abortBudget';
import { isLocalServer, useServers } from '../lib/servers';
import '../styles/resume-preview.css';

/** Selecting a saved conversation only reads a bounded tail; it starts no agent. */
export function ResumePreview({ selected }: { selected: ResumableSession | null }): JSX.Element | null {
  const server = useServers((state) => state.servers.find(isLocalServer));
  const [messages, setMessages] = useState<HistoryMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const budget = abortBudget(10_000, controller.signal);
    setMessages([]);
    setError(null);
    setLoading(Boolean(selected && server));
    if (selected && server) {
      void fetchServerHistoryTranscript(server, selected.historyId || selected.sessionId, budget.signal, { preview: true, previewLimit: 12 })
        .then((transcript) => {
          if (!controller.signal.aborted) setMessages(transcript.messages.filter((message) => message.role !== 'tool').slice(-4));
        })
        .catch((reason: unknown) => {
          if (!controller.signal.aborted) setError(budget.signal.aborted ? 'Reading this preview took too long. Retry when this computer is responding; nothing was restarted.' : reason instanceof Error ? reason.message : 'Preview unavailable.');
        })
        .finally(() => { budget.release(); if (!controller.signal.aborted) setLoading(false); });
    } else budget.release();
    return () => { controller.abort(); budget.release(); };
  }, [selected?.historyId, selected?.sessionId, server, selected, retry]);
  if (!selected) return null;
  return <section className="resume-preview" aria-label="Read-only conversation preview">
    <h3>Recent conversation <small>Read only · no agent started</small></h3>
    {loading ? <p role="status">Reading recent messages…</p> : null}
    {!server ? <p>No local connection is configured. The conversation has not been restarted.</p> : null}
    {error ? <><p role="status">Preview unavailable: {error}</p><button type="button" className="btn btn-ghost" onClick={() => setRetry((current) => current + 1)}>Retry preview</button></> : null}
    {!loading && server && !error && messages.length === 0 ? <p>No readable recent messages. The saved conversation is unchanged.</p> : null}
    {messages.map((message, index) => {
      const display = harnessDisplay(message.text, message.role);
      return <div className="resume-preview-message" key={message.id || index}>
        <strong>{display.speaker === 'system' ? 'System' : message.role === 'user' ? 'You' : selected.tool === 'claude' ? 'Claude' : 'Codex'}</strong>
        <p>{display.text.slice(0, 1200)}{display.text.length > 1200 ? '…' : ''}</p>
      </div>;
    })}
  </section>;
}

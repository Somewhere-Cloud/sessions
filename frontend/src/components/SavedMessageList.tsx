import { useState } from 'react';
import { changeSavedMessage, type SavedMessageChange } from '../api/sessionsd';
import type { DispatchMessage } from '../types';
import { Button } from './ui/Button';

const OPERATION_PREFIX = 'sessions-message:';

// Saved next-turn messages beside the composer. A message the runner saved
// (its id is a Sessions operation) can be edited, sent next or removed until
// the runner dispatches it. The runner decides each change under the lock
// that dispatches, so a refusal is shown as given and nothing is resent.
export function SavedMessageList({ messages, sessionId, changeable, providerName }: {
  messages: DispatchMessage[]; sessionId: string; changeable: boolean; providerName: string;
}): JSX.Element {
  return (
    <details>
      <summary>{changeable ? 'View or change saved messages' : 'View saved messages'}</summary>
      {messages.map((message, index) => {
        const operationId = changeable && message.blockId?.startsWith(OPERATION_PREFIX) ? message.blockId.slice(OPERATION_PREFIX.length) : '';
        return operationId
          ? <SavedMessageItem key={message.id} message={message} sessionId={sessionId} operationId={operationId} first={index === 0} providerName={providerName} />
          : <p key={message.id} className="remote-queued-text">{message.content}</p>;
      })}
    </details>
  );
}

function SavedMessageItem({ message, sessionId, operationId, first, providerName }: {
  message: DispatchMessage; sessionId: string; operationId: string; first: boolean; providerName: string;
}): JSX.Element {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(message.content);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const change = async (request: SavedMessageChange): Promise<void> => {
    if (busy) return;
    setBusy(true); setError('');
    try {
      await changeSavedMessage(sessionId, request);
      setEditing(false);
    } catch (reason) {
      setError(reason instanceof Error && reason.message.trim() ? reason.message : 'Saved message was not changed.');
    } finally {
      setBusy(false);
    }
  };
  if (editing) {
    return (
      <div className="remote-queued-item is-editing">
        <textarea className="remote-queued-editor" value={draft} disabled={busy} aria-label="Edit saved message"
          onChange={(event) => setDraft(event.target.value)} rows={Math.min(8, Math.max(2, draft.split('\n').length))} />
        <div className="remote-queued-actions">
          <Button size="compact" variant="primary" disabled={busy || !draft.trim() || draft === message.content}
            onClick={() => void change({ action: 'edit', operation_id: operationId, text: draft })}>{busy ? 'Saving…' : 'Save'}</Button>
          <Button size="compact" variant="ghost" disabled={busy} onClick={() => { setEditing(false); setDraft(message.content); setError(''); }}>Keep original</Button>
        </div>
        {error ? <span role="alert" className="remote-queued-error">{error}</span> : null}
      </div>
    );
  }
  return (
    <div className="remote-queued-item">
      <p className="remote-queued-text">{message.content}</p>
      <div className="remote-queued-actions">
        {first ? null : <Button size="compact" variant="ghost" disabled={busy}
          title={`Move to the front of the saved messages. ${providerName} cannot take a message mid-turn, so it is sent when the current turn ends.`}
          onClick={() => void change({ action: 'move', operation_id: operationId, to: 1 })}>Send next</Button>}
        <Button size="compact" variant="ghost" disabled={busy} title="Change this message before it is dispatched"
          onClick={() => { setDraft(message.content); setEditing(true); setError(''); }}>Edit</Button>
        <Button size="compact" variant="ghost" disabled={busy} title="Remove this message; it will never be sent"
          onClick={() => void change({ action: 'cancel', operation_id: operationId })}>Remove</Button>
      </div>
      {error ? <span role="alert" className="remote-queued-error">{error}</span> : null}
    </div>
  );
}

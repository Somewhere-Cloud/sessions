import { useState, type FormEvent } from 'react';
import type { ApprovalDecision, SessionInfo } from '../types';

interface Props {
  session: SessionInfo;
  // The team route's waiting line, which can be fresher than the session row.
  waiting?: string;
  onApprove?: (lane: SessionInfo, decision: ApprovalDecision) => Promise<void>;
  onAnswer?: (lane: SessionInfo, text: string) => Promise<void>;
  onError: (message: string | null) => void;
}

// childQuestion is the question a live lane stopped to ask, or null when it is
// not asking one. A permission request is answered with the approval buttons.
export function childQuestion(session: SessionInfo, waiting?: string): string | null {
  if (session.exited || session.pendingApproval || session.idleReason !== 'needs-input') return null;
  return waiting?.trim() || session.idleDetail?.trim() || 'Waiting for your answer.';
}

function approvalLabel(summary: string): string {
  return summary.replace(/^Run /, 'run ').replace(/^Change /, 'change ').replace(/^Grant /, 'grant ');
}

// SubagentWaiting answers what a lane is waiting on without opening it: the
// permission it requested, or the question it asked, typed straight to it.
export function SubagentWaiting({ session, waiting, onApprove, onAnswer, onError }: Props): JSX.Element | null {
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState('');
  const [sent, setSent] = useState(false);
  const run = async (action: () => Promise<void>, failure: string): Promise<boolean> => {
    setBusy(true);
    onError(null);
    try {
      await action();
      return true;
    } catch (reason) {
      onError(reason instanceof Error ? reason.message : failure);
      return false;
    } finally {
      setBusy(false);
    }
  };
  if (session.pendingApproval && onApprove) {
    const decide = (decision: ApprovalDecision): void => {
      void run(() => onApprove(session, decision), 'The answer did not reach the lane.');
    };
    return (
      <div className="subagent-approval" role="group" aria-label="Permission request">
        <span>Asks to {approvalLabel(session.pendingApproval.summary)}</span>
        <span className="subagent-approval-actions">
          <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => decide('allow')}>{busy ? 'Answering…' : 'Allow'}</button>
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => decide('allow-session')}>Allow for session</button>
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => decide('deny')}>Decline</button>
        </span>
      </div>
    );
  }
  const question = childQuestion(session, waiting);
  if (!question || !onAnswer) return null;
  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const text = draft.trim();
    if (!text || busy) return;
    if (await run(() => onAnswer(session, text), 'The answer did not reach the lane.')) {
      setDraft('');
      setSent(true);
    }
  };
  return (
    <form className="subagent-question" aria-label="Question from this lane" onSubmit={(event) => void submit(event)}>
      <p className="subagent-question-text">{question}</p>
      <span className="subagent-question-answer">
        <input
          type="text"
          value={draft}
          aria-label="Answer this lane"
          placeholder={sent ? 'Sent. Add more…' : 'Answer…'}
          disabled={busy}
          onChange={(event) => { setDraft(event.target.value); setSent(false); }}
        />
        <button type="submit" className="btn btn-secondary" disabled={busy || !draft.trim()}>{busy ? 'Sending…' : 'Send'}</button>
      </span>
    </form>
  );
}

import { createPortal } from 'react-dom';
import type { SessionInfo } from '../types';
import { handoffRequest, type HandoffRecord } from '../api/sessionsd/handoffs';
import { useProviderHandoff } from '../hooks/useProviderHandoff';
import { ModalDialog } from './ui/ModalDialog';
import { Button } from './ui/Button';
import { Field, Input, Select, Textarea } from './ui/Field';
import { Actions, PageHeader, Status } from './ui/Layout';
import { creationLabel, HandoffCoverage, HandoffProgress, providerName } from './ProviderHandoffPresentation';
import '../styles/provider-handoff.css';

type Handoff = ReturnType<typeof useProviderHandoff>;

export function ProviderHandoffDialog({ session, serverId, onOpen, onClose }: {
  session: SessionInfo; serverId: string; onOpen: (id: string) => void; onClose: () => void;
}): JSX.Element {
  const state = useProviderHandoff(session, serverId, onOpen, onClose);
  const { receipt, saved, busy, error, loaded } = state;
  return createPortal(<ModalDialog className="dialog handoff-dialog" labelledBy="handoff-title" onClose={onClose} dismissOnBackdrop={false}>
    <PageHeader title="Switch provider" titleId="handoff-title" level={2} eyebrow="Continue your work"
      description="Start a fresh conversation with selected context. Your source conversation stays available; drafts and queued work stay there." />
    {!loaded && !error ? <Status>Reading saved switches…</Status> : null}
    {saved.length ? <details className="handoff-saved"><summary>Saved switches ({saved.length})</summary><div className="ui-stack">{saved.map((item, index) =>
      <Button disabled={busy} key={item.request.operationId} onClick={(event) => { const details = event.currentTarget.closest('details'); if (details) details.open = false; state.setReceipt(item); state.setCaptureUnconfirmed(false); state.setPreview(null); state.setText(''); }}>
        {providerName(item.request.destinationProvider)} · {creationLabel(item.creation)} · Switch {index + 1}
      </Button>)}</div></details> : null}
    <div className="handoff-destination"><span>Continue with</span><strong>{providerName((receipt?.request ?? state.choice).destinationProvider)}</strong></div>
    {receipt ? <><HandoffProgress receipt={receipt} captureUnconfirmed={state.captureUnconfirmed} /><HandoffNext state={state} session={session} /></> : <HandoffReview state={state} serverId={serverId} />}
    {busy ? <Status>Saving or checking this switch…</Status> : null}
    {error ? <Status tone="warning" live="assertive">{error}</Status> : null}
    {state.errorDetails ? <details className="handoff-details"><summary>Error details</summary><p>{state.errorDetails}</p></details> : null}
    <Actions><Button variant="ghost" onClick={onClose}>Close</Button></Actions>
  </ModalDialog>, document.body);
}

function HandoffReview({ state, serverId }: { state: Handoff; serverId: string }): JSX.Element {
  const { choice, preview, busy, loaded, run } = state;
  return <div className="ui-stack">
    <details className="handoff-options"><summary>Model and reasoning options</summary><div className="ui-stack">
      <Field label="Exact model ID" hint="Leave blank to use the provider default."><Input value={choice.model} disabled={busy}
        onChange={(event) => { state.setChoice({ ...choice, model: event.target.value }); state.setPreview(null); }} /></Field>
      <Field label="Reasoning effort"><Select value={choice.effort} disabled={busy}
        onChange={(event) => { state.setChoice({ ...choice, effort: event.target.value }); state.setPreview(null); }}>
        <option value="">Automatic</option>{[['low', 'Low'], ['medium', 'Medium'], ['high', 'High'], ['xhigh', 'Extra high'], ['max', 'Maximum']].map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </Select></Field>
    </div></details>
    {preview ? <HandoffCoverage record={preview} preview /> : <p className="ui-description">Review the text available to carry forward before preparing the new conversation. Full history and attachments stay with the source.</p>}
    <Actions><Button disabled={busy || !loaded} onClick={() => void run(async () => {
      state.setPreview(await handoffRequest<HandoffRecord>('/preview', serverId, choice));
    })}>{preview ? 'Update preview' : 'Review switch'}</Button>
      {preview ? <Button variant="primary" disabled={busy} onClick={() => void run(state.start)}>Prepare {providerName(choice.destinationProvider)} conversation</Button> : null}
    </Actions>
  </div>;
}

function HandoffNext({ state, session }: { state: Handoff; session: SessionInfo }): JSX.Element {
  const { receipt, busy, canSend, text, run } = state;
  if (!receipt) return <></>;
  return <div className="ui-stack">
    <Actions><Button loading={busy} onClick={() => void run(state.refresh)}>Check status</Button>
      {receipt.targetSessionId ? <Button onClick={() => void run(() => state.openSegment(receipt.targetSessionId!))} disabled={busy}>Open target conversation</Button> : null}
      {receipt.request.sourceSessionId !== session.id ? <Button disabled={busy} onClick={() => void run(() => state.openSegment(receipt.request.sourceSessionId))}>Open source conversation</Button> : null}
    </Actions>
    <p className="field-help">Status checks run briefly while this dialog is open. Checking never resends context or a request.</p>
    {state.statusNotice ? <Status tone="warning">{state.statusNotice}</Status> : null}
    {canSend ? <div className="handoff-compose ui-stack">
      <Field label="First request" hint={receipt.prompt?.status === 'not-delivered' ? 'The target refused this request. A retry uses the same saved request identity and text.' : 'Send only when ready. Required images and files must be reattached in the target conversation.'}>
        <Textarea rows={3} value={text} disabled={busy} onChange={(event) => state.setText(event.target.value)} />
      </Field>
      <Actions><Button variant="primary" disabled={busy || !text.trim()} onClick={() => void run(state.sendFirstRequest)}>{receipt.prompt?.status === 'not-delivered' ? 'Retry first request' : 'Send first request'}</Button></Actions>
    </div> : null}
  </div>;
}

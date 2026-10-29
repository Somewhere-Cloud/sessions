import type { HandoffRecord } from '../api/sessionsd/handoffs';
import { Status } from './ui/Layout';

export const providerName = (value: string): string => value === 'codex' ? 'Codex' : 'Claude';
export function creationLabel(value: string): string {
  return ({ created: 'Conversation prepared', pending: 'Preparing conversation…', unknown: 'Preparation needs checking' })[value] ?? 'Preparation needs attention';
}
const contextLabels: Record<string, string> = {
  applied: 'Text transferred to provider', 'staged-for-first-request': 'Text captured · ready for first request',
  'sent-with-request': 'Text sent with first request', pending: 'Waiting for context', unknown: 'Context needs checking'
};
const promptLabels: Record<string, string> = {
  'not-sent': 'Not sent', 'not-delivered': 'Not delivered', accepted: 'Accepted by target',
  unknown: 'Delivery uncertain', unreadable: 'Delivery receipt unavailable', pending: 'Checking delivery'
};

export function HandoffCoverage({ record, preview = false }: { record: HandoffRecord; preview?: boolean }): JSX.Element {
  return <section className="handoff-coverage" aria-label={preview ? 'Context preview' : 'Captured context'}>
    <strong>{preview ? 'Context preview' : 'Captured context'}</strong>
    <div className="handoff-counts"><span><b>{record.coverage.included}</b> text messages</span>
      <span><b>{record.coverage.omitted}</b> omitted</span><span>{(record.coverage.bytes / 1024).toFixed(1)} KiB</span></div>
    <p className="ui-description">Latest complete authored text messages, up to 24 KiB. Sources over 32 MiB need a reviewed briefing.</p>
    <p className="ui-description">Images, files and tool output are not copied. Reattach any assets you need. File references still point to the source computer.</p>
    {preview ? <p className="field-help">This is a preview. Context is captured when you prepare the conversation; counts may change if the source advances.</p> : null}
    <details><summary>Context limits</summary><p>{record.coverage.limitation}</p></details>
  </section>;
}

export function HandoffProgress({ receipt, captureUnconfirmed = false }: { receipt: HandoffRecord; captureUnconfirmed?: boolean }): JSX.Element {
  return <section className="ui-stack" aria-label="Provider switch progress">
    <ol className="handoff-progress" aria-live="polite">
      <li><span>Conversation</span><strong>{creationLabel(receipt.creation)}</strong></li>
      <li><span>Context</span><strong>{contextLabels[receipt.context ?? 'pending'] ?? 'Context needs attention'}</strong></li>
      <li><span>First request</span><strong>{promptLabels[receipt.prompt?.status ?? 'not-sent'] ?? 'Delivery needs checking'}</strong></li>
      <li><span>Response</span><strong>{receipt.response === 'assistant-observed' ? 'Assistant text observed' : 'No assistant text observed yet'}</strong></li>
    </ol>
    <p className="field-help">Response evidence comes from retained target activity after this request began. It does not confirm recall of the captured context.</p>
    {receipt.recovery ? <Status tone="warning">{receipt.recovery}</Status> : null}
    {receipt.prompt?.reason ? <Status tone="warning">{receipt.prompt.reason}</Status> : null}
    {receipt.error ? <Status tone="warning" live="assertive">{receipt.error}</Status> : null}
    {captureUnconfirmed ? <Status tone="warning">The preparation reply was not confirmed. Check this switch’s status to learn whether context was captured. Nothing will be resent.</Status> : <HandoffCoverage record={receipt} />}
    <details className="handoff-details"><summary>Switch details</summary><dl>
      <dt>Operation</dt><dd><code>{receipt.request.operationId}</code></dd>
      <dt>Source history</dt><dd><code>{receipt.artifact.sourceHistoryId}</code></dd>
      <dt>Model</dt><dd>{receipt.artifact.destinationModel || 'Provider default'}</dd>
      {receipt.nativeTargetId ? <><dt>Native conversation</dt><dd><code>{receipt.nativeTargetId}</code></dd></> : null}
      <dt>Recorded states</dt><dd>{receipt.creation} / {receipt.context ?? 'pending'} / {receipt.prompt?.status ?? 'not-sent'} / {receipt.activity ?? 'not-observed'}</dd>
    </dl><p className="field-help">Messages sent directly in the target have their own delivery receipts.</p></details>
  </section>;
}

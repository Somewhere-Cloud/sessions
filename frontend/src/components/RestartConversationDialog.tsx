import { Field, Select } from './ui/Field';
import { Button } from './ui/Button';
import { Actions, PageHeader, Status } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { previewRestart, restartConversation, type RestartPreview, type RestartResult } from '../api/sessionsd/restart';
import { getActiveServer, getServer, serverDisplayName } from '../lib/servers';
import { draftStorageKey, flushDraft, readDraft, saveDraft } from '../lib/draftStore';
import { sessionMode } from '../lib/sessionMode';
import { resolvedSessionLabel } from '../lib/tabLabels';
import type { SessionInfo } from '../types';
import type { RestartConversationProps } from './RestartConversation';
import { ModalDialog } from './ui/ModalDialog';
import '../styles/restart-conversation.css';

type RestartChoice = NonNullable<RestartPreview['recordedRestart']>;

function preserveRestartDraft(machineId: string, source: string, destination: string): void {
  flushDraft(draftStorageKey(machineId, source));
  const draft = readDraft(draftStorageKey(machineId, source));
  if (draft.warning) throw new Error(draft.warning);
  if (!draft.text) return;
  const saved = saveDraft(draftStorageKey(machineId, destination), draft.text);
  if (!saved.saved) throw new Error(`${saved.warning} Your draft remains on the original tab. The replacement is running; copy your draft before opening it.`);
}

function RestartChoices({ session, choice, locked, onPermissions, onRuntimeMode, onRemoteControl }: {
  session: SessionInfo;
  choice: RestartChoice;
  locked: boolean;
  onPermissions: (value: 'constrained' | 'full') => void;
  onRuntimeMode: (value: 'rich' | 'terminal') => void;
  onRemoteControl: (value: boolean) => void;
}): JSX.Element {
  return <>
    <Field label="Permissions"><Select aria-label="Restart permissions" value={choice.permissions} disabled={locked} onChange={(event) => onPermissions(event.target.value as 'constrained' | 'full')}>
      <option value="constrained">Ask for approval</option><option value="full">Full access (YOLO)</option>
    </Select></Field>
    {choice.permissions === 'full' ? <p>Full access lets this conversation run commands without approval. This changes only the replacement runtime.</p> : null}
    <Field label="Runtime"><Select aria-label="Restart runtime" value={choice.runtimeMode ?? sessionMode(session)} disabled={locked || choice.remoteControl} onChange={(event) => onRuntimeMode(event.target.value as 'rich' | 'terminal')}><option value="rich">Rich</option><option value="terminal">Terminal</option></Select></Field>
    {session.tool === 'claude-code' ? <label><input type="checkbox" checked={choice.remoteControl} disabled={locked} onChange={(event) => onRemoteControl(event.target.checked)} /> Remote Control (Claude Terminal; requires consent in Settings)</label> : null}
  </>;
}

const permissionLabel = { constrained: 'Ask for approval', full: 'Full access (YOLO)' } as const;
const runtimeLabel = { rich: 'Rich', terminal: 'Terminal' } as const;

/** A recorded restart's choices, read-only, and whether retrying still interrupts anything. */
function RecordedRestart({ session, choice, sourceRunning }: { session: SessionInfo; choice: RestartChoice; sourceRunning: boolean }): JSX.Element {
  return <>
    <p role="status">A restart is already recorded. Retrying repeats its choices and never starts a second replacement.</p>
    <dl className="restart-recorded-choices" aria-label="Recorded choices">
      <dt>Permissions</dt><dd>{permissionLabel[choice.permissions]}</dd>
      <dt>Runtime</dt><dd>{choice.runtimeMode ? runtimeLabel[choice.runtimeMode] : `${runtimeLabel[sessionMode(session)]}, unchanged`}</dd>
      {session.tool === 'claude-code' ? <><dt>Remote Control</dt><dd>{choice.remoteControl ? 'On' : 'Off'}</dd></> : null}
    </dl>
    {choice.permissions === 'full' ? <p>Full access lets the replacement run commands without approval.</p> : null}
    {sourceRunning
      ? <p>Retrying ends runtime <code>{session.id}</code> first; {session.working ? 'its current turn and ' : ''}running commands are interrupted.</p>
      : <p>The original runtime has already ended, so retrying interrupts nothing.</p>}
  </>;
}

/** The verb the daemon's preview offers, so the heading matches the one action button. */
function dialogVerb(unavailable: RestartPreview | null, offer: { label: string } | null): string {
  if (!offer) return 'Restart';
  return unavailable?.action === 'open' ? 'Open running copy of' : 'Resume';
}

/** What the daemon offers instead when this record has no runtime to end. */
function unavailableAction(preview: RestartPreview, canResume: boolean): { label: string; target: string } | null {
  if (preview.action === 'resume' && canResume) return { label: 'Resume conversation', target: preview.targetSessionId ?? preview.sourceSessionId };
  if (preview.action === 'open' && preview.targetSessionId) return { label: 'Open running copy', target: preview.targetSessionId };
  return null;
}

/** A runtime id is the confirmation boundary; never follow a retired row's successor. */
export function RestartConversationDialog({ session, onOpen, onResume, serverId, initialRemoteControl, initialRuntimeMode, onClose }: RestartConversationProps & { onClose: () => void }): JSX.Element | null {
  // Changing access is an explicit choice; the default keeps what this runtime has.
  const [permissions, setPermissions] = useState<'constrained' | 'full'>(session.permissions === 'constrained' ? 'constrained' : 'full');
  const [remoteControl, setRemoteControl] = useState(initialRemoteControl ?? session.args.some((arg) => arg === '--remote-control' || arg.startsWith('--remote-control=')));
  const [runtimeMode, setRuntimeMode] = useState<'rich' | 'terminal'>(initialRuntimeMode ?? sessionMode(session));
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<RestartResult | null>(null);
  const [preview, setPreview] = useState<RestartPreview | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const machineId = serverId ?? getActiveServer().id;
  const machineName = serverDisplayName(getServer(machineId), true);
  useEffect(() => {
    const controller = new AbortController();
    void previewRestart(session.id, machineId, controller.signal).then(setPreview).catch((reason) => {
      if (!controller.signal.aborted) setPreviewError(`Account check unavailable: ${String(reason)}. Restart still uses ${machineName}'s current login.`);
    });
    return () => controller.abort();
  }, [session.id, machineId, machineName]);
  const locked = useRef(false);
  // Older daemons omit restartable; they answered 404 for a record that was not live.
  const unavailable = !result && preview?.restartable === false ? preview : null;
  // A recorded restart finishes only with its exact choices; different ones are refused.
  const recorded = preview?.action === 'retry-restart' ? preview.recordedRestart : undefined;
  const choice: RestartChoice = recorded ?? { permissions, remoteControl, runtimeMode: remoteControl ? 'terminal' : runtimeMode };
  const offer = unavailable ? unavailableAction(unavailable, Boolean(onResume)) : null;
  // Older daemons omit state; only a runtime that is still there can be interrupted.
  const sourceRunning = !preview?.state || preview.state === 'running' || preview.state === 'unreachable';
  const show = async (id: string, elsewhere: string, unlisted: string): Promise<void> => {
    if (getActiveServer().id !== machineId) throw new Error(elsewhere);
    const { useSessions } = await import('../store/sessions');
    // The restart receipt can arrive before the periodic session list. Opening
    // an unknown row lets that poll prune the new tab and clear its selection.
    await useSessions.getState().refresh(machineId);
    const current = useSessions.getState();
    if (getActiveServer().id !== machineId || current.serverId !== machineId || !current.sessions.some((row) => row.id === id)) throw new Error(unlisted);
    onOpen(id); onClose();
  };
  const reopen = async (id: string): Promise<void> => {
    preserveRestartDraft(machineId, session.id, id);
    await show(id, 'The replacement is running on the original computer. Select that computer, then choose Open replacement.',
      'The replacement is running but its computer has not returned the new conversation yet. Reconnect, then choose Open replacement.');
  };
  const follow = async (): Promise<void> => {
    if (!offer || busy) return;
    setBusy(true); setError(null);
    try {
      if (unavailable?.action === 'open') {
        await show(offer.target, `The running copy is on ${machineName}. Select that computer, then choose Open running copy.`,
          `${machineName} has not listed the running copy yet. Reconnect, then choose Open running copy.`);
        return;
      }
      if (getActiveServer().id !== machineId) throw new Error(`This conversation is saved on ${machineName}. Select that computer, then resume it.`);
      onResume?.(session); onClose();
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); }
    finally { setBusy(false); }
  };
  const restart = async (): Promise<void> => {
    if (locked.current) return;
    locked.current = true; setSubmitted(true); setBusy(true); setError(null);
    try {
      flushDraft(draftStorageKey(machineId, session.id));
      const draft = readDraft(draftStorageKey(machineId, session.id));
      if (draft.warning) throw new Error(draft.warning);
      const next = await restartConversation(session.id, choice.permissions, choice.remoteControl, machineId, choice.runtimeMode);
      setResult(next);
      if (next.error) throw new Error(next.error);
      if (!next.laneId) throw new Error('No replacement runtime was reported. Retry the same choices to recover the recorded restart.');
      if (next.partial) {
        setError(next.adoption?.warning ?? 'The replacement is running, but its history link needs repair. Retry these same choices to finish its record.');
        return;
      }
      await reopen(next.laneId);
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'Restart failed. Retry the same choices to check the recorded operation.'); }
    finally { locked.current = false; setBusy(false); }
  };
  return createPortal(<ModalDialog className="dialog restart-conversation-dialog" labelledBy={`restart-title-${session.id}`} onClose={onClose} dismissible={!busy} dismissOnBackdrop={false}>
        <PageHeader level={2} titleId={`restart-title-${session.id}`} title={<>{dialogVerb(unavailable, offer)} “{resolvedSessionLabel(session)}”</>} />
        <p><strong>On {machineName}</strong> · This does not move the conversation to another computer.</p>
        {unavailable ? <p role="status">{unavailable.reason || 'This runtime is not running here, so there is nothing to restart.'}</p> : <>
        {recorded ? <RecordedRestart session={session} choice={recorded} sourceRunning={sourceRunning} /> : <>
        <p>Keeps the saved conversation and your unsent draft. Running commands are interrupted. Your draft is never sent automatically.</p>
        <details><summary>What restarts</summary><p>This ends only runtime <code>{session.id}</code>{session.pid ? ` (PID ${session.pid})` : ''} and reopens the same {session.tool === 'codex' ? 'Codex' : 'Claude'} conversation.</p><p>Login profile: {session.profile || 'default'} · Model: {session.model || 'current'}</p></details>
        </>}
        <p>Uses this profile’s current sign-in, not necessarily the account that originally opened this chat.{preview?.savedLoginEmail ? ` Saved sign-in: ${preview.savedLoginEmail} (not verified live).` : ''}</p>
        {preview?.warning || previewError ? <p role="status">{preview?.warning ?? previewError}</p> : null}
        {choice.remoteControl && !preview?.accountChanged ? <p>A changed Claude login may use a different Remote Control link.</p> : null}
        {recorded ? null : <>
        {session.working ? <p role="status">This agent is working. Restart interrupts its current turn.</p> : null}
        <RestartChoices session={session} choice={choice} locked={busy || submitted}
          onPermissions={setPermissions} onRuntimeMode={setRuntimeMode} onRemoteControl={setRemoteControl} />
        </>}
        </>}
        {error ? <Status tone="danger" live="assertive">{error}</Status> : null}
        {result?.sourceEnded ? <p role="status">Original runtime ended.{result.laneId ? ` Replacement: ${result.laneId}.` : ' Replacement creation is incomplete.'}</p> : null}
        <Actions>
          <Button disabled={busy} onClick={onClose}>{result || unavailable ? 'Close' : 'Cancel'}</Button>
          {result?.laneId ? <Button disabled={busy} onClick={() => { void reopen(result.laneId!).catch((reason) => setError(String(reason))); }}>Open replacement</Button> : null}
          {unavailable ? offer ? <Button variant="primary" loading={busy} onClick={() => void follow()}>{offer.label}</Button> : null
            : <Button variant="primary" loading={busy} disabled={!(preview || previewError)} onClick={() => void restart()}>{busy ? 'Restarting…' : submitted ? 'Retry same restart' : recorded ? 'Retry recorded restart' : 'End this runtime and reopen'}</Button>}
        </Actions>
    </ModalDialog>, document.body);
}

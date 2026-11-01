import { Surface, Actions } from './ui/Layout';
import { Button } from './ui/Button';
import { useEffect, useState } from 'react';
import { retryProviderSession, stopProviderRetry, updateSessionLimitRecovery } from '../api/sessionsd';
import type { LimitRecovery, ProviderFailureKind, ProviderRetry } from '../types';
import { resumesAfterLimit, returnTimeLabel } from '../lib/returnTime';

interface Props {
  sessionId: string;
  failureKind: ProviderFailureKind;
  detail?: string;
  /**
   * The provider's own line this claim rests on. Shown, not implied: a session
   * that was logged in and working once carried "Claude is not logged in"
   * because those words appeared in the agent's own grep output, and there was
   * no way for the person to see where the claim had come from.
   */
  evidence?: string;
  retry?: ProviderRetry;
  /** The daemon's plan to resume after a usage limit; shown calmly instead of trouble. */
  limitRecovery?: LimitRecovery | null;
  rich: boolean;
  /**
   * 'card' is the full control. 'banner' is the one-line form used when the
   * terminal is already on screen, where a card offering to open the terminal
   * would be repeating what the person is looking at.
   */
  placement?: 'card' | 'banner';
  onOpenTerminal: () => void;
  /** Opens Accounts. Connecting the account is the safe fix for an auth failure. */
  onConnectAccount?: () => void;
}

function retryCountdown(nextAt: number, now: number): number {
  return Math.max(0, Math.ceil((nextAt - now) / 1000));
}

export function ProviderFaultCard({
  sessionId, failureKind, detail, evidence, retry, limitRecovery, rich, placement = 'card', onOpenTerminal, onConnectAccount
}: Props): JSX.Element {
  const resumeAt = resumesAfterLimit({ failureKind, retry, limitRecovery });
  if (resumeAt !== null) {
    return <LimitResumeNotice sessionId={sessionId} resumeAt={resumeAt} rich={rich} banner={placement === 'banner'} />;
  }
  return <ProviderTroubleCard {...{ sessionId, failureKind, detail, evidence, retry, rich, placement, onOpenTerminal, onConnectAccount }} />;
}

function ProviderTroubleCard({
  sessionId, failureKind, detail, evidence, retry, rich, placement = 'card', onOpenTerminal, onConnectAccount
}: Props): JSX.Element {
  const [now, setNow] = useState(Date.now());
  const [busy, setBusy] = useState<'retry' | 'stop' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const terminalIsVisible = placement === 'banner';

  useEffect(() => {
    if (!retry) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [retry]);

  const act = async (action: 'retry' | 'stop'): Promise<void> => {
    if (busy) return;
    setBusy(action);
    setError(null);
    try {
      if (action === 'retry') await retryProviderSession(sessionId);
      else await stopProviderRetry(sessionId);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sessions could not change this retry.');
    } finally {
      setBusy(null);
    }
  };

  const guidance = failureKind === 'auth'
    ? authGuidance(rich, terminalIsVisible, Boolean(onConnectAccount))
    : retry
      ? `Retrying in ${retryCountdown(retry.nextAt, now)}s (attempt ${retry.attempt} of ${retry.max})`
      : rich
        ? 'Retry'
        : 'Send your message again when the provider is back';
  const text = detail || 'The provider did not complete this turn.';

  if (terminalIsVisible) {
    return (
      <div className="provider-fault-banner" role="status" aria-label="Provider trouble">
        <strong>{text}</strong>
        <span className="provider-fault-banner-hint">{guidance}</span>
        {failureKind === 'auth' && onConnectAccount ? (
          <Button type="button" variant="primary" onClick={onConnectAccount}>Connect account</Button>
        ) : null}
        {evidence ? <ProviderFaultEvidence evidence={evidence} /> : null}
      </div>
    );
  }

  return (
    <Surface className="provider-control-card is-provider-fault" role="group" aria-label="Provider trouble">
      <span className="provider-control-card-title">Provider trouble</span>
      <p className="provider-control-card-text">{text}</p>
      {evidence ? <ProviderFaultEvidence evidence={evidence} /> : null}
      <span className="provider-control-card-hint" aria-live="polite">{guidance}</span>
      <Actions className="provider-control-card-choices" role="toolbar" aria-label="Provider recovery">
        {failureKind === 'auth' && onConnectAccount ? (
          <Button type="button" variant="primary" onClick={onConnectAccount}>Connect account</Button>
        ) : null}
        {failureKind === 'auth' && (!rich || !onConnectAccount) ? (
          <Button type="button" variant={onConnectAccount ? 'secondary' : 'primary'} onClick={onOpenTerminal}>Open Terminal</Button>
        ) : rich ? (
          <Button type="button" variant="primary" disabled={busy !== null} onClick={() => void act('retry')}>
            {busy === 'retry' ? 'Retrying…' : 'Retry now'}
          </Button>
        ) : null}
        {retry ? (
          <Button type="button" variant="secondary" disabled={busy !== null} onClick={() => void act('stop')}>
            {busy === 'stop' ? 'Stopping…' : 'Stop retrying'}
          </Button>
        ) : null}
      </Actions>
      {error ? <span className="provider-control-card-hint is-error" role="alert">{error}</span> : null}
    </Surface>
  );
}

// Authentication is blocked work, not an idle session. Connecting the account
// is always safe; a terminal login applies only where the provider's own
// terminal exists. Sessions never clears the error on the person's behalf.
function authGuidance(rich: boolean, terminalIsVisible: boolean, canConnect: boolean): string {
  if (!canConnect) return terminalIsVisible ? 'Log in below' : 'Open the terminal to log in';
  if (rich) return 'Connect the account, then retry this turn';
  return terminalIsVisible ? 'Log in below, or connect the account' : 'Connect the account, or open the terminal to log in';
}

/** What the claim rests on, in the provider's own words. */
function ProviderFaultEvidence({ evidence }: { evidence: string }): JSX.Element {
  return (
    <code className="provider-fault-evidence" title={`The provider printed this: ${evidence}`}>
      {evidence}
    </code>
  );
}

// A usage limit Sessions will resume by itself is not trouble: nothing is
// asked of the person. It names the time and offers the two choices that
// change it, each the GUI form of a CLI command (`sessions retry ID`,
// `sessions limits off --lane ID`).
function LimitResumeNotice({ sessionId, resumeAt, rich, banner }: {
  sessionId: string;
  resumeAt: number;
  rich: boolean;
  banner: boolean;
}): JSX.Element {
  const [busy, setBusy] = useState<'resume' | 'cancel' | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const when = returnTimeLabel(resumeAt);
  const act = async (action: 'resume' | 'cancel'): Promise<void> => {
    if (busy) return;
    setBusy(action);
    setMessage(null);
    try {
      if (action === 'resume') await retryProviderSession(sessionId);
      else {
        await updateSessionLimitRecovery(sessionId, 'off');
        setMessage('Sessions will not resume this session on its own. Send a message when you want to continue.');
      }
    } catch (reason) {
      setMessage(reason instanceof Error ? reason.message : 'Sessions could not change this plan.');
    } finally {
      setBusy(null);
    }
  };
  const text = `Usage limit reached. Resumes at ${when}.`;
  const hint = 'Sessions will send \u201cContinue where you left off.\u201d when the limit resets.';
  const choices = (
    <Actions className="provider-control-card-choices" role="toolbar" aria-label="Limit recovery">
      {rich ? (
        <Button type="button" variant="secondary" disabled={busy !== null} onClick={() => void act('resume')}>
          {busy === 'resume' ? 'Retrying…' : 'Retry now'}
        </Button>
      ) : null}
      <Button type="button" variant="ghost" disabled={busy !== null} onClick={() => void act('cancel')}>
        {busy === 'cancel' ? 'Saving…' : 'Don\u2019t resume'}
      </Button>
    </Actions>
  );
  if (banner) {
    return (
      <div className="provider-fault-banner is-limit-resume" role="status" aria-label="Usage limit">
        <strong>{text}</strong>
        <span className="provider-fault-banner-hint">{message ?? hint}</span>
        {choices}
      </div>
    );
  }
  return (
    <Surface className="provider-control-card is-limit-resume" role="group" aria-label="Usage limit">
      <span className="provider-control-card-title">Resumes at {when}</span>
      <p className="provider-control-card-text">The provider usage limit stopped this turn.</p>
      <span className="provider-control-card-hint" aria-live="polite">{message ?? hint}</span>
      {choices}
    </Surface>
  );
}

import { Surface, Actions } from './ui/Layout';
import { Button } from './ui/Button';
import { useState } from 'react';

interface Props {
  providerName: 'Claude' | 'Codex';
  onResume?: () => void | Promise<void>;
  onClose: () => Promise<void>;
}

export function LostConversationCard({ providerName, onResume, onClose }: Props): JSX.Element {
  const [closing, setClosing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = async (): Promise<void> => {
    if (closing) return;
    setClosing(true);
    setError(null);
    try {
      await onClose();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sessions could not close this lost record.');
      setClosing(false);
    }
  };

  return (
    <Surface className="provider-control-card is-lost-conversation" role="group" aria-label="Lost conversation recovery">
      <span className="provider-control-card-title">Conversation saved</span>
      <p className="provider-control-card-text">Your {providerName} conversation is saved. Resume it to keep going.</p>
      <Actions className="provider-control-card-choices" role="toolbar" aria-label="Lost conversation actions">
        <Button type="button" variant="primary" disabled={!onResume || closing} onClick={() => void onResume?.()}>Resume conversation…</Button>
        <Button type="button" variant="secondary" disabled={closing} onClick={() => void close()}>{closing ? 'Closing…' : 'Close'}</Button>
      </Actions>
      {error ? <span className="provider-control-card-hint is-error" role="alert">{error}</span> : null}
    </Surface>
  );
}

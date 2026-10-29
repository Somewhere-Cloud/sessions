import { Button, IconButton } from './ui/Button';
import { ModalDialog } from './ui/ModalDialog';
import { PageHeader } from './ui/Layout';
import { useId, useState } from 'react';
import { createPortal } from 'react-dom';
import type { MachineAccounts } from '../lib/accountRollup';
import { ProviderMark } from './ProviderBadge';

interface Props {
  busy: boolean;
  computers: MachineAccounts[];
  initialComputer: string;
  error: string | null;
  onAdd: (computer: string, tool: 'claude' | 'codex', label: string) => void;
  onCancel: () => void;
}

export function AddAccountDialog({ busy, computers, initialComputer, error, onAdd, onCancel }: Props): JSX.Element {
  const [tool, setTool] = useState<'claude' | 'codex' | null>(null);
  const [computer, setComputer] = useState(() => computers.some((machine) => machine.serverId === initialComputer)
    ? initialComputer : computers[0]?.serverId ?? '');
  const titleId = useId();
  const machineName = computers.find((machine) => machine.serverId === computer)?.machineName;
  return createPortal(
    <ModalDialog className="dialog account-picker" labelledBy={titleId} onClose={onCancel} dismissible={!busy}>
        <PageHeader title="Add account" titleId={titleId} level={2} description="Your subscriptions, together."
          actions={<IconButton aria-label="Close account picker" disabled={busy} onClick={onCancel}>×</IconButton>} />
        <div className="account-picker-body">
          {computers.length > 1 ? <div className="account-picker-computers" role="group" aria-label="Computer">
            {computers.map((machine) => <Button type="button" key={machine.serverId} disabled={busy}
              aria-pressed={computer === machine.serverId} onClick={() => setComputer(machine.serverId)}>{machine.machineName}</Button>)}
          </div> : <p className="account-picker-location">Connecting on {machineName ?? 'this computer'}</p>}
          <div className="account-picker-providers" role="group" aria-label="Provider">
            <ProviderChoice tool="claude" selected={tool === 'claude'} busy={busy} onSelect={setTool} />
            <ProviderChoice tool="codex" selected={tool === 'codex'} busy={busy} onSelect={setTool} />
          </div>
          {tool === 'codex' ? <DeviceSignInHelp /> : <p className="account-picker-note">Sign in with the provider. No API key needed. You can rename the account afterwards.</p>}
          {error ? <p className="settings-message" role="alert">{error}</p> : null}
        </div>
        <footer className="account-picker-footer">
          <span>Your other accounts stay signed in.</span>
          <Button type="button" variant="primary" loading={busy} disabled={busy || !tool || !machineName}
            onClick={() => { if (tool) onAdd(computer, tool, ''); }}>{busy ? 'Preparing…' : tool ? `Sign in to ${tool === 'codex' ? 'ChatGPT' : 'Claude'}` : 'Choose a provider'}</Button>
        </footer>
    </ModalDialog>, document.querySelector('.operations-shell') ?? document.body
  );
}

function ProviderChoice({ tool, selected, busy, onSelect }: {
  tool: 'claude' | 'codex'; selected: boolean; busy: boolean; onSelect: (tool: 'claude' | 'codex') => void;
}): JSX.Element {
  const name = tool === 'codex' ? 'ChatGPT' : 'Claude';
  return <Button type="button" className="account-provider-choice" aria-label={name} aria-pressed={selected}
    disabled={busy} onClick={() => onSelect(tool)}>
    <ProviderMark provider={tool} size={36} />
    <span><strong>{name}</strong><small>{tool === 'codex' ? 'Use your plan with Codex' : 'Use your Claude Code subscription'}</small></span>
    <span className="account-provider-selection" aria-hidden="true">{selected ? '✓' : '+'}</span>
  </Button>;
}

export function DeviceSignInHelp(): JSX.Element {
  return <aside className="account-device-help">
    <strong>One-time ChatGPT setting</strong>
    <p>This sign-in uses a device code. If OpenAI asks, enable device-code sign-in in ChatGPT Settings → Security, then retry. Managed workspaces may need an administrator.</p>
    <a href="https://chatgpt.com/#settings/Security" target="_blank" rel="noreferrer">Open ChatGPT security settings ↗</a>
  </aside>;
}

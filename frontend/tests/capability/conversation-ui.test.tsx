import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { InputBar } from '../../src/components/InputBar';
import { ProviderControlCard } from '../../src/components/ProviderControlCard';
import { installFakeDaemon, makeSession, useFakeMachines } from './fake-daemon';

function setup() {
  const machines = [{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [makeSession({ id: 'polish' })] }];
  installFakeDaemon(machines); useFakeMachines(machines);
}

describe('conversation shared presentation preserves behavior', () => {
  it('keeps IME Enter, legacy composition keys and Shift+Enter out of the send path', async () => {
    const submit = vi.fn().mockResolvedValue(undefined);
    render(<InputBar sessionId="ime-polish" connected send={async () => {}} submitMessage={submit} />);
    const input = screen.getByRole('textbox', { name: 'Message' });
    fireEvent.change(input, { target: { value: 'こんにちは\nsecond line' } });
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 229 });
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
    expect(submit).not.toHaveBeenCalled();
    expect(input).toHaveValue('こんにちは\nsecond line');
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(submit).toHaveBeenCalledExactlyOnceWith('\x1b[200~こんにちは\nsecond line\x1b[201~'));
    await waitFor(() => expect(input).toHaveValue(''));
  });

  it('portals model review, contains keyboard focus and returns to the trigger without changing a draft', async () => {
    setup();
    const user = userEvent.setup(); const configure = vi.fn().mockResolvedValue(undefined);
    render(<main className="operations-shell" data-theme="light"><InputBar sessionId="polish" connected send={async () => {}} submitMessage={async () => {}}
      model="claude-fable-5" effort="high" modelControlSupported onConfigureModel={configure} /></main>);
    const input = screen.getByRole('textbox', { name: 'Message' });
    await user.type(input, 'Keep my draft');
    const trigger = screen.getByRole('button', { name: /Fable.*high/i });
    await user.click(trigger);
    const dialog = screen.getByRole('dialog', { name: 'Claude · next message' });
    expect(dialog.closest('.operations-shell')).toBeNull();
    expect(dialog).toHaveFocus();
    await user.tab({ shift: true });
    expect(within(dialog).getByRole('button', { name: 'Use for next message' })).toHaveFocus();
    await user.click(dialog.querySelector('.model-picker-trigger')!);
    const search = screen.getByRole('combobox', { name: 'Search Claude models' });
    expect(search).toHaveFocus();
    expect(search.closest('[data-modal-owner]')).not.toBeNull();
    await user.keyboard('{Escape}');
    expect(dialog).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(trigger).toHaveFocus(); expect(input).toHaveValue('Keep my draft'); expect(configure).not.toHaveBeenCalled();
    await user.click(trigger);
    await user.selectOptions(screen.getByLabelText('Effort'), 'medium');
    await user.click(screen.getByRole('button', { name: 'Use for next message' }));
    await waitFor(() => expect(configure).toHaveBeenCalledExactlyOnceWith('claude-fable-5', 'medium'));
    expect(input).toHaveValue('Keep my draft');
  });

  it('preserves exact approval decisions and disables every choice while waiting', async () => {
    const user = userEvent.setup(); let finish!: () => void;
    const approve = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    render(<ProviderControlCard detail={null} blockingState={null} trustChoice={null} onOpenTerminal={() => {}}
      approval={{ id: 'decision', kind: 'command', summary: 'Run tests', command: 'npm test' }} onApprove={approve} />);
    await user.click(screen.getByRole('button', { name: 'Allow for this session' }));
    expect(approve).toHaveBeenCalledExactlyOnceWith('allow-session');
    within(screen.getByRole('toolbar')).getAllByRole('button').forEach((button) => expect(button).toBeDisabled());
    await act(async () => finish());
    expect(screen.getByRole('button', { name: 'Decline' })).toBeEnabled();
  });
});

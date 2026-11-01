// CAPABILITY: reversible sidebar actions offer Undo, sessions that moved while
// you were elsewhere read as unread, and a session you are not looking at says
// so with one brief toast.
//
// Everything here goes through the mounted navigator and the real toast layer
// against the fake daemon, so "Undo" is proven to call the daemon's inverse
// operation rather than repaint the row.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import { SessionAttentionLayer } from '../../src/components/SessionAttentionLayer';
import { ToastLayer } from '../../src/components/ToastLayer';
import { useSessions } from '../../src/store/sessions';
import { useToasts } from '../../src/store/toasts';

function machineWith(...sessions: ReturnType<typeof makeSession>[]): FakeMachine {
  return { id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions };
}

function rowFor(name: string): HTMLElement {
  const row = screen.getByText(name, { selector: '.session-nav-title' }).closest('.session-nav-row');
  if (!(row instanceof HTMLElement)) throw new Error(`no row for ${name}`);
  return row;
}

beforeEach(() => {
  useToasts.setState({ toast: null });
  vi.spyOn(document, 'hasFocus').mockReturnValue(true);
});
afterEach(() => useToasts.setState({ toast: null }));

describe('capability: undo after a reversible sidebar action', () => {
  it('pins from the row and Undo unpins through the daemon', async () => {
    const machine = machineWith(makeSession({ id: 'keeper', name: 'Keeper' }));
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Workbench><ToastLayer /></Workbench>);

    await user.click(await screen.findByRole('button', { name: 'Actions for Keeper' }));
    await user.click(await screen.findByRole('menuitem', { name: /^Pin\b/ }));
    await waitFor(() => expect(daemon.session('keeper')?.pinned).toBe(true));

    const toast = await screen.findByRole('status');
    expect(toast).toHaveTextContent('Pinned Keeper');
    await user.click(within(toast).getByRole('button', { name: 'Undo' }));
    await waitFor(() => expect(daemon.session('keeper')?.pinned).toBe(false));
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
  });

  it('sets aside, Escape dismisses, and Cmd+Z undoes the next one', async () => {
    const machine = machineWith(makeSession({ id: 'keeper', name: 'Keeper' }));
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Workbench><ToastLayer /></Workbench>);

    await user.click(await screen.findByRole('button', { name: 'Actions for Keeper' }));
    await user.click(await screen.findByRole('menuitem', { name: /^Set aside/ }));
    await waitFor(() => expect(daemon.session('keeper')?.setAsideAt).not.toBeNull());
    expect(await screen.findByRole('status')).toHaveTextContent('Set aside Keeper');
    fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
    expect(daemon.session('keeper')?.setAsideAt).not.toBeNull();

    await act(async () => { await useSessions.getState().refresh(); });
    const { setAsideWithUndo } = await import('../../src/store/sessionActions');
    await act(async () => { await setAsideWithUndo('keeper', false); });
    expect(await screen.findByRole('status')).toHaveTextContent('Restored Keeper');
    fireEvent.keyDown(document.body, { key: 'z', metaKey: true });
    await waitFor(() => expect(daemon.session('keeper')?.setAsideAt).not.toBeNull());
  });

  it('never runs Undo from Cmd+Z typed in a text field', async () => {
    const machine = machineWith(makeSession({ id: 'keeper', name: 'Keeper' }));
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    render(<Workbench><ToastLayer /><textarea aria-label="draft" /></Workbench>);
    await screen.findByRole('button', { name: 'Actions for Keeper' });
    const { pinWithUndo } = await import('../../src/store/sessionActions');
    await act(async () => { await pinWithUndo('keeper', true); });
    await screen.findByRole('status');
    fireEvent.keyDown(screen.getByLabelText('draft'), { key: 'z', metaKey: true });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(daemon.session('keeper')?.pinned).toBe(true);
  });
});

describe('capability: unread', () => {
  it('draws an unread row and marks a read one unread from the menu', async () => {
    const machine = machineWith(
      makeSession({ id: 'moved', name: 'Moved on', unread: true, attentionAt: Date.now() }),
      makeSession({ id: 'quiet', name: 'Quiet one', unread: false })
    );
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Workbench><ToastLayer /></Workbench>);

    await screen.findByRole('button', { name: 'Actions for Moved on' });
    expect(rowFor('Moved on')).toHaveClass('is-unread');
    expect(rowFor('Quiet one')).not.toHaveClass('is-unread');

    await user.click(screen.getByRole('button', { name: 'Actions for Quiet one' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Mark as unread' }));
    await waitFor(() => expect(daemon.readStateWrites).toContainEqual({ id: 'quiet', unread: true }));
    await waitFor(() => expect(rowFor('Quiet one')).toHaveClass('is-unread'));
    expect(await screen.findByRole('status')).toHaveTextContent('Marked Quiet one unread');
  });

  it('marks the session in view seen and never draws it unread', async () => {
    const machine = machineWith(makeSession({ id: 'open', name: 'Open one', unread: true, attentionAt: Date.now() }));
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    render(<Workbench><SessionAttentionLayer viewingId="open" onOpen={() => {}} /></Workbench>);
    await waitFor(() => expect(daemon.readStateWrites).toContainEqual({ id: 'open', unread: false }));
    await waitFor(() => expect(useSessions.getState().sessions.find((s) => s.id === 'open')?.unread).toBe(false));
  });
});

describe('capability: attention toasts for other sessions', () => {
  async function finishTurn(daemon: ReturnType<typeof installFakeDaemon>, id: string, values: Record<string, unknown> = {}): Promise<void> {
    Object.assign(daemon.session(id)!, { attentionAt: Date.now() + 1_000, unread: true, idleReason: 'completed', ...values });
    await act(async () => { await useSessions.getState().refresh(); });
  }

  function mount(onOpen = vi.fn()) {
    const machine = machineWith(
      makeSession({ id: 'here', name: 'Here', seenAt: Date.now(), unread: false, attentionAt: 1 }),
      makeSession({ id: 'there', name: 'Over there', seenAt: Date.now(), unread: false, attentionAt: 1 }),
      makeSession({ id: 'third', name: 'Third', seenAt: Date.now(), unread: false, attentionAt: 1 })
    );
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    render(<Workbench><SessionAttentionLayer viewingId="here" onOpen={onOpen} /></Workbench>);
    return { daemon, onOpen };
  }

  it('toasts once for a burst, never for the session in view, and Open goes there', async () => {
    const { daemon, onOpen } = mount();
    await screen.findByRole('button', { name: 'Actions for Over there' });
    await finishTurn(daemon, 'here');
    await finishTurn(daemon, 'there');
    await finishTurn(daemon, 'third', { idleReason: 'needs-input' });
    const toast = await screen.findByRole('status', {}, { timeout: 4_000 });
    expect(toast).toHaveTextContent('Third needs you');
    expect(toast).toHaveTextContent('and 1 other session');
    expect(toast).not.toHaveTextContent('Here');
    await userEvent.setup().click(within(toast).getByRole('button', { name: 'Open' }));
    expect(onOpen).toHaveBeenCalledWith('third');
  });

  it('stays quiet while the window is in the background', async () => {
    const { daemon } = mount();
    await screen.findByRole('button', { name: 'Actions for Over there' });
    vi.mocked(document.hasFocus).mockReturnValue(false);
    await finishTurn(daemon, 'there');
    await new Promise((resolve) => setTimeout(resolve, 2_000));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('respects a desktop-notification switch the person turned off', async () => {
    const { daemon } = mount();
    daemon.notify.done = false;
    await screen.findByRole('button', { name: 'Actions for Over there' });
    await act(async () => { window.dispatchEvent(new Event('focus')); });
    await new Promise((resolve) => setTimeout(resolve, 50));
    await finishTurn(daemon, 'there');
    await new Promise((resolve) => setTimeout(resolve, 2_000));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});

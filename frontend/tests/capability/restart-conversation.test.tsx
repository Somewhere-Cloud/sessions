import { useDurableDraft } from '../../src/hooks/useDurableDraft';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RestartConversation, RestartConversationHost } from '../../src/components/RestartConversation';
import { previewRestart, type RestartPreview } from '../../src/api/sessionsd/restart';
import { makeSession } from './fake-daemon';
import { getActiveServer, useServers } from '../../src/lib/servers';
import { draftStorageKey, readDraft, saveDraft } from '../../src/lib/draftStore';
import { useSessions } from '../../src/store/sessions';

// Keep these tests focused on mutating restart requests. The read-only account
// preview has its own host/account capability tests and daemon contract tests.
vi.mock('../../src/api/sessionsd/restart', async (original) => ({
  ...await original<typeof import('../../src/api/sessionsd/restart')>(),
  previewRestart: vi.fn().mockResolvedValue({ accountChanged: false })
}));

const source = makeSession({ id: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', name: 'Restart fixture', tool: 'claude-code', kind: 'claude-structured', model: 'fixture-model', profile: 'fixture-work', permissions: 'constrained', working: true });
const laneId = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb';

function FixtureDraft(): JSX.Element {
  const draft = useDurableDraft('fixture', source.id);
  return <textarea aria-label="Fixture draft" value={draft.text} onChange={(event) => draft.setText(event.target.value)} />;
}

describe('confirmed conversation restart', () => {
  beforeEach(() => {
    useServers.setState({ servers: [{ id: 'fixture', name: 'Fixture', host: '127.0.0.1', port: 8899, isDefault: true }], activeId: 'fixture' });
    useSessions.setState({ serverId: 'fixture', sessions: [source], activeId: source.id, hydrated: true });
  });
  it('shows the exact runtime, selects YOLO, reopens automatically and retains the draft', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); const posted: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
      if (!options?.body) return new Response(JSON.stringify({ sessions: [makeSession({ id: laneId })] }));
      posted.push(JSON.parse(options.body));
      return new Response(JSON.stringify({ ok: true, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId }), { status: 200 });
    }));
    const machine = getActiveServer().id;
    saveDraft(draftStorageKey(machine, source.id), 'My exact unsent draft');
    render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('dialog');
    expect(screen.getByRole('dialog')).toHaveTextContent(source.id);
    expect(screen.getByRole('dialog')).toHaveTextContent('fixture-work');
    expect(screen.getByRole('dialog')).toHaveTextContent('fixture-model');
    expect(screen.getByRole('dialog')).toHaveTextContent('Restart interrupts its current turn');
    expect(posted).toHaveLength(0);
    // The default keeps this runtime's access; full access is an explicit choice.
    expect(screen.getByRole('combobox', { name: 'Restart permissions' })).toHaveValue('constrained');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Restart permissions' }), 'full');
    await user.click(screen.getByRole('checkbox'));
    await user.click(screen.getByRole('button', { name: 'End this runtime and reopen' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(useSessions.getState().sessions.some((row) => row.id === laneId)).toBe(true);
    expect(posted).toEqual([{ sourceSessionId: source.id, confirmSessionId: source.id, permissions: 'full', remoteControl: true, runtimeMode: 'terminal' }]);
    expect(readDraft(draftStorageKey(machine, laneId)).text).toBe('My exact unsent draft');
    expect(readDraft(draftStorageKey(machine, source.id)).text).toBe('My exact unsent draft');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps progress mounted when the source ends, locks choices and retries the same operation', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); const posted: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
      if (!options?.body) return new Response(JSON.stringify({ sessions: [makeSession({ id: laneId })] }));
      posted.push(JSON.parse(options.body)); const partial = posted.length === 1;
      return new Response(JSON.stringify({ ok: !partial, partial, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId,
        adoption: partial ? { warning: 'Replacement is running; its history link needs repair.' } : undefined }), { status: partial ? 202 : 200 });
    }));
    const { rerender } = render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('dialog');
    await user.click(screen.getByRole('button', { name: 'End this runtime and reopen' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('history link needs repair');
    rerender(<><RestartConversation session={{ ...source, exited: true }} onOpen={onOpen} /><RestartConversationHost /></>);
    expect(screen.getByRole('dialog')).toHaveTextContent(`Replacement: ${laneId}`);
    expect(screen.getByRole('combobox', { name: 'Restart permissions' })).toBeDisabled();
    expect(onOpen).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Retry same restart' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith(laneId));
    expect(posted[0]).toEqual(posted[1]);
  });
  it('keeps the submitted choices after a lost response and retries without changing them', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); const posted: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
      if (!options?.body) return new Response(JSON.stringify({ sessions: [makeSession({ id: laneId })] }));
      posted.push(JSON.parse(options.body));
      if (posted.length === 1) throw new TypeError('Fixture response was lost');
      return new Response(JSON.stringify({ ok: true, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId }), { status: 200 });
    }));
    render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('dialog');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Restart permissions' }), 'full');
    await user.click(screen.getByRole('button', { name: 'End this runtime and reopen' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('response was lost');
    expect(screen.getByRole('combobox', { name: 'Restart permissions' })).toBeDisabled();
    expect(onOpen).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Retry same restart' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith(laneId));
    expect(posted[0]).toEqual(posted[1]);
  });

  it('keeps an explicit open action when the replacement list is delayed without recreating it', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); let posted = 0; let listed = false;
    vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
      if (!options?.body) return new Response(JSON.stringify({ sessions: listed ? [makeSession({ id: laneId })] : [] }));
      posted++;
      return new Response(JSON.stringify({ ok: true, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId }));
    }));
    render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('dialog');
    await user.click(screen.getByRole('button', { name: 'End this runtime and reopen' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('replacement is running');
    expect(onOpen).not.toHaveBeenCalled();
    listed = true;
    await user.click(screen.getByRole('button', { name: 'Open replacement' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(posted).toBe(1);
  });

  it('refuses to end the runtime when its latest unsent draft cannot be saved', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    render(<><FixtureDraft /><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.type(screen.getByRole('textbox', { name: 'Fixture draft' }), 'Keep my newest edits');
    const storage = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Fixture storage full'); });
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('dialog');
    await user.click(screen.getByRole('button', { name: 'End this runtime and reopen' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('original runtime is still running');
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Fixture draft' })).toHaveValue('Keep my newest edits');
    expect(onOpen).not.toHaveBeenCalled();
    storage.mockRestore();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
  });

  it('keeps the constrained default when the person does not change it', async () => {
    const user = userEvent.setup(); const posted: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url, options) => {
      if (!options?.body) return new Response(JSON.stringify({ sessions: [makeSession({ id: laneId })] }));
      posted.push(JSON.parse(options.body));
      return new Response(JSON.stringify({ ok: true, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId }));
    }));
    render(<><RestartConversation session={source} onOpen={vi.fn()} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await user.click(await screen.findByRole('button', { name: 'End this runtime and reopen' }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({ permissions: 'constrained', remoteControl: false });
  });
});

// The preview is the daemon's answer about what restart would do with this
// record. When it says resume, open or nothing, the dialog follows it and never
// offers to end a runtime.
describe('restart follows the daemon preview', () => {
  const restartPosts = (): unknown[] => vi.mocked(fetch).mock.calls
    .filter(([url]) => String(url).endsWith('/api/recovery/restart')).map(([, options]) => JSON.parse(String(options?.body)));
  const preview = (answer: Partial<RestartPreview>): void => {
    vi.mocked(previewRestart).mockResolvedValueOnce({ sourceSessionId: source.id, accountChanged: false, ...answer });
  };
  const listing = (rows: ReturnType<typeof makeSession>[]) => vi.fn(async (_url: unknown, options?: RequestInit) => {
    if (!options?.body) return new Response(JSON.stringify({ sessions: rows }));
    return new Response(JSON.stringify({ ok: true, sourceEnded: true, sourceSessionId: source.id, operationId: 'operation', laneId }));
  });
  beforeEach(() => {
    useServers.setState({ servers: [{ id: 'fixture', name: 'Fixture', host: '127.0.0.1', port: 8899, isDefault: true }], activeId: 'fixture' });
    useSessions.setState({ serverId: 'fixture', sessions: [source], activeId: source.id, hydrated: true });
  });

  it('offers the saved resume instead of a restart when the daemon says resume', async () => {
    const user = userEvent.setup(); const onResume = vi.fn(); const onOpen = vi.fn();
    vi.stubGlobal('fetch', listing([]));
    preview({ state: 'lost', restartable: false, action: 'resume', command: `sessions resume ${source.id}`, targetSessionId: source.id,
      reason: 'This runtime is no longer running, so restart has nothing to end. Resume reopens the same saved conversation with its recorded profile.' });
    render(<><RestartConversation session={source} onOpen={onOpen} onResume={onResume} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('button', { name: 'Resume conversation' });
    expect(screen.getByRole('heading')).toHaveTextContent('Resume “Restart fixture”');
    await user.click(screen.getByRole('button', { name: 'Resume conversation' }));
    expect(onResume).toHaveBeenCalledExactlyOnceWith(source);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('button', { name: 'End this runtime and reopen' })).toBeNull();
    expect(restartPosts()).toEqual([]);
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('opens the running successor the daemon names instead of starting another runtime', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn(); const onResume = vi.fn();
    vi.stubGlobal('fetch', listing([makeSession({ id: laneId })]));
    preview({ state: 'exited', restartable: false, action: 'open', command: `sessions attach ${laneId}`, targetSessionId: laneId,
      reason: 'aaaaaaaa was already continued, and bbbbbbbb is running now; open it instead of starting a second runner on the same conversation' });
    render(<><RestartConversation session={source} onOpen={onOpen} onResume={onResume} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    expect(await screen.findByRole('status')).toHaveTextContent('open it instead');
    expect(screen.getByRole('heading')).toHaveTextContent('Open running copy of “Restart fixture”');
    expect(screen.queryByRole('combobox', { name: 'Restart permissions' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Open running copy' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(restartPosts()).toEqual([]);
    expect(onResume).not.toHaveBeenCalled();
  });

  it('shows the reason and offers nothing destructive when the daemon has no action', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', listing([]));
    preview({ state: 'moved', restartable: false, action: 'none',
      reason: 'This conversation moved to https://mini.example.test:8787. Open it there; resuming or restarting it here would fork it.' });
    render(<><RestartConversation session={source} onOpen={vi.fn()} onResume={vi.fn()} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    expect(await screen.findByRole('status')).toHaveTextContent('would fork it');
    expect(screen.getByRole('heading')).toHaveTextContent('Restart “Restart fixture”');
    const dialog = screen.getByRole('dialog');
    expect([...dialog.querySelectorAll('button')].map((button) => button.textContent)).toEqual(['Close']);
    expect(restartPosts()).toEqual([]);
  });

  it('retries a recorded restart with exactly its recorded choices', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn();
    vi.stubGlobal('fetch', listing([makeSession({ id: laneId })]));
    const recorded = { permissions: 'constrained' as const, remoteControl: false, runtimeMode: 'terminal' as const };
    preview({ state: 'running', restartable: true, action: 'retry-restart', recordedRestart: recorded,
      command: `sessions restart ${source.id} --confirm ${source.id} --permissions constrained --terminal`,
      reason: 'A restart of this runtime is already recorded. Repeating exactly these choices finishes it or returns its replacement without starting another runtime; different choices are refused.' });
    const fullSource = { ...source, permissions: 'full' as const };
    render(<><RestartConversation session={fullSource} onOpen={onOpen} initialRuntimeMode="rich" /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await user.click(await screen.findByRole('button', { name: 'Retry recorded restart' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(restartPosts()).toEqual([{ sourceSessionId: source.id, confirmSessionId: source.id, ...recorded }]);
  });

  it('shows recorded choices read-only, and omits a runtime the recorded restart left unset', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn();
    vi.stubGlobal('fetch', listing([makeSession({ id: laneId })]));
    preview({ state: 'running', restartable: true, action: 'retry-restart', recordedRestart: { permissions: 'full', remoteControl: false } });
    render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('button', { name: 'Retry recorded restart' });
    // Nothing to choose: the recorded choices are stated, not offered.
    expect(screen.queryByRole('combobox')).toBeNull();
    const choices = screen.getByLabelText('Recorded choices');
    expect(choices).toHaveTextContent('PermissionsFull access (YOLO)');
    expect(choices).toHaveTextContent('RuntimeRich, unchanged');
    expect(choices).toHaveTextContent('Remote ControlOff');
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('run commands without approval');
    // The source is still running, so the retry still ends it.
    expect(dialog).toHaveTextContent(`Retrying ends runtime ${source.id} first; its current turn and running commands are interrupted.`);
    await user.click(screen.getByRole('button', { name: 'Retry recorded restart' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(restartPosts()).toEqual([{ sourceSessionId: source.id, confirmSessionId: source.id, permissions: 'full', remoteControl: false }]);
  });

  it('does not claim a second interruption when the recorded restart already ended the source', async () => {
    const user = userEvent.setup(); const onOpen = vi.fn();
    vi.stubGlobal('fetch', listing([makeSession({ id: laneId })]));
    const recorded = { permissions: 'constrained' as const, remoteControl: true, runtimeMode: 'terminal' as const };
    preview({ state: 'exited', restartable: true, action: 'retry-restart', recordedRestart: recorded,
      reason: 'A restart of this runtime is already recorded. Repeating exactly these choices finishes it or returns its replacement without starting another runtime; different choices are refused.' });
    render(<><RestartConversation session={source} onOpen={onOpen} /><RestartConversationHost /></>);
    await user.click(screen.getByRole('button', { name: 'Restart / change permissions…' }));
    await screen.findByRole('button', { name: 'Retry recorded restart' });
    const dialog = screen.getByRole('dialog');
    expect(screen.getByRole('heading')).toHaveTextContent('Restart “Restart fixture”');
    expect(dialog).toHaveTextContent('The original runtime has already ended, so retrying interrupts nothing.');
    expect(dialog).not.toHaveTextContent('interrupted');
    expect(dialog).not.toHaveTextContent('interrupts its current turn');
    // The daemon's long retry paragraph is summarized, not repeated.
    expect(dialog).not.toHaveTextContent('different choices are refused');
    expect(screen.getByLabelText('Recorded choices')).toHaveTextContent('PermissionsAsk for approvalRuntimeTerminalRemote ControlOn');
    await user.click(screen.getByRole('button', { name: 'Retry recorded restart' }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledExactlyOnceWith(laneId));
    expect(restartPosts()).toEqual([{ sourceSessionId: source.id, confirmSessionId: source.id, ...recorded }]);
  });

  it('does not offer restart on a row paused after a reboot', () => {
    render(<RestartConversation session={{ ...source, working: false, unreachable: true, unreachableReason: 'restart-restore-pending' }} onOpen={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Restart / change permissions…' })).toBeNull();
  });
});

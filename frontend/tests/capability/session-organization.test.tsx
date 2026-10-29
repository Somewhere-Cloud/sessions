import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { archivedLiveIds, usesClaudeRemoteControl } from '../../src/lib/sessionOrganization';
import { groupAgents } from '../../src/lib/projectAgents';
import { SessionArchiveButton } from '../../src/components/SessionArchiveButton';
import { InputBar } from '../../src/components/InputBar';
import { draftStorageKey, readDraft, saveDraft } from '../../src/lib/draftStore';
import { useSessions } from '../../src/store/sessions';
import { useServers } from '../../src/lib/servers';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

const machine = (id: string, sessions: FakeMachine['sessions']): FakeMachine => ({
  id, name: id, host: `${id}.test`, port: 8787, isDefault: id === 'local', sessions
});
const archived = (id: string, extra = {}) => makeSession({
  id, name: id, setAsideAt: 100, lastDataAt: 1, createdAt: 1, working: false,
  lastHumanMessageAt: null, lastAgentMessageAt: null, ...extra
});

describe('reversible manual session organization', () => {
  it('keeps pins, attention, work and new output visible, including their manager path', () => {
    const protections = [
      { lastDataUnknown: true }, { pinned: true }, { working: true }, { launching: true }, { unreachable: true },
      { idleReason: 'needs-input' }, { failureKind: 'other' },
      { pendingApproval: { id: 'approval' } }, { lastDataAt: 101 },
      { lastHumanMessageAt: 101 }, { lastAgentMessageAt: 101 },
      { observations: { approval: { pending: true } } },
      { observations: { last: { provider_record_observed_at: 101 } } }
    ];
    for (const protection of protections) {
      const rows = [archived('manager'), archived('child', { parentSessionId: 'manager', ...protection }), archived('other')];
      expect([...archivedLiveIds(rows)]).toEqual(['other']);
    }
    expect([...archivedLiveIds([archived('finished', { idleReason: 'completed' })])]).toEqual(['finished']);
    expect(archivedLiveIds([makeSession({ id: 'unmarked', idleReason: 'completed' })]).size).toBe(0);
    expect(archivedLiveIds([archived('manager'), makeSession({ id: 'unmarked', parentSessionId: 'manager' })]).size).toBe(0);
    expect([...archivedLiveIds([archived('a', { parentSessionId: 'b', working: true }), archived('b', { parentSessionId: 'a' })])]).toEqual([]);
  });

  it('keeps machine boundaries and archived child/project discovery independent of active root grouping', () => {
    const local = machine('local', [archived('same'), archived('child', { delegationKind: 'agent', parentSessionId: 'same' })]);
    const remote = machine('remote', [archived('same', { working: true })]);
    const snapshots = [local, remote].map((server) => ({ server: { ...server, isDefault: !!server.isDefault }, sessions: server.sessions, error: null }));
    const projects = { local: [{ id: 'p', name: 'Original project', implicit: false, roots: [], live: 2, needs_input: 0, session_ids: ['same', 'child'] }] };
    const hidden = groupAgents(snapshots, projects, false, () => true, 'archived');
    expect(hidden.flatMap((group) => group.rows.map((row) => [group.name, row.server.id, row.session.id])))
      .toEqual([['Original project', 'local', 'same'], ['Original project', 'local', 'child']]);
    expect(groupAgents(snapshots, {}, false, () => true).flatMap((group) => group.rows.map((row) => row.server.id)))
      .toEqual(['remote']);
    const offline = [{ ...snapshots[0], error: 'not connected' }];
    expect(groupAgents(offline, {}, false, () => true, 'archived')).toEqual([]);
    expect(groupAgents(offline, {}, false, () => true).flatMap((group) => group.rows.map((row) => row.session.id)))
      .toEqual(['same']);
  });

  it('finds archived work after remount and restores without lifecycle calls or draft loss', async () => {
    window.localStorage.setItem('sessions:projects-machine-scope', 'local');
    const session = makeSession({ id: 'quiet', name: 'Parser review', idleReason: 'completed', working: false });
    const host = machine('local', [session]);
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    const user = userEvent.setup();
    const close = vi.fn();
    const view = render(<Workbench>
      <SessionArchiveButton session={session} onArchived={close} />
      <InputBar send={async () => {}} submitMessage={async () => {}} connected sessionId={session.id} draftMachineId="local" />
    </Workbench>);
    const draft = await screen.findByPlaceholderText(/Message Claude/);
    fireEvent.change(draft, { target: { value: 'Still editing this draft' } });
    await user.click(screen.getByRole('button', { name: 'Set aside' }));
    await waitFor(() => expect(close).toHaveBeenCalledWith('quiet'));
    expect(readDraft(draftStorageKey('local', 'quiet')).text).toBe('Still editing this draft');
    view.unmount();
    const open = vi.fn();
    render(<Workbench onOpen={open} />);
    const section = await screen.findByRole('region', { name: 'Archived live sessions' });
    expect(screen.queryByText('Parser review', { selector: '.session-nav-title' })).not.toBeInTheDocument();
    await user.type(screen.getByRole('textbox', { name: 'Filter sessions' }), 'Parser');
    expect(within(section).getByRole('button', { name: /Settled/ })).toHaveAttribute('aria-expanded', 'true');
    await user.click(await within(section).findByText('Parser review', { selector: '.session-nav-title' }));
    expect(open).toHaveBeenCalledWith('quiet');
    expect(host.sessions[0].setAsideAt).toBeTruthy();
    render(<SessionArchiveButton session={host.sessions[0]} />);
    await user.click(screen.getByRole('button', { name: 'Restore to active list' }));
    await waitFor(() => expect(host.sessions[0].setAsideAt).toBeNull());
    expect(screen.getByText('Parser review', { selector: '.session-nav-title' })).toBeInTheDocument();
    expect(readDraft(draftStorageKey('local', 'quiet')).text).toBe('Still editing this draft');
    expect(daemon.ended).toEqual([]);
    expect(daemon.archived).toEqual([]);
    expect(daemon.requests.filter((r) => r.method !== 'GET').map((r) => r.path))
      .toEqual(['/api/sessions/quiet/set-aside', '/api/sessions/quiet/set-aside']);
  });

  it('keeps the open composer and machine-scoped drafts when a daemon archive mark arrives', async () => {
    window.localStorage.setItem('sessions:projects-machine-scope', 'local');
    const host = machine('local', [archived('automatic', { setAsideAt: null })]);
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    saveDraft(draftStorageKey('other-client-machine', 'automatic'), 'Another stored draft');
    const view = render(<Workbench>
      <InputBar send={async () => {}} submitMessage={async () => {}} connected sessionId="automatic" draftMachineId="local" />
    </Workbench>);
    const composer = await screen.findByPlaceholderText(/Message Claude/);
    fireEvent.change(composer, { target: { value: 'Keep editing while organized' } });
    host.sessions[0] = { ...host.sessions[0], setAsideAt: 100 };
    await act(async () => { await useSessions.getState().refresh(); });
    await screen.findByRole('region', { name: 'Archived live sessions' });
    expect(composer).toBeInTheDocument();
    expect(composer).toHaveValue('Keep editing while organized');
    expect(readDraft(draftStorageKey('other-client-machine', 'automatic')).text).toBe('Another stored draft');
    view.unmount();
    expect(readDraft(draftStorageKey('local', 'automatic')).text).toBe('Keep editing while organized');
    expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
    expect(daemon.ended).toEqual([]); expect(daemon.archived).toEqual([]);
  });

  it('brings archived work back on provider receipt alone without a legacy output timestamp change', async () => {
    window.localStorage.setItem('sessions:projects-machine-scope', 'local');
    const host = machine('local', [archived('provider-only')]);
    installFakeDaemon([host]); useFakeMachines([host]);
    render(<Workbench />);
    await screen.findByRole('region', { name: 'Archived live sessions' });
    host.sessions[0] = { ...host.sessions[0], observations: { version: 1,
      capabilities: { runtime: 'claude-structured', turn_boundaries: 'provider-stream', tool_records: 'provider-stream', terminal_output: false },
      runner: { state: 'reachable', since: null, exit_code: null }, working: null, turn: null, tools: null, observers: [],
      last: { provider_record_observed_at: 101, provider_record_event_at: null, terminal_output_at: null,
        transcript_record_event_at: null, input_dispatched_at: null, provider_user_record_at: null } } };
    await act(async () => { await useSessions.getState().refresh(); });
    expect(await screen.findByText('provider-only', { selector: '.session-nav-title' })).toBeInTheDocument();
    expect(host.sessions[0].setAsideAt).toBe(100);
  });

  it('brings a marked row back when an approval alone changes in the next inventory frame', async () => {
    window.localStorage.setItem('sessions:projects-machine-scope', 'local');
    const host = machine('local', [archived('approval-only')]);
    installFakeDaemon([host]);
    useFakeMachines([host]);
    render(<Workbench />);
    await screen.findByRole('region', { name: 'Archived live sessions' });
    expect(screen.queryByText('approval-only', { selector: '.session-nav-title' })).not.toBeInTheDocument();
    host.sessions[0] = { ...host.sessions[0], pendingApproval: { id: 'new-approval', kind: 'command', summary: 'Approve command' } };
    await act(async () => { await useSessions.getState().refresh(); });
    expect(await screen.findByText('approval-only', { selector: '.session-nav-title' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Archived live sessions' })).not.toBeInTheDocument();
    expect(host.sessions[0].setAsideAt).toBe(100);
  });

  it('does not apply an old acknowledgement or close a same-ID view on another machine', async () => {
    const local = machine('local', [makeSession({ id: 'same' })]);
    const remote = machine('remote', [makeSession({ id: 'same' })]);
    const daemon = installFakeDaemon([local, remote]);
    useFakeMachines([local, remote]);
    await useSessions.getState().refresh();
    const fetch = globalThis.fetch;
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    globalThis.fetch = async (input, init) => {
      const response = await fetch(input, init);
      if (String(input).endsWith('/set-aside')) await gate;
      return response;
    };
    const close = vi.fn();
    const view = render(<SessionArchiveButton session={{ ...local.sessions[0] }} onArchived={close} />);
    try {
      fireEvent.click(screen.getByRole('button', { name: 'Set aside' }));
      await waitFor(() => expect(daemon.requests.some((r) => r.path.endsWith('/set-aside'))).toBe(true));
      act(() => { useServers.getState().setActive('remote'); });
      await act(async () => { await useSessions.getState().refresh('remote'); });
      expect(useSessions.getState().serverId).toBe('remote');
      view.rerender(<SessionArchiveButton session={remote.sessions[0]} onArchived={close} />);
      await act(async () => { release(); await gate; });
      await waitFor(() => expect(screen.getByRole('button', { name: 'Set aside' })).toBeEnabled());
      expect(useSessions.getState().sessions[0].setAsideAt).toBeUndefined();
      expect(remote.sessions[0].setAsideAt).toBeUndefined();
      expect(close).not.toHaveBeenCalled();
    } finally { release(); globalThis.fetch = fetch; }
  });

  it('reports unsupported organization without falling back to ending or legacy archival', async () => {
    const host = machine('local', [makeSession({ id: 'old' })]);
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    await useSessions.getState().refresh();
    const fetch = globalThis.fetch;
    globalThis.fetch = async (input, init) => String(input).endsWith('/set-aside')
      ? new Response(JSON.stringify({ error: 'organization unavailable' }), { status: 501 }) : fetch(input, init);
    const close = vi.fn();
    try {
      render(<SessionArchiveButton session={host.sessions[0]} onArchived={close} />);
      fireEvent.click(screen.getByRole('button', { name: 'Set aside' }));
      expect(await screen.findByRole('alert')).toHaveTextContent(/update|unavailable|support/i);
      expect(close).not.toHaveBeenCalled();
      expect(daemon.ended).toEqual([]);
      expect(daemon.archived).toEqual([]);
    } finally { globalThis.fetch = fetch; }
  });
});

it('separates only recorded Claude Remote Control launches, never guessed provider defaults', () => {
  expect(usesClaudeRemoteControl(makeSession({ id: 'remote-fixture', args: ['--remote-control'] }))).toBe(true);
  expect(usesClaudeRemoteControl(makeSession({ id: 'remote-fixture', args: ['--settings', '{"remoteControlAtStartup":true}'] }))).toBe(true);
  expect(usesClaudeRemoteControl(makeSession({ id: 'remote-fixture', args: ['--settings', '{"remoteControlAtStartup":false}'] }))).toBe(false);
  expect(usesClaudeRemoteControl(makeSession({ id: 'remote-fixture', args: ['--settings', '/path/to/settings.json'] }))).toBe(false);
  expect(usesClaudeRemoteControl(makeSession({ id: 'remote-fixture', tool: 'codex', args: ['--remote-control'] }))).toBe(false);
});

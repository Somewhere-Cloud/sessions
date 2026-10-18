import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { submitMessage } from '../../src/api/sessionsd/operations';
import { eventsToMessages } from '../../src/lib/claudeEvents';
import { InputBar } from '../../src/components/InputBar';
import { RemoteView } from '../../src/components/RemoteView';
import { useFakeMachines } from './fake-daemon';
import type { ClaudeSessionEvent } from '../../src/types';

const workingSidebar = { parserName: 'Claude', parserIcon: '', isWorking: true, timer: '', tokens: '', context: '', finalElapsed: '', currentTask: '', checklist: [] };
const savedMessage: ClaudeSessionEvent = { type: 'queue-operation', source: 'sessions-next-turn', operation: 'enqueue', content: 'Saved follow-up', uuid: 'sessions-message:saved', timestamp: '2026-10-18T08:11:00Z' };

function renderSavedQueue(terminalAvailable: boolean): void {
  render(<RemoteView sessionId="paused-chat" events={[savedMessage]} historyPending={false} connected sendConfirmed={async () => {}} submitMessage={async () => {}}
    hasEarlierClaudeEvents={false} loadingEarlierClaudeEvents={false} onLoadEarlierClaudeEvents={() => {}}
    sidebar={workingSidebar} onOpenTerminal={() => {}} terminalAvailable={terminalAvailable} provider="claude-code" />);
}

describe('Claude saved-message continuation', () => {
  it.each([
    ['refused', 409, { error: 'Claude did not continue saved messages: Claude turn is active. Nothing was resent' }],
    ['accepted', 200, { id: 'paused-chat' }]
  ] as const)('asks the runner once even while the view says working: %s', async (_name, status, body) => {
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    const user = userEvent.setup();
    const posts: string[] = [];
    globalThis.fetch = vi.fn(async (url: unknown, init?: RequestInit) => {
      if (init?.method === 'POST') posts.push(String(url));
      return Response.json(body, { status });
    });
    renderSavedQueue(false);
    // The view's working flag is inferred; a paused queue can sit behind it,
    // so continuing stays offered and the runner decides.
    expect(screen.getByText(/runner decides/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Continue saved messages' }));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toMatch(/\/api\/sessions\/paused-chat\/retry$/);
    if (status === 409) expect(await screen.findByRole('alert')).toHaveTextContent('Claude turn is active. Nothing was resent');
    else expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    // Asking is the only request: no message is resent and nothing is interrupted.
    expect(posts.some((url) => /\/(submit|input)$/.test(url))).toBe(false);
  });

  it('is not offered for a terminal session, which has no saved-message queue', () => {
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    renderSavedQueue(true);
    expect(screen.queryByRole('button', { name: 'Continue saved messages' })).not.toBeInTheDocument();
  });

  it('shows a process error after Claude answered as what it is, not a failed turn', () => {
    const messages = eventsToMessages([{ type: 'system', subtype: 'provider_exit_after_result', origin: 'sessions', timestamp: '2026-10-18T08:12:00Z',
      detail: 'Claude answered this turn, then its process reported an error: exit status 1. The answer is kept and will not be run again.' } as ClaudeSessionEvent]);
    expect(messages).toHaveLength(1);
    expect(messages[0]?.quietStatus).toContain('will not be run again');
    expect(messages[0]?.errorResponse).toBeUndefined();
  });
});

describe('Claude next-turn acceptance', () => {
  it.each([false, true])('preserves a queued receipt after broken HTTP: %s', async (broken) => {
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    let operation = '';
    const fetch = vi.fn(async (_url: unknown, init?: RequestInit) => {
      if (init?.method === 'POST') operation = JSON.parse(String(init.body)).operation_id;
      if (init?.method === 'POST' && broken) return new Response('{', { status: 500 });
      return Response.json({ operation_id: operation, session_id: 'fixture', status: 'accepted', acceptance: 'queue', delivered: true, retry: false });
    });
    globalThis.fetch = fetch;
    await expect(submitMessage('fixture', 'follow-up', 'local')).resolves.toEqual({ queued: true });
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(fetch).toHaveBeenCalledTimes(broken ? 2 : 1);
  });

  it('keeps a draft when an older busy runner refuses it', async () => {
    const user = userEvent.setup();
    const submit = vi.fn().mockRejectedValue(new Error('This runner cannot accept mid-turn messages'));
    render(<InputBar sessionId="old-runner" draftMachineId="local" provider="claude-code" richSession providerWorking connected
      send={async () => {}} submitMessage={submit} />);
    await user.type(screen.getByRole('textbox'), 'Do not lose this');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(submit).toHaveBeenCalledOnce();
    expect(screen.getByRole('textbox')).toHaveValue('Do not lose this');
    expect(screen.getByRole('alert')).toHaveTextContent('Your draft is still here');
  });

  it('keeps identical queued messages distinct until their own dispatch', () => {
    const queued = (id: string): ClaudeSessionEvent => ({ type: 'queue-operation', operation: 'enqueue', content: 'Continue', uuid: `sessions-message:${id}`, timestamp: '2026-10-06T10:00:00Z' });
    const user: ClaudeSessionEvent = { type: 'user', uuid: 'sessions-message:first', timestamp: '2026-10-06T10:00:01Z', message: { role: 'user', content: 'Continue' } };
    const messages = eventsToMessages([queued('first'), queued('second'), user]);
    expect(messages.filter((message) => message.pendingQueue)).toHaveLength(1);
    expect(messages.find((message) => message.pendingQueue)?.blockId).toBe('sessions-message:second');
    expect(messages.filter((message) => message.role === 'user' && !message.pendingQueue)).toHaveLength(1);
  });

  it('never labels a previously claimed message as queued or confirmed after restart', () => {
    const messages = eventsToMessages([
      { type: 'queue-operation', source: 'sessions-next-turn', operation: 'enqueue', content: 'Follow-up', uuid: 'sessions-message:uncertain' },
      { type: 'queue-operation', source: 'sessions-next-turn', operation: 'dispatch-unknown', operation_id: 'sessions-message:uncertain', content: 'Follow-up', uuid: 'sessions-message:uncertain:unknown' }
    ]);
    expect(messages).toHaveLength(1);
    expect(messages[0]?.pendingQueue).toBeUndefined();
    expect(messages[0]?.status).toBe('accepted');
    expect(messages[0]?.errorResponse).toContain('unknown');
  });

  it('reconciles an uncertain dispatch with a held local queue without showing it as pending', () => {
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    window.localStorage.setItem('sessions:dispatch:local:uncertain-chat', JSON.stringify([
      { id: 'local-send', role: 'user', content: 'Uncertain follow-up', status: 'queued', queued: true, createdAt: 1 }
    ]));
    render(<RemoteView sessionId="uncertain-chat" events={[
      { type: 'queue-operation', source: 'sessions-next-turn', operation: 'dispatch-unknown', operation_id: 'sessions-message:uncertain', uuid: 'sessions-message:uncertain:unknown', content: 'Uncertain follow-up' }
    ]} historyPending={false} connected sendConfirmed={async () => {}} submitMessage={async () => {}}
      hasEarlierClaudeEvents={false} loadingEarlierClaudeEvents={false} onLoadEarlierClaudeEvents={() => {}}
      sidebar={{ parserName: 'Claude', parserIcon: '', isWorking: false, timer: '', tokens: '', context: '', finalElapsed: '', currentTask: '', checklist: [] }}
      onOpenTerminal={() => {}} terminalAvailable={false} provider="claude-code" />);
    expect(screen.getAllByText('Uncertain follow-up')).toHaveLength(1);
    expect(screen.queryByText(/saved for Claude's next turn/)).not.toBeInTheDocument();
    expect(screen.getByRole('textbox')).toHaveValue('');
  });
});

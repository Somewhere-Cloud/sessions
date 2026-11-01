import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { eventsToMessages } from '../../src/lib/claudeEvents';
import { RemoteView } from '../../src/components/RemoteView';
import { useFakeMachines } from './fake-daemon';
import type { ClaudeSessionEvent } from '../../src/types';

const sidebar = { parserName: 'Claude', parserIcon: '', isWorking: true, timer: '', tokens: '', context: '', finalElapsed: '', currentTask: '', checklist: [] };
const saved = (id: string, content: string): ClaudeSessionEvent => ({ type: 'queue-operation', source: 'sessions-next-turn', operation: 'enqueue', content, uuid: `sessions-message:${id}`, timestamp: '2026-11-01T10:00:00Z' });
const change = (id: string, operation: string, extra: Record<string, unknown> = {}): ClaudeSessionEvent =>
  ({ type: 'queue-operation', source: 'sessions-next-turn', operation, operation_id: `sessions-message:${id}`, uuid: `sessions-message:${id}:${operation}`, ...extra });

function renderQueue(events: ClaudeSessionEvent[], queueControl = true): void {
  render(<RemoteView sessionId="busy-chat" queueControl={queueControl} events={events} historyPending={false} connected sendConfirmed={async () => {}} submitMessage={async () => {}}
    hasEarlierClaudeEvents={false} loadingEarlierClaudeEvents={false} onLoadEarlierClaudeEvents={() => {}}
    sidebar={sidebar} onOpenTerminal={() => {}} terminalAvailable={false} provider="claude-code" />);
}

function captureQueuePosts(status: number, body: unknown): Array<{ url: string; body: Record<string, unknown> }> {
  useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
  const posts: Array<{ url: string; body: Record<string, unknown> }> = [];
  globalThis.fetch = vi.fn(async (url: unknown, init?: RequestInit) => {
    if (init?.method === 'POST') posts.push({ url: String(url), body: JSON.parse(String(init.body)) });
    return Response.json(body, { status });
  });
  return posts;
}

describe('saved Claude message controls', () => {
  it('edits, sends next and removes a saved message through the queue contract only', async () => {
    const posts = captureQueuePosts(200, { session_id: 'busy-chat', paused: false, items: [] });
    const user = userEvent.setup();
    renderQueue([saved('first', 'First follow-up'), saved('second', 'Second follow-up')]);
    await user.click(screen.getByText('View or change saved messages'));
    const second = screen.getByText('Second follow-up').closest('.remote-queued-item') as HTMLElement;
    const first = screen.getByText('First follow-up').closest('.remote-queued-item') as HTMLElement;
    // The first message is already next; only later ones offer it.
    expect(within(first).queryByRole('button', { name: 'Send next' })).not.toBeInTheDocument();
    expect(within(second).getByRole('button', { name: 'Send next' })).toHaveAttribute('title', expect.stringContaining('cannot take a message mid-turn'));
    await user.click(within(second).getByRole('button', { name: 'Send next' }));
    await user.click(within(first).getByRole('button', { name: 'Edit' }));
    const editor = screen.getByRole('textbox', { name: 'Edit saved message' });
    await user.clear(editor);
    await user.type(editor, 'Rewritten follow-up');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(posts).toHaveLength(2));
    const remaining = screen.getByText('Second follow-up').closest('.remote-queued-item') as HTMLElement;
    await user.click(within(remaining).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(posts).toHaveLength(3));
    expect(posts.every((post) => /\/api\/sessions\/busy-chat\/queue$/.test(post.url))).toBe(true);
    expect(posts.map((post) => post.body)).toEqual([
      { action: 'move', operation_id: 'second', to: 1 },
      { action: 'edit', operation_id: 'first', text: 'Rewritten follow-up' },
      { action: 'cancel', operation_id: 'second' }
    ]);
  });

  it('shows the runner refusal for a message already dispatched and keeps nothing pending locally', async () => {
    captureQueuePosts(409, { code: 'dispatched', error: 'This message was already dispatched to Claude, so it can no longer be edited, moved or cancelled. Nothing was changed; it will not be sent again.', items: [] });
    const user = userEvent.setup();
    renderQueue([saved('raced', 'Raced follow-up')]);
    await user.click(screen.getByText('View or change saved messages'));
    await user.click(screen.getByRole('button', { name: 'Remove' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('already dispatched');
  });

  it('offers no changes when the runner cannot make them', async () => {
    const user = userEvent.setup();
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    renderQueue([saved('old', 'Old runner follow-up')], false);
    await user.click(screen.getByText('View saved messages'));
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
  });

  it('projects the runner history of edits, reorders and cancellations', () => {
    const messages = eventsToMessages([
      saved('a', 'A'), saved('b', 'B'), saved('c', 'C'),
      change('b', 'edit', { content: 'B edited', uuid: 'sessions-message:b:edit:1' }),
      change('c', 'remove', { uuid: 'sessions-message:c:cancelled' }),
      change('b', 'reorder', { order: ['sessions-message:b', 'sessions-message:a'] })
    ]);
    expect(messages.filter((message) => message.pendingQueue && !message.withdrawn).map((message) => message.content)).toEqual(['B edited', 'A']);
  });

  it('does not leave the composer copy of an edited or removed message behind as a phantom', async () => {
    useFakeMachines([{ id: 'local', name: 'Fixture', host: 'localhost', port: 8787, isDefault: true, sessions: [] }]);
    window.localStorage.setItem('sessions:dispatch:local:busy-chat', JSON.stringify([
      { id: 'local-a', role: 'user', content: 'Original A', status: 'queued', queued: true, createdAt: 1, confirmBaseline: 0 },
      { id: 'local-b', role: 'user', content: 'Original B', status: 'queued', queued: true, createdAt: 2, confirmBaseline: 0 }
    ]));
    const user = userEvent.setup();
    renderQueue([saved('a', 'Original A'), saved('b', 'Original B'),
      change('a', 'edit', { content: 'Edited A', uuid: 'sessions-message:a:edit:1' }),
      change('b', 'remove', { uuid: 'sessions-message:b:cancelled' })]);
    expect(screen.getByText(/1 saved for Claude's next turn/)).toBeInTheDocument();
    await user.click(screen.getByText('View or change saved messages'));
    expect(screen.queryByText('Original A')).not.toBeInTheDocument();
    expect(screen.queryByText('Original B')).not.toBeInTheDocument();
    expect(screen.getByText('Edited A')).toBeInTheDocument();
  });
});

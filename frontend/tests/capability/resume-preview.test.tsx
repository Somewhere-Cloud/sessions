import { describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ResumeDialog } from '../../src/components/ResumeDialog';
import { ResumePreview } from '../../src/components/ResumePreview';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';
import type { ResumableSession } from '../../src/api/sessionsd';

const selected: ResumableSession = {
  sessionId: 'provider-identity', historyId: 'saved-history', title: 'Release plan',
  tool: 'claude', cwd: '/work/release', modifiedAt: Date.now(), sizeBytes: 20_000,
  firstUserMessage: 'Prepare the release'
};

function machine(): FakeMachine {
  return {
    id: 'local', name: 'This Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [],
    history: [{ id: 'saved-history', name: 'Release plan', tool: 'claude', cwd: '/work/release',
      machine: 'This Mac', created_at: Date.now(), last_activity_at: Date.now(), message_count: 2, conversation_available: true }],
    transcripts: { 'saved-history': [
      { id: 'user', index: 0, role: 'user', text: 'Prepare the release', timestamp: null },
      { id: 'assistant', index: 1, role: 'assistant', text: 'The tests passed; awaiting review.', timestamp: null }
    ] },
    resumable: [selected]
  };
}

describe('read before continuing a conversation', () => {
  it('ends a hung read with an explicit read-only retry, not an endless spinner or a resumed agent', async () => {
    const host = machine();
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    const fetch = globalThis.fetch;
    globalThis.fetch = (_input, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(options.signal?.reason), { once: true });
    });
    vi.useFakeTimers();
    try {
      render(<ResumePreview selected={selected} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
      expect(screen.getByText(/Reading this preview took too long/)).toBeInTheDocument();
      expect(screen.queryByText('Reading recent messages…')).not.toBeInTheDocument();
    } finally { vi.useRealTimers(); }
    globalThis.fetch = fetch;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry preview' }));
    expect(await screen.findByText('The tests passed; awaiting review.')).toBeInTheDocument();
    expect(daemon.created).toEqual([]);
    expect(daemon.adopted).toEqual([]);
  });

  it('reads the saved history identity through the bounded preview route without launching anything', async () => {
    const host = machine();
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    render(<ResumePreview selected={selected} />);
    expect(await screen.findByText('The tests passed; awaiting review.')).toBeInTheDocument();
    expect(daemon.requests.some((request) => request.path === '/api/history/saved-history/preview')).toBe(true);
    expect(daemon.adopted).toEqual([]);
    expect(daemon.created).toEqual([]);
  });

  it('replaces the old preview with an instructional failure rather than retaining the wrong conversation', async () => {
    const host = machine();
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    const { rerender } = render(<ResumePreview selected={selected} />);
    await screen.findByText('The tests passed; awaiting review.');
    rerender(<ResumePreview selected={{ ...selected, sessionId: 'other-provider', historyId: 'missing' }} />);
    await waitFor(() => expect(screen.queryByText('The tests passed; awaiting review.')).not.toBeInTheDocument());
    expect(await screen.findByText(/Preview unavailable:/)).toBeInTheDocument();
    expect(daemon.requests.filter((request) => request.path.includes('/history/')).every((request) => request.path.endsWith('/preview'))).toBe(true);
    expect(daemon.adopted).toEqual([]);
  });

  it('starts project-first, limits the initial list and lets the person reveal more', async () => {
    const host = machine();
    host.resumable = Array.from({ length: 35 }, (_, index) => ({ ...selected, sessionId: `provider-${index}`, title: `Conversation ${index}` }));
    const daemon = installFakeDaemon([host]);
    useFakeMachines([host]);
    render(<ResumeDialog onClose={() => {}} onStartNew={() => {}} onResumed={() => {}} />);
    expect(await screen.findByRole('tab', { name: 'By project folder' })).toHaveAttribute('aria-selected', 'true');
    await screen.findByRole('button', { name: /Conversation 19/ });
    expect(screen.queryByRole('button', { name: /Conversation 20/ })).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Show more conversations' }));
    expect(await screen.findByRole('button', { name: /Conversation 34/ })).toBeInTheDocument();
    expect(daemon.adopted).toEqual([]);
  });
});

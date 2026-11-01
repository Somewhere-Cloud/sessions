// CAPABILITY: a person sees each session's pull request at a glance, and can
// see and turn off the automation that tidies up after a merge.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { CleanupAutomationCard } from '../../src/components/CleanupAutomationCard';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

describe('capability: merged work', () => {
  it('shows each linked pull request compactly on its session row', async () => {
    const machine: FakeMachine = {
      id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true,
      sessions: [
        makeSession({ id: 'open-pr', name: 'Parser work', pullRequest: { url: 'https://github.com/acme/app/pull/42', number: 42, state: 'open', checks: 'failing', allMerged: false } }),
        makeSession({ id: 'merged-pr', name: 'Docs work', pullRequest: { url: 'https://github.com/acme/app/pull/7', number: 7, state: 'merged', checks: 'passing', allMerged: true } }),
        makeSession({ id: 'closed-pr', name: 'Spike', pullRequest: { url: 'https://github.com/acme/app/pull/9', number: 9, state: 'closed', checks: 'none', allMerged: false } }),
        makeSession({ id: 'no-pr', name: 'Plain' })
      ]
    };
    installFakeDaemon([machine]);
    useFakeMachines([machine]);
    render(<Workbench />);
    const open = await screen.findByLabelText('Pull request #42, open, checks failing');
    expect(open).toHaveClass('is-open');
    expect(open.querySelector('.session-nav-pr-checks.is-failing')).not.toBeNull();
    expect(screen.getByLabelText('Pull request #7, merged')).toHaveClass('is-merged');
    expect(screen.getByLabelText('Pull request #9, closed')).toHaveClass('is-closed');
    expect(document.querySelectorAll('.session-nav-pr')).toHaveLength(3);
  });

  it('is on by default, explains what it keeps, and turns off through the same API as the CLI', async () => {
    const calls: string[] = [];
    let watch = true;
    vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url.endsWith('/api/pull-request-watch')) {
        if (init?.method === 'PUT') watch = JSON.parse(String(init.body)).enabled;
        return new Response(JSON.stringify({ enabled: watch, intervalMs: 300000, settleQuietMs: 120000, lastSettled: [] }));
      }
      if (url.endsWith('/api/worktrees/auto')) {
        return new Response(JSON.stringify({ enabled: true, intervalMs: 600000, graceMs: 3600000, plan: [
          { session: 'a', worktree_path: '/w/a', branch: 'sessions/a', action: 'would-remove' },
          { session: 'b', worktree_path: '/w/b', branch: 'sessions/b', action: 'skipped', reason: 'worktree is DIRTY' }
        ] }));
      }
      throw new Error(`unexpected request ${url}`);
    });
    render(<CleanupAutomationCard hostName="Fixture Mac" />);
    const watchToggle = await screen.findByRole('checkbox', { name: 'Watch linked pull requests' });
    const cleanToggle = screen.getByRole('checkbox', { name: 'Remove merged worktrees' });
    await waitFor(() => expect(watchToggle).toBeChecked());
    await waitFor(() => expect(cleanToggle).toBeChecked());
    expect(await screen.findByText(/1 ready to remove, 1 kept/)).toBeInTheDocument();
    fireEvent.click(watchToggle);
    await waitFor(() => expect(watchToggle).not.toBeChecked());
    expect(calls.filter((call) => call.startsWith('PUT'))).toEqual(['PUT http://localhost:8787/api/pull-request-watch']);
  });
});

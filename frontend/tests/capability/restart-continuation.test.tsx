// CAPABILITY: a session a reboot, update, or crash interrupted carries on by
// itself, and says so calmly. The note is from Restart continuation, never
// from the person, and the setting is the GUI form of
// `sessions continuation on|off`.
import { describe, expect, it } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RestartContinuationCard } from '../../src/components/RestartContinuationCard';
import { RestartContinuationNotice, restartContinuationNotice } from '../../src/components/SessionNotices';
import { SessionView } from '../../src/components/SessionView';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import type { SessionInfo } from '../../src/types';

const SENT_AT = new Date(2026, 10, 4, 9, 41).getTime();

function machineWith(sessions: SessionInfo[]): FakeMachine {
  return { id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions };
}

function continued(overrides: Partial<SessionInfo> & { id: string }): SessionInfo {
  return makeSession({
    tool: 'claude-code', kind: 'claude-structured',
    restartContinuation: { reason: 'reboot', kind: 'paused', state: 'sent', sentAt: SENT_AT, updatedAt: SENT_AT },
    ...overrides
  });
}

describe('capability: a session continued after a restart reads calmly', () => {
  it('names the reason and the time, and only explains when something went wrong', () => {
    const sent = restartContinuationNotice({ reason: 'reboot', state: 'sent', sentAt: SENT_AT });
    expect(sent?.title).toMatch(/^Continued after restart \(reboot\) at /);
    expect(restartContinuationNotice({ reason: 'unknown', state: 'pending' })?.title).toBe('Continuing after restart (reason unknown)…');
    expect(restartContinuationNotice({ reason: 'crash', state: 'failed', detail: 'did not come back' })?.detail).toBe('did not come back');
    expect(restartContinuationNotice({ reason: 'update', state: 'skipped' })).toBeNull();
    expect(restartContinuationNotice(null)).toBeNull();
  });

  it('shows the note in the session, and where a reopened session came from', () => {
    render(<RestartContinuationNotice session={continued({ id: 'reopened', restartContinuation: {
      reason: 'crash', kind: 'lost', state: 'sent', sentAt: SENT_AT, sourceId: 'abcdef0123456789'
    } })} />);
    const notice = screen.getByRole('status', { name: 'Restart continuation' });
    expect(within(notice).getByText(/^Continued after restart \(crash\) at /)).toBeInTheDocument();
    expect(within(notice).getByText(/Reopened in place of abcdef01/)).toBeInTheDocument();
    expect(notice.className).not.toMatch(/danger|error/);
  });

  it('appears above the conversation in the session view', async () => {
    const session = continued({ id: 'rebooted' });
    const machine = machineWith([session]);
    installFakeDaemon([machine]);
    useFakeMachines([machine]);
    render(<SessionView sessionId={session.id} isActive />);
    expect(await screen.findByRole('status', { name: 'Restart continuation' })).toHaveTextContent(/Continued after restart \(reboot\)/);
  });
});

describe('capability: restart continuation is a Settings toggle with a CLI twin', () => {
  it('is on by default and turns off through the same route as `sessions continuation off`', async () => {
    const machine = machineWith([continued({ id: 'rebooted' })]);
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<RestartContinuationCard clientOnly={false} hostName="Fixture Mac" />);
    const toggle = await screen.findByRole('checkbox', { name: /Continue interrupted sessions/ });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).toBeChecked();
    expect(screen.getByText(/1 session was continued after a restart/)).toBeInTheDocument();
    await user.click(toggle);
    await waitFor(() => expect(daemon.requests.some((request) =>
      request.method === 'PUT' && request.path === '/api/restart-continuation'
      && (request.body as { enabled?: boolean }).enabled === false)).toBe(true));
    await waitFor(() => expect(toggle).not.toBeChecked());
  });
});

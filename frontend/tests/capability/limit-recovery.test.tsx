// CAPABILITY: a session stopped by its plan's usage limit is not an
// emergency. Sessions says when it resumes, lets the person cancel that, and
// a session can be set aside until a time (or until the limit resets) and
// comes back on its own. Each choice is the GUI form of a CLI command:
// `sessions limits off --lane ID` and `sessions aside ID --until WHEN|reset`.
import { describe, expect, it } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ProviderFaultCard } from '../../src/components/ProviderFaultCard';
import { SessionView } from '../../src/components/SessionView';
import { classifySession } from '../../src/lib/sessionStatus';
import { snoozePresets } from '../../src/lib/returnTime';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import type { SessionInfo } from '../../src/types';

const RESUME_AT = Date.now() + 2 * 3_600_000;

function limited(overrides: Partial<SessionInfo> & { id: string }): SessionInfo {
  return makeSession({
    failureKind: 'rate-limited', failureDetail: 'Claude rate limit reached', failureProvider: 'claude',
    failureAt: Date.now() - 60_000,
    limitRecovery: { state: 'scheduled', resumeAt: RESUME_AT, resetAt: RESUME_AT - 60_000, source: 'provider-message', attempt: 1 },
    ...overrides
  });
}

function machineWith(sessions: SessionInfo[]): FakeMachine {
  return { id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions };
}

describe('capability: a usage limit Sessions will resume reads calmly', () => {
  it('classifies as resuming, names the time, and asks for nothing', () => {
    const status = classifySession(limited({ id: 'calm' }));
    expect(status.state).toBe('resuming');
    expect(status.label).toMatch(/^Resumes /);
    expect(status.wantsAttention).toBe(false);
    // Without a plan the same fault is still provider trouble.
    const unplanned = classifySession(limited({ id: 'plain', limitRecovery: null }));
    expect(unplanned.state).toBe('provider-down');
  });

  it('shows the resume time instead of provider trouble, and can cancel it', async () => {
    const session = limited({ id: 'waiting' });
    const machine = machineWith([session]);
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(
      <ProviderFaultCard
        sessionId={session.id} failureKind="rate-limited" detail={session.failureDetail}
        limitRecovery={session.limitRecovery} rich onOpenTerminal={() => {}}
      />
    );
    const card = screen.getByRole('group', { name: 'Usage limit' });
    expect(within(card).getByText(/^Resumes at /)).toBeInTheDocument();
    expect(within(card).getByText(/Continue where you left off\./)).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Provider trouble' })).not.toBeInTheDocument();

    await user.click(within(card).getByRole('button', { name: 'Don’t resume' }));
    await waitFor(() => expect(daemon.requests.some((request) =>
      request.method === 'PUT' && request.path === '/api/limit-recovery/lanes/waiting'
      && (request.body as { override?: string }).override === 'off')).toBe(true));
    expect(await within(card).findByText(/will not resume this session on its own/)).toBeInTheDocument();
    expect(daemon.delivered.waiting ?? []).toEqual([]);
  });

  it('shows ordinary provider trouble while the outage retries still run', () => {
    render(
      <ProviderFaultCard
        sessionId="retrying" failureKind="rate-limited" detail="Claude rate limit reached"
        retry={{ attempt: 2, max: 5, nextAt: Date.now() + 30_000, kind: 'rate-limited' }}
        limitRecovery={{ state: 'scheduled', resumeAt: RESUME_AT }} rich onOpenTerminal={() => {}}
      />
    );
    expect(screen.getByRole('group', { name: 'Provider trouble' })).toBeInTheDocument();
  });
});

describe('capability: set a session aside until a time', () => {
  it('offers compact presets that end on a morning', () => {
    const presets = snoozePresets(new Date(2026, 10, 1, 13, 0));
    expect(presets.map((preset) => preset.label)).toEqual(['1 hour', '3 hours', 'Tomorrow morning', 'Next week']);
    expect(new Date(presets[2].until).getHours()).toBe(9);
    expect(new Date(presets[3].until).getDay()).toBe(1);
  });

  it('sends the chosen return time and nothing else', async () => {
    const live = makeSession({ id: 'snoozer', name: 'Parser cleanup' });
    const machine = machineWith([live]);
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Workbench><SessionView sessionId={live.id} isActive /></Workbench>);
    await user.click(await screen.findByText('Set aside until…'));
    const before = Date.now();
    await user.click(screen.getByRole('button', { name: /^1 hour/ }));
    await waitFor(() => expect(machine.sessions[0].setAsideAt).toBeTruthy());
    const until = machine.sessions[0].setAsideUntil ?? 0;
    expect(until).toBeGreaterThanOrEqual(before + 3_600_000 - 1_000);
    expect(until).toBeLessThanOrEqual(Date.now() + 3_600_000);
    expect(daemon.delivered.snoozer ?? []).toEqual([]);
    expect(daemon.ended).toEqual([]);
  }, 20_000);

  it('can set a limited session aside until Sessions resumes it', async () => {
    const session = limited({ id: 'limited-snooze', name: 'Overnight migration' });
    const machine = machineWith([session]);
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Workbench><SessionView sessionId={session.id} isActive /></Workbench>);
    await user.click(await screen.findByText('Set aside until…'));
    await user.click(screen.getByRole('button', { name: /^Limit resets/ }));
    await waitFor(() => expect(machine.sessions[0].setAsideUntil).toBe(RESUME_AT));
    const request = daemon.requests.find((candidate) => candidate.path === '/api/sessions/limited-snooze/set-aside');
    expect(request?.body).toEqual({ setAside: true, untilLimitReset: true });
  }, 20_000);
});

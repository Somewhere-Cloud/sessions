// CAPABILITY: resuming a saved conversation keeps the access it ran with
// unless the person chooses otherwise.
//
// The daemon keeps a saved record's policy when the request names none, and
// gives a record with none the full-access default. The GUI must therefore
// send no override until the person picks one, and show what will apply.
import { describe, expect, it } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ResumeDialog } from '../../src/components/ResumeDialog';
import { resumeExactSession } from '../../src/lib/resumeExactSession';
import { useSessions } from '../../src/store/sessions';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeDaemon, type FakeMachine } from './fake-daemon';
import type { SessionInfo } from '../../src/types';

const PROVIDER_UUID = '6a1d0c3e-2b4f-4c58-9d70-1e2f3a4b5c6d';

function savedRun(permissions?: 'constrained' | 'full'): SessionInfo {
  return makeSession({ id: 'saved-runtime', name: 'Audit run', conversationId: PROVIDER_UUID, exited: true, exitCode: 0, exitedAt: Date.now() - 60_000, permissions });
}

function machineWith(sessions: SessionInfo[]): FakeMachine {
  return {
    id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions,
    resumable: [{ sessionId: PROVIDER_UUID, tool: 'claude', origin: 'Claude Code', title: 'Audit the release', cwd: '/Users/example/project',
      modifiedAt: Date.UTC(2026, 0, 15, 9, 0, 0), firstUserMessage: 'Audit the release', sizeBytes: 12_000 }],
    providers: [{ id: 'claude', name: 'Claude Code', installed: true, updateAvailable: false, models: [{
      id: 'claude-fable-5', displayName: 'Fable 5', description: 'Everyday work', hidden: false, isDefault: true,
      defaultReasoningEffort: 'medium', supportedReasoningEfforts: [{ reasoningEffort: 'medium', description: 'Medium' }]
    }] }]
  };
}

function setup(sessions: SessionInfo[]): FakeDaemon {
  const machine = machineWith(sessions);
  const daemon = installFakeDaemon([machine]);
  useFakeMachines([machine]);
  useSessions.setState({ serverId: 'local', sessions, hydrated: true });
  return daemon;
}

function adoptBody(daemon: FakeDaemon): Record<string, unknown> {
  return daemon.requests.find((request) => request.path === '/api/recovery/adopt')?.body as Record<string, unknown>;
}

async function openSettings(sourceSessionId?: string): Promise<HTMLElement> {
  const user = userEvent.setup();
  render(<ResumeDialog onClose={() => {}} onResumed={() => {}} onStartNew={() => {}} preferredSourceSessionId={sourceSessionId} />);
  await user.click(await screen.findByRole('button', { name: /Audit the release/ }));
  await user.click(await screen.findByText('Change agent, model or permissions'));
  return screen.getByRole('combobox', { name: 'Resume permissions' });
}

async function start(daemon: FakeDaemon): Promise<Record<string, unknown>> {
  await userEvent.setup().click(await screen.findByRole('button', { name: 'Start Claude (Fable 5)' }));
  await waitFor(() => expect(daemon.adopted).toEqual([PROVIDER_UUID]));
  return adoptBody(daemon);
}

describe('capability: resume keeps the recorded access', () => {
  it('shows a recorded constrained policy and sends no override for it', async () => {
    const run = savedRun('constrained');
    const daemon = setup([run]);
    const permissions = await openSettings(run.id);
    expect(permissions).toHaveValue('constrained');
    expect(screen.getByText('Same access as this conversation’s last run.')).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Start plan' })).not.toHaveTextContent('Full access (YOLO)');
    const body = await start(daemon);
    expect(body).toMatchObject({ target: PROVIDER_UUID, sourceSessionId: run.id });
    expect(body).not.toHaveProperty('permissions');
  });

  it('sends full access only when the person chooses it over a constrained record', async () => {
    const run = savedRun('constrained');
    const daemon = setup([run]);
    const permissions = await openSettings(run.id);
    await userEvent.setup().selectOptions(permissions, 'full');
    expect(screen.queryByText('Same access as this conversation’s last run.')).toBeNull();
    expect(await start(daemon)).toMatchObject({ permissions: 'full' });
  });

  it('sends constrained access when the person chooses it over a full record', async () => {
    const run = savedRun('full');
    const daemon = setup([run]);
    const permissions = await openSettings(run.id);
    expect(permissions).toHaveValue('full');
    await userEvent.setup().selectOptions(permissions, 'constrained');
    expect(await start(daemon)).toMatchObject({ permissions: 'constrained' });
  });

  it('keeps a recorded full policy without an override', async () => {
    const run = savedRun('full');
    const daemon = setup([run]);
    expect(await openSettings(run.id)).toHaveValue('full');
    expect(await start(daemon)).not.toHaveProperty('permissions');
  });

  it('leaves a conversation with no recorded policy on the daemon default', async () => {
    const daemon = setup([]);
    const permissions = await openSettings();
    expect(permissions).toHaveValue('full');
    expect(screen.queryByText('Same access as this conversation’s last run.')).toBeNull();
    expect(await start(daemon)).not.toHaveProperty('permissions');
  });

  it('resumes a chosen saved row without overriding its recorded policy', async () => {
    const run = savedRun('constrained');
    const daemon = setup([run]);
    await resumeExactSession(run, undefined, undefined, 'local');
    const body = adoptBody(daemon);
    expect(body).toMatchObject({ target: PROVIDER_UUID, sourceSessionId: run.id });
    expect(body).not.toHaveProperty('permissions');
  });
});

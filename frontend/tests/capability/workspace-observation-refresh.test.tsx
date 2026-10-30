import { act, render, screen, waitFor } from '@testing-library/react';
import { expect, it } from 'vitest';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import { useSessions } from '../../src/store/sessions';

it('accepts background branch and favicon observations without new session activity', async () => {
  localStorage.setItem('sessions:projects-machine-scope', 'local');
  localStorage.setItem('sessions:navigator-grouping', 'recent');
  const machine: FakeMachine = { id: 'local', name: 'Mac', host: 'localhost', port: 8787, isDefault: true,
    sessions: [makeSession({ id: 'observed', name: 'Inspect project', lastSummary: 'A crowded response preview', tags: { project: 'Atlas' } })] };
  installFakeDaemon([machine]); useFakeMachines([machine]); render(<Workbench />);
  await screen.findByText('Inspect project', { selector: '.session-nav-title' });
  expect(document.querySelector('[data-session-id="observed"] .session-last-message')).toBeNull();
  const icon = 'data:image/svg+xml;base64,PHN2Zy8+';
  machine.sessions[0] = { ...machine.sessions[0], workspaceGit: { state: 'branch', branch: 'fix/identity', observedAt: 1, iconDataUrl: icon } };
  await act(async () => { await useSessions.getState().refresh(); });
  await waitFor(() => expect(screen.getByText('fix/identity')).toBeVisible());
  expect(document.querySelector('[data-session-id="observed"] .project-mark img')).toHaveAttribute('src', icon);
  machine.sessions[0] = { ...machine.sessions[0], workspaceGit: { state: 'branch', branch: 'main', observedAt: 2 } };
  await act(async () => { await useSessions.getState().refresh(); });
  await waitFor(() => expect(screen.getByText('main')).toBeVisible());
  expect(screen.queryByText('fix/identity')).toBeNull();
  expect(document.querySelector('[data-session-id="observed"] .project-mark img')).toBeNull();
});

it('keeps each project identity in a mixed implicit group without branding the group', async () => {
  localStorage.setItem('sessions:projects-machine-scope', 'local');
  localStorage.setItem('sessions:navigator-grouping', 'project');
  const icon = 'data:image/svg+xml;base64,PHN2Zy8+';
  const sessions = ['Atlas', 'Beacon', 'Named'].map((name) => makeSession({ id: name, name: `Inspect ${name}`, tags: { project: name },
    workspaceGit: { state: 'branch', branch: 'main', observedAt: 1, iconDataUrl: icon } }));
  const machine: FakeMachine = { id: 'local', name: 'Mac', host: 'localhost', port: 8787, isDefault: true, sessions,
    projects: [
      { id: 'other', name: 'Other projects', implicit: true, roots: [], session_ids: ['Atlas', 'Beacon'], live: 2, needs_input: 0 },
      { id: 'named', name: 'Named', implicit: false, roots: [], session_ids: ['Named'], live: 1, needs_input: 0 },
    ] };
  installFakeDaemon([machine]); useFakeMachines([machine]); render(<Workbench />);
  await screen.findByText('Inspect Atlas', { selector: '.session-nav-title' });
  await waitFor(() => expect(document.querySelector('.inbox-project.is-implicit [data-session-id="Atlas"] .session-nav-project-label')).toHaveTextContent('Atlas'));
  expect(document.querySelector('.inbox-project.is-implicit .inbox-project-head img')).toBeNull();
  for (const name of ['Atlas', 'Beacon']) {
    const row = document.querySelector(`[data-session-id="${name}"]`)!;
    expect(row.querySelector('.session-nav-project-label')).toHaveTextContent(name);
    expect(row.querySelector('.project-mark img')).toHaveAttribute('src', icon);
    expect(row.querySelector('.session-nav-stamp .sr-only .machine-mark')).not.toBeNull();
  }
  expect(document.querySelector('.inbox-project:not(.is-implicit) .inbox-project-head img')).toHaveAttribute('src', icon);
  expect(document.querySelector('[data-session-id="Named"] .session-nav-project-label')).toBeNull();
});

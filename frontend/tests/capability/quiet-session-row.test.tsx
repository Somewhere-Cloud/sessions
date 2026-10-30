import { expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionLastMessage } from '../../src/components/SessionLastMessage';
import { ProjectMark, SessionWorkspaceBranch } from '../../src/components/SessionProjectIdentity';
import { quietSessionTitle } from '../../src/lib/tabLabels';
import { makeSession } from './fake-daemon';

it('quiet previews omit terminal chrome while preserving authored content', () => {
  const base = makeSession({ id: 'quiet', lastSummary: 'Agent: **Keep the draft**', tool: 'codex' });
  const { rerender } = render(<SessionLastMessage session={base} quiet />);
  expect(screen.getByText('Keep the draft')).toBeVisible();
  for (const lastSummary of ['─────', '❯ redraw output', '\u001b[31mThinking…']) {
    rerender(<SessionLastMessage session={{ ...base, lastSummary }} quiet />);
    expect(document.querySelector('.session-last-message')).toBeNull();
  }
  for (const lastSummary of ['Use ├── src for the tree layout', 'Running the migration succeeded', '100% complete', 'Escape handling is fixed', '> Keep this quoted instruction', '> The migration completed', '$ 50 is the total']) {
    rerender(<SessionLastMessage session={{ ...base, lastSummary }} quiet />);
    expect(screen.getByText(lastSummary)).toBeVisible();
  }
  rerender(<SessionLastMessage session={{ ...base, tool: 'terminal' }} quiet />);
  expect(document.querySelector('.session-last-message')).toBeNull();
  expect(quietSessionTitle('Codex: Review search')).toBe('Review search');
  expect(quietSessionTitle('Codex: ')).toBe('Codex: ');
  expect(quietSessionTitle('claude-structured session')).toBe('claude-structured session');
});

it('workspace observations never infer a branch from creation provenance', () => {
  const session = makeSession({ id: 'workspace', branch: 'creation-only', sourceRepo: '/wrong-root' });
  const { rerender, container } = render(<SessionWorkspaceBranch session={session} />);
  expect(container).toBeEmptyDOMElement();
  rerender(<SessionWorkspaceBranch session={{ ...session, workspaceGit: { state: 'unavailable', observedAt: 1 } }} />);
  expect(screen.getByText('Branch unavailable')).toBeVisible();
  expect(container).not.toHaveTextContent('creation-only');
  rerender(<SessionWorkspaceBranch session={{ ...session, workspaceGit: { state: 'detached', revision: 'abcdef123456', observedAt: 1 } }} />);
  expect(screen.getByText('Detached abcdef12')).toBeVisible();
});

it('project icons use local image data and retain an initials fallback', () => {
  const { container, rerender } = render(<ProjectMark name="Sessions" icon="https://example.invalid/logo.png" />);
  expect(container.querySelector('img')).toBeNull();
  const icon = 'data:image/png;base64,aWNvbg==';
  rerender(<ProjectMark name="Sessions" icon={icon} />);
  const image = container.querySelector('img')!;
  expect(image.getAttribute('src')).toBe(icon);
  fireEvent.error(image);
  expect(image).not.toBeVisible();
  expect(container).toHaveTextContent('S');
});

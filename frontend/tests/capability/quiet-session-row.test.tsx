import { expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SessionLastMessage } from '../../src/components/SessionLastMessage';
import { SessionWorkspaceBranch } from '../../src/components/SessionProjectIdentity';
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

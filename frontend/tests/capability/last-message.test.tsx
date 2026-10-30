// CAPABILITY: the last message a session shows is the last message.
//
// Regression scenario: a row kept showing an agent result after the person
// had already sent something else, and a provider outage read as the agent's
// reply. Both were true of every list in the app, because each one reached for
// `lastSummary` — which is the last result the agent produced, deliberately
// survives the next turn, and used to have provider faults written into it.
import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { Workbench } from './harness';
import { SessionLastMessage } from '../../src/components/SessionLastMessage';
import { lastMessage, lastMessageLine } from '../../src/lib/lastMessage';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import type { SessionInfo } from '../../src/types';

const EARLIER = 1_757_000_000_000;
const LATER = EARLIER + 60_000;
const LATEST = LATER + 60_000;

/** One session of a given kind, with the stamps the daemon would have set. */
function sessionOfKind(kind: string, tool: SessionInfo['tool'], extra: Partial<SessionInfo> = {}): SessionInfo {
  const session = makeSession({ id: `${kind || 'pty'}-session`, name: `${kind || 'pty'} session`, tool });
  session.kind = kind || undefined;
  return Object.assign(session, extra);
}

describe('capability: what a session says its last message is', () => {
  it('keeps an adopted reply at its output time rather than daemon restart time', () => {
    const session = sessionOfKind('', 'claude-code', {
      lastHumanMessageAt: EARLIER, lastDataAt: LATER,
      idleReason: 'completed', idleSince: LATEST, lastSummary: 'Saved reply.'
    });
    expect(lastMessage(session)).toMatchObject({ kind: 'agent', at: LATER, text: 'Saved reply.' });
    expect(lastMessage({ ...session, idleSince: LATEST + 60_000 }).at).toBe(LATER);
  });

  it('shows the agent reply when the agent spoke last', () => {
    for (const [kind, tool] of [
      ['', 'claude-code'], ['claude-structured', 'claude-code'],
      ['codex-app-server', 'codex'], ['lane', 'lane'], ['', 'terminal']
    ] as Array<[string, SessionInfo['tool']]>) {
      const session = sessionOfKind(kind, tool, {
        lastHumanMessageAt: EARLIER, idleReason: 'completed', idleSince: LATER,
        lastSummary: 'Implementation is ready for review.'
      });
      expect(lastMessageLine(session), `${kind || 'pty'}/${tool}`)
        .toBe('Agent: Implementation is ready for review.');
      expect(lastMessage(session).at, `${kind || 'pty'}/${tool}`).toBe(LATER);
    }
  });

  it('does not mistake input relayed by another agent for an assistant reply', () => {
    const relayed = sessionOfKind('lane', 'claude-code', {
      lastAgentMessageAt: LATER,
      lastSummary: 'An older completed result.'
    });
    expect(lastMessageLine(relayed)).toBe('');
    expect(lastMessage(relayed).kind).toBe('none');
  });

  // Regression scenario: the person sent something and the agent has not
  // answered, so the agent's previous result is not the last message.
  it('says the person spoke last, and does not show the older agent result', () => {
    const waiting = sessionOfKind('claude-structured', 'claude-code', {
      lastAgentMessageAt: EARLIER, lastHumanMessageAt: LATER,
      lastSummary: 'Implementation is ready for review.'
    });
    expect(lastMessageLine(waiting)).toBe('You sent a message · no reply yet');
    expect(lastMessage(waiting).at).toBe(LATER);
    expect(lastMessage(waiting).awaitingReply).toBe(true);

    const working = { ...waiting, working: true };
    expect(lastMessageLine(working)).toBe('You sent a message · working');
  });

  // A provider outage is not something the agent said.
  it('shows a fault after the last reply as a fault', () => {
    const faulted = sessionOfKind('codex-app-server', 'codex', {
      lastHumanMessageAt: EARLIER, idleReason: 'completed', idleSince: LATER,
      lastSummary: 'Implementation is ready for review.',
      failureKind: 'provider-unavailable',
      failureDetail: 'Codex API unavailable (503, overloaded)',
      failureAt: LATEST
    });
    expect(lastMessage(faulted).kind).toBe('fault');
    expect(lastMessageLine(faulted)).toBe('Codex API unavailable (503, overloaded)');
    expect(lastMessage(faulted).at).toBe(LATEST);

    render(<SessionLastMessage session={faulted} />);
    const line = screen.getByText('Codex API unavailable (503, overloaded)');
    expect(line.className).toContain('is-fault');
    expect(line.title).toMatch(/not a message from the agent/);
  });

  // A fault that happened before the agent's last reply is old news.
  it('keeps the agent reply when the fault came first', () => {
    const recovered = sessionOfKind('claude-structured', 'claude-code', {
      lastHumanMessageAt: EARLIER, idleReason: 'completed', idleSince: LATEST,
      lastSummary: 'Recovered and finished the change.',
      failureKind: 'provider-unavailable', failureDetail: 'was overloaded', failureAt: LATER
    });
    expect(lastMessageLine(recovered)).toBe('Agent: Recovered and finished the change.');
  });

  it('uses a finished reply after the human send, but not a stale summary during a new turn', () => {
    const finished = sessionOfKind('claude-structured', 'claude-code', {
      lastHumanMessageAt: EARLIER, idleReason: 'completed', idleSince: LATER,
      lastSummary: 'The handoff is complete.'
    });
    expect(lastMessageLine(finished)).toBe('Agent: The handoff is complete.');
    expect(lastMessage(finished)).toMatchObject({ at: LATER, awaitingReply: false });

    const nextTurn = { ...finished, idleReason: undefined, idleSince: null, working: true };
    expect(lastMessageLine(nextTurn)).toBe('You sent a message · working');
  });

  it('does not elevate an old summary when a later turn needs input or recovery', () => {
    for (const idleReason of ['needs-input', 'needs-recovery'] as const) {
      const blocked = sessionOfKind('claude-structured', 'claude-code', {
        lastHumanMessageAt: LATER,
        idleReason: idleReason as SessionInfo['idleReason'], idleSince: LATEST,
        lastSummary: 'An older completed result.'
      });
      expect(lastMessageLine(blocked), idleReason).toBe('You sent a message · no reply yet');
      expect(lastMessage(blocked).awaitingReply, idleReason).toBe(true);
    }
  });

  // A send this client has made and not seen acknowledged is not a delivery.
  it('labels an unconfirmed send as unconfirmed', () => {
    const session = sessionOfKind('claude-structured', 'claude-code', {
      idleReason: 'completed', idleSince: LATER, lastSummary: 'Older result.'
    });
    expect(lastMessageLine(session, 'check the release notes')).toBe('You: check the release notes · sending…');
    expect(lastMessage(session, 'check the release notes').awaitingReply).toBe(true);
  });

  it('describes shell input without expecting an assistant reply or interpreting output as completion', () => {
    for (const working of [false, true]) {
      for (const lastDataAt of [EARLIER, LATEST]) {
        const shell = sessionOfKind('', 'terminal', {
          lastHumanMessageAt: LATER, lastDataAt, working
        });
        expect(lastMessageLine(shell)).toBe('You sent terminal input');
        expect(lastMessage(shell)).toMatchObject({
          kind: 'you', at: LATER, awaitingReply: false
        });
        render(<SessionLastMessage session={shell} />);
      }
    }
    expect(screen.getAllByText('You sent terminal input')).toHaveLength(4);
    expect(screen.queryByText(/no reply yet|Agent replied|completed/i)).not.toBeInTheDocument();
  });

  it('keeps shell sends unconfirmed until acknowledged, without an assistant-reply expectation', () => {
    const shell = sessionOfKind('', 'terminal');
    expect(lastMessageLine(shell, 'printf hello')).toBe('You: printf hello · sending…');
    expect(lastMessage(shell, 'printf hello')).toMatchObject({
      kind: 'you', text: 'printf hello', awaitingReply: false
    });
  });

  it('does not let terminal bytes clear a Claude or Codex reply expectation', () => {
    for (const tool of ['claude-code', 'codex'] as const) {
      const provider = sessionOfKind('', tool, {
        lastHumanMessageAt: LATER, lastDataAt: LATEST, working: false
      });
      expect(lastMessageLine(provider)).toBe('You sent a message · no reply yet');
      expect(lastMessage(provider).awaitingReply).toBe(true);
    }
  });

  it('says nothing rather than something wrong when nothing has been said', () => {
    const fresh = sessionOfKind('', 'terminal', {});
    expect(lastMessageLine(fresh)).toBe('');
    const { container } = render(<SessionLastMessage session={fresh} />);
    expect(container.querySelector('.session-last-message')).toBeNull();
  });
});

describe('capability: the navigator row carries it', () => {
  function fleet(sessions: SessionInfo[]): FakeMachine[] {
    return [{ id: 'local', name: 'This Mac', host: 'localhost', port: 8787, isDefault: true, sessions }];
  }

  it('shows the newest message and its own time on the row', async () => {
    const waiting = sessionOfKind('claude-structured', 'claude-code', {
      lastAgentMessageAt: EARLIER, lastHumanMessageAt: LATER,
      lastSummary: 'An older agent result.', lastDataAt: LATER
    });
    const machines = fleet([waiting]);
    installFakeDaemon(machines);
    useFakeMachines(machines, 'local');

    render(<Workbench />);
    const row = await screen.findByText(waiting.name!);
    const node = row.closest('.session-nav-row') as HTMLElement;
    expect(within(node).getByText('Waiting for reply')).toBeInTheDocument();
    expect(within(node).queryByText(/An older agent result/)).not.toBeInTheDocument();
  }, 20_000);

  it('does not claim a shell with later output is still waiting for a reply', async () => {
    const shell = sessionOfKind('', 'terminal', {
      name: 'Shell output observed', lastHumanMessageAt: LATER,
      lastDataAt: LATEST, working: false
    });
    const machines = fleet([shell]);
    installFakeDaemon(machines);
    useFakeMachines(machines, 'local');

    render(<Workbench />);
    const row = (await screen.findByText(shell.name!)).closest('.session-nav-row') as HTMLElement;
    expect(row.querySelector('.session-last-message')).toBeNull();
    expect(row.querySelector('.session-nav-time')).not.toBeNull();
    expect(within(row).queryByText(/no reply yet|Agent replied/)).not.toBeInTheDocument();
  }, 20_000);
});

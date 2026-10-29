import { describe, expect, it } from 'vitest';
import { classifySnapshotComposerState } from '../../src/lib/detectMultiChoice';
import { endedAtLabel } from '../../src/lib/sessionStatus';
import { makeSession } from './fake-daemon';

describe('native review regressions', () => {
  it.each(['❯', '›', '>'])('keeps the %s input box available beneath routine Claude notices', (prompt) => {
    const snapshot = `Choose which approach to discuss next.
1. First approach
2. Second approach
✔ Update installed · Restart to update
──────────────── Sessions ─
${prompt} 
────────────────────────
▶▶ bypass permissions on (shift+tab to cycle)`;
    expect(classifySnapshotComposerState(snapshot).kind).toBe('normal-composer');
  });

  it('still identifies actual menus and notices without an input box', () => {
    expect(classifySnapshotComposerState(`Choose a mode
────────────
❯ 1. Auto
────────────
  2. Manual
Enter to select · ↑/↓ to navigate · Esc to cancel`).kind).toBe('numbered-picker');
    expect(classifySnapshotComposerState('Update available. Press Enter to continue.').kind).toBe('update/notice-banner');
    expect(classifySnapshotComposerState('Do you trust the files in this folder?').kind).toBe('trust-prompt');
  });

  it('keeps the full closed timestamp available while using a compact row date', () => {
    const ended = new Date(2025, 2, 15, 15, 42);
    const session = makeSession({ id: 'closed-date', exited: true, exitedAt: ended.getTime() });
    expect(endedAtLabel(session, true)).toBe(ended.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }));
    expect(endedAtLabel(session)).toContain('42');
  });
});

import { describe, expect, it, vi } from 'vitest';
import { openViewsKey, migrateOpenViews, readOpenViews, writeOpenViews } from '../../src/lib/openViews';
import { makeSession } from './fake-daemon';

describe('open view identity', () => {
  it('isolates the same UUID by endpoint and window, and restores only its own views', () => {
    const a = openViewsKey('alpha', '?server=alpha');
    const b = openViewsKey('beta', '?server=alpha');
    const otherWindow = openViewsKey('alpha', '?tool=codex');
    writeOpenViews(a, ['same']);
    expect(readOpenViews(a)).toEqual(['same']);
    expect(readOpenViews(b)).toEqual([]);
    expect(readOpenViews(otherWindow)).toEqual([]);
    expect(openViewsKey('alpha')).toEqual(openViewsKey('alpha'));
  });

  it('keeps native main identity across client restarts without adding native window APIs', () => {
    vi.stubGlobal('__TAURI_INTERNALS__', {});
    try {
      const key = openViewsKey('native-host', '');
      writeOpenViews(key, ['native-kept']);
      sessionStorage.clear();
      expect(openViewsKey('native-host', '')).toEqual(key);
      expect(readOpenViews(key)).toEqual(['native-kept']);
    } finally { vi.unstubAllGlobals(); }
  });

  it('preserves legacy history if the scoped migration cannot be saved', () => {
    localStorage.setItem('sessions:open-tabs:v1', JSON.stringify(['kept']));
    const fail = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('storage full'); });
    try {
      expect(migrateOpenViews('migration-failed', [makeSession({ id: 'kept' })], true)).toEqual([]);
      expect(localStorage.getItem('sessions:open-tabs:v1')).toBe(JSON.stringify(['kept']));
    } finally { fail.mockRestore(); }
  });

  it('validates legacy IDs only after known local inventory and never borrows them for peers', () => {
    localStorage.setItem('sessions:open-tabs:v1', JSON.stringify(['same', 'unknown']));
    const remote = openViewsKey('beta', '?server=beta');
    expect(migrateOpenViews(remote, [makeSession({ id: 'same' })], false)).toEqual([]);
    expect(localStorage.getItem('sessions:open-tabs:v1')).not.toBeNull();
    const local = openViewsKey('alpha', '?server=alpha');
    expect(migrateOpenViews(local, [makeSession({ id: 'same' })], true)).toEqual(['same']);
    expect(localStorage.getItem('sessions:open-tabs:v1')).toBeNull();
    expect(migrateOpenViews(remote, [makeSession({ id: 'same' })], false)).toEqual([]);
  });
});

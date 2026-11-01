import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const native = vi.hoisted(() => ({
  check: vi.fn(),
  download: vi.fn(),
  install: vi.fn(),
  notify: vi.fn()
}));
vi.mock('../../src/lib/tauriBridge', () => ({
  isTauri: () => true,
  checkForNativeUpdate: native.check,
  downloadNativeUpdate: native.download,
  installDownloadedNativeUpdate: native.install,
  notifyNativeUpdate: native.notify,
  notifyProviderUpdate: vi.fn(async () => {})
}));

import { AppUpdateCard } from '../../src/components/SidebarUpdateCards';
import { checkAppUpdate, resetAppUpdate, startAppUpdateWatcher, useAppUpdate } from '../../src/lib/appUpdate';

const info = { currentVersion: '0.2.45', version: '0.2.46', notes: null, publishedAt: null };

describe('capability: Sessions updates download quietly and restart on request', () => {
  beforeEach(() => {
    resetAppUpdate();
    native.check.mockReset().mockResolvedValue(info);
    native.download.mockReset().mockImplementation(async (progress?: (value: { downloadedBytes: number; totalBytes: number }) => void) => { progress?.({ downloadedBytes: 5, totalBytes: 10 }); });
    native.install.mockReset().mockResolvedValue(undefined);
    native.notify.mockReset().mockResolvedValue(undefined);
  });

  it('checks after launch, downloads in the background, and installs only on Restart', async () => {
    vi.useFakeTimers();
    try {
      const stop = startAppUpdateWatcher();
      expect(native.check).not.toHaveBeenCalled();
      await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
      expect(native.check).toHaveBeenCalledTimes(1);
      expect(native.download).toHaveBeenCalledTimes(1);
      expect(native.install).not.toHaveBeenCalled();
      expect(useAppUpdate.getState().phase).toBe('ready');
      expect(native.notify).toHaveBeenCalledTimes(1);
      // A ready download is never discarded by a later automatic check.
      await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60 * 1000); });
      expect(native.check).toHaveBeenCalledTimes(1);
      stop();
    } finally {
      vi.useRealTimers();
    }
    render(<AppUpdateCard />);
    expect(screen.getByText('Update ready')).toBeInTheDocument();
    expect(screen.getByText('Only the window restarts. Your sessions keep running.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Restart to update to Sessions 0.2.46' }));
    await waitFor(() => expect(native.install).toHaveBeenCalledTimes(1));
  });

  it('keeps a failed background download quiet and a failed install retryable', async () => {
    native.download.mockRejectedValueOnce(new Error('offline'));
    await checkAppUpdate(false);
    expect(useAppUpdate.getState()).toMatchObject({ phase: 'available', error: null });
    render(<AppUpdateCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Download' }));
    await waitFor(() => expect(useAppUpdate.getState().phase).toBe('ready'));
    native.install.mockRejectedValueOnce(new Error('disk full'));
    fireEvent.click(screen.getByRole('button', { name: 'Restart to update to Sessions 0.2.46' }));
    await screen.findByRole('alert');
    expect(screen.getByRole('alert').textContent).toMatch(/Sessions is unchanged and your sessions keep running/);
    fireEvent.click(screen.getByRole('button', { name: 'Restart to update to Sessions 0.2.46' }));
    await waitFor(() => expect(native.install).toHaveBeenCalledTimes(2));
  });

  it('says nothing when an automatic check fails', async () => {
    native.check.mockRejectedValueOnce(new Error('offline'));
    await checkAppUpdate(false);
    expect(useAppUpdate.getState()).toMatchObject({ phase: 'idle', error: null });
    const { container } = render(<AppUpdateCard />);
    expect(container).toBeEmptyDOMElement();
  });
});

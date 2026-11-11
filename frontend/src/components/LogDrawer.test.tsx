import React from 'react';
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchLogs } from '../api/client';
import { LogDrawer } from './LogDrawer';

vi.mock('../api/client', () => ({
  fetchLogs: vi.fn(),
  clearLogs: vi.fn(),
  notifyToast: vi.fn(),
}));

beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(fetchLogs).mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('LogDrawer polling', () => {
  it('waits for a slow log request before scheduling the next poll', async () => {
    let resolveFirst!: (value: { logs: [] }) => void;
    vi.mocked(fetchLogs)
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValue({ logs: [] });

    render(<LogDrawer isOpen onClose={() => {}} />);
    expect(fetchLogs).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(2500); });
    expect(fetchLogs).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveFirst({ logs: [] });
      await Promise.resolve();
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(2499); });
    expect(fetchLogs).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(fetchLogs).toHaveBeenCalledTimes(2);
  });
});

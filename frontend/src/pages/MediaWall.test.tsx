import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchLatestItems, fetchLibraryViews, fetchMediaItems, fetchNextUpItems } from '../api/client';
import { MediaWall } from './MediaWall';
import { User } from '../types';

vi.mock('../api/client', () => ({
  fetchLatestItems: vi.fn(),
  fetchLibraryViews: vi.fn(),
  fetchMediaItems: vi.fn(),
  fetchNextUpItems: vi.fn(),
  notifyToast: vi.fn(),
  getStoredToken: vi.fn(() => 'token'),
}));

const user = { Id: 'user-1', Policy: { IsAdministrator: false } } as User;

beforeEach(() => {
  vi.mocked(fetchLibraryViews).mockResolvedValue([]);
  vi.mocked(fetchLatestItems).mockResolvedValue([]);
  vi.mocked(fetchNextUpItems).mockResolvedValue([]);
  vi.mocked(fetchMediaItems).mockResolvedValue({ Items: [], TotalRecordCount: 0 });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('MediaWall request limits', () => {
  it('debounces rapid search changes and requests only the latest query', async () => {
    vi.useFakeTimers();
    const { rerender } = render(<MediaWall user={user} searchQuery="" />);
    await act(async () => { await Promise.resolve(); });
    vi.mocked(fetchMediaItems).mockClear();

    rerender(<MediaWall user={user} searchQuery="a" />);
    await act(async () => { await Promise.resolve(); });
    rerender(<MediaWall user={user} searchQuery="ab" />);
    await act(async () => { await Promise.resolve(); });
    expect(fetchMediaItems).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(250);
    });
    expect(fetchMediaItems).toHaveBeenCalledTimes(1);
    expect(vi.mocked(fetchMediaItems).mock.calls[0][0].get('SearchTerm')).toBe('ab');
  });

  it('ignores a slower response for an obsolete search', async () => {
    vi.useFakeTimers();
    const pending: Array<(value: { Items: any[]; TotalRecordCount: number }) => void> = [];
    vi.mocked(fetchMediaItems).mockImplementation(() => new Promise((resolve) => pending.push(resolve)));
    const { rerender } = render(<MediaWall user={user} searchQuery="" />);
    expect(fetchMediaItems).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.shift()?.({ Items: [], TotalRecordCount: 0 });
      await Promise.resolve();
    });

    rerender(<MediaWall user={user} searchQuery="old" />);
    await act(async () => { await Promise.resolve(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(250); });
    expect(fetchMediaItems).toHaveBeenCalledTimes(2);

    rerender(<MediaWall user={user} searchQuery="new" />);
    await act(async () => { await Promise.resolve(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(250); });
    expect(fetchMediaItems).toHaveBeenCalledTimes(3);

    const item = { Id: 'new-item', Name: 'new-result', Type: 'Movie' };
    await act(async () => {
      pending[1]({ Items: [item], TotalRecordCount: 1 });
      await Promise.resolve();
    });
    expect(screen.getByText('new-result')).toBeInTheDocument();
    await act(async () => {
      pending[0]({ Items: [{ Id: 'old-item', Name: 'old-result', Type: 'Movie' }], TotalRecordCount: 1 });
      await Promise.resolve();
    });
    expect(screen.getByText('new-result')).toBeInTheDocument();
    expect(screen.queryByText('old-result')).not.toBeInTheDocument();
  });

  it('limits latest-item requests to three concurrent libraries', async () => {
    const views = Array.from({ length: 5 }, (_, index) => ({ Id: `lib-${index}`, Name: `Library ${index}` }));
    vi.mocked(fetchLibraryViews).mockResolvedValue(views as any);
    let active = 0;
    let maxActive = 0;
    const resolvers: Array<() => void> = [];
    vi.mocked(fetchLatestItems).mockImplementation(() => {
      active++;
      maxActive = Math.max(maxActive, active);
      return new Promise((resolve) => {
        resolvers.push(() => {
          active--;
          resolve([]);
        });
      });
    });

    render(<MediaWall user={user} />);
    await waitFor(() => expect(fetchLatestItems).toHaveBeenCalledTimes(3));
    expect(maxActive).toBe(3);

    await act(async () => {
      resolvers.splice(0, 3).forEach((resolve) => resolve());
      await Promise.resolve();
    });
    await waitFor(() => expect(fetchLatestItems).toHaveBeenCalledTimes(5));
    expect(maxActive).toBe(3);
    await act(async () => resolvers.splice(0).forEach((resolve) => resolve()));
  });
});

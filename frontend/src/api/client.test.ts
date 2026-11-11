import { afterEach, describe, expect, it, vi } from 'vitest';
import { request } from './client';

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('API request cancellation', () => {
  it('forwards an optional AbortSignal to fetch', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => JSON.stringify({ ok: true }),
    });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();

    await expect(request('/test', 'GET', undefined, controller.signal)).resolves.toEqual({ ok: true });
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);
  });
});

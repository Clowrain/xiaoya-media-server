import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AdminLayout } from './AdminLayout';
import { MediaItem } from '../../types';

vi.mock('./AdminLibraries', () => ({ AdminLibraries: () => <div>Libraries panel loaded</div> }));
vi.mock('./AdminXiaoyaSync', () => ({ AdminXiaoyaSync: () => <div>Sync panel loaded</div> }));
vi.mock('./AdminResources', () => ({ AdminResources: () => <div>Resources panel loaded</div> }));
vi.mock('./AdminSystemUsers', () => ({ AdminSystemUsers: () => <div>Users panel loaded</div> }));
vi.mock('./AdminTmdb', () => ({
  AdminTmdb: ({ initialMatchItem }: { initialMatchItem?: MediaItem | null }) => (
    <div>TMDB panel {initialMatchItem?.Id || 'empty'}</div>
  ),
}));

describe('AdminLayout lazy panels', () => {
  it('loads only the selected panel and preserves wall-to-TMDB navigation', async () => {
    render(<AdminLayout onOpenLogs={() => {}} />);
    expect(await screen.findByText('Libraries panel loaded')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /小雅同步/ }));
    expect(await screen.findByText('Sync panel loaded')).toBeInTheDocument();

    const item = { Id: 'item-42', Name: 'Example', Type: 'Movie' } as MediaItem;
    render(<AdminLayout initialMatchItem={item} onOpenLogs={() => {}} />);
    expect(await screen.findByText('TMDB panel item-42')).toBeInTheDocument();
  });
});

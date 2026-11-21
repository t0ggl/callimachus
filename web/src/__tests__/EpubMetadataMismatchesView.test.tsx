import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EpubMetadataMismatchesView } from '../views/EpubMetadataMismatchesView';
import type { EPUBMetadataMismatchItem } from '../types';

const sampleMismatches: EPUBMetadataMismatchItem[] = [
  {
    bookId: 'book-1',
    title: 'The Final Empire',
    authorName: 'Brandon Sanderson',
    ideal: { title: 'The Final Empire', series: 'Mistborn', seriesIndex: 1 },
    idealSource: 'match',
    mismatchedFields: ['title', 'series', 'seriesIndex'],
    files: [
      {
        assetId: 'asset-1',
        filename: 'The Final Empire (2006) - Brandon Sanderson.epub',
        current: { title: '01 - Mistborn [Retail]', series: '', seriesIndex: undefined },
        mismatches: [
          { field: 'title', current: '01 - Mistborn [Retail]', ideal: 'The Final Empire' },
          { field: 'series', current: '', ideal: 'Mistborn' },
          { field: 'seriesIndex', current: '', ideal: '1' },
        ],
      },
    ],
  },
  {
    bookId: 'book-2',
    title: 'Edgedancer',
    authorName: 'Brandon Sanderson',
    ideal: { title: 'Edgedancer', series: 'The Stormlight Archive', seriesIndex: 2.5 },
    idealSource: 'catalog',
    mismatchedFields: ['seriesIndex'],
    files: [
      {
        assetId: 'asset-2',
        filename: 'Edgedancer (2016) - Brandon Sanderson.epub',
        current: { title: 'Edgedancer', series: 'The Stormlight Archive', seriesIndex: 2 },
        mismatches: [
          { field: 'seriesIndex', current: '2', ideal: '2.5' },
        ],
      },
    ],
  },
];

describe('EpubMetadataMismatchesView', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('displays mismatched title, series, and series index fields and supports filtering', async () => {
    const user = userEvent.setup({ delay: null });
    const onCountChanged = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(Response.json({ items: sampleMismatches, scannedBooks: 12 })),
    );

    render(<EpubMetadataMismatchesView onCountChanged={onCountChanged} />);

    const list = await screen.findByRole('list', { name: 'EPUB metadata mismatches' });
    expect(within(list).getAllByRole('listitem')).toHaveLength(2);
    expect(onCountChanged).toHaveBeenCalledWith(2);
    expect(screen.getByText('12 EPUB books scanned')).toBeInTheDocument();

    const firstRow = within(screen.getByTestId('mismatch-book-1'));
    expect(firstRow.getByText('01 - Mistborn [Retail]')).toBeInTheDocument();
    expect(firstRow.getByText('Mistborn')).toBeInTheDocument();
    expect(firstRow.getAllByText('Missing')).toHaveLength(2);

    // Filter by field: Title -> only book-1 remains
    const fieldGroup = screen.getByRole('group', { name: 'Filter by mismatched field' });
    await user.click(within(fieldGroup).getByRole('button', { name: 'Title' }));
    expect(within(screen.getByRole('list', { name: 'EPUB metadata mismatches' })).getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByTestId('mismatch-book-1')).toBeInTheDocument();
    expect(screen.queryByTestId('mismatch-book-2')).not.toBeInTheDocument();

    // Search filter with no match -> Clear Filters resets
    const searchInput = screen.getByRole('textbox', { name: 'Filter metadata mismatches' });
    await user.type(searchInput, 'Nonexistent');
    expect(screen.getByText('No matching books')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Clear Filters' }));
    expect(within(screen.getByRole('list', { name: 'EPUB metadata mismatches' })).getAllByRole('listitem')).toHaveLength(2);
  });

  it('applies ideal metadata via POST and removes the resolved book', async () => {
    const user = userEvent.setup({ delay: null });
    const onCatalogChanged = vi.fn();
    const onCountChanged = vi.fn();
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/activity/metadata-mismatches') {
        return Response.json({ items: sampleMismatches, scannedBooks: 12 });
      }
      if (url === '/api/activity/metadata-mismatches/book-1/apply' && init?.method === 'POST') {
        return Response.json({ bookId: 'book-1', updated: 1 });
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <EpubMetadataMismatchesView
        onCatalogChanged={onCatalogChanged}
        onCountChanged={onCountChanged}
      />,
    );

    const applyBtn = await screen.findByRole('button', { name: 'Apply ideal metadata to The Final Empire' });
    await user.click(applyBtn);

    await waitFor(() => {
      expect(screen.queryByTestId('mismatch-book-1')).not.toBeInTheDocument();
    });
    expect(screen.getByRole('status')).toHaveTextContent('Applied ideal metadata to The Final Empire.');
    expect(onCatalogChanged).toHaveBeenCalledOnce();
    expect(onCountChanged).toHaveBeenLastCalledWith(1);
    expect(screen.getByTestId('mismatch-book-2')).toBeInTheDocument();
  });

  it('shows inline error if applying ideal metadata fails and keeps the row', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/activity/metadata-mismatches') {
        return Response.json({ items: [sampleMismatches[0]], scannedBooks: 5 });
      }
      if (url === '/api/activity/metadata-mismatches/book-1/apply') {
        return new Response('EPUB file is read-only.', {
          status: 500,
          headers: { 'Content-Type': 'text/plain' },
        });
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<EpubMetadataMismatchesView />);

    await user.click(await screen.findByRole('button', { name: 'Apply ideal metadata to The Final Empire' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('EPUB file is read-only.');
    expect(screen.getByTestId('mismatch-book-1')).toBeInTheDocument();
  });

  it('opens Fix Metadata directly in BookDetailModal when requested', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/activity/metadata-mismatches') {
        return Response.json({ items: [sampleMismatches[0]], scannedBooks: 4 });
      }
      if (url === '/api/books?id=book-1') {
        return Response.json([
          {
            id: 'book-1',
            title: 'The Final Empire',
            authorId: 'a1',
            authorName: 'Brandon Sanderson',
            seriesTitle: 'Mistborn',
            seriesIndex: 1,
            formats: ['EPUB'],
            editions: [
              {
                id: 'e1',
                bookId: 'book-1',
                format: 'EPUB',
                language: 'en',
                assets: [
                  {
                    id: 'asset-1',
                    editionId: 'e1',
                    filename: 'The Final Empire (2006) - Brandon Sanderson.epub',
                    format: 'EPUB',
                  },
                ],
              },
            ],
          },
        ]);
      }
      if (url === '/api/books/book-1/metadata-candidates') {
        return Response.json([]);
      }
      if (url === '/api/books/book-1/metadata-diff') {
        return Response.json({ preview: 'abc', files: [] });
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<EpubMetadataMismatchesView />);

    await user.click(await screen.findByRole('button', { name: 'Fix metadata for The Final Empire' }));

    const dialog = await screen.findByRole('dialog', { name: 'Details for The Final Empire' });
    expect(within(dialog).getByRole('heading', { name: 'Fix Metadata' })).toBeInTheDocument();
  });

  it('renders empty state when no mismatches exist and error alert with retry when load fails', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('Forbidden', { status: 403 }))
      .mockResolvedValueOnce(Response.json({ items: [], scannedBooks: 8 }));
    vi.stubGlobal('fetch', fetchMock);

    render(<EpubMetadataMismatchesView />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Administrator privileges are required to view EPUB metadata mismatches.',
    );

    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('No significant EPUB metadata mismatches')).toBeInTheDocument();
    expect(screen.getByText(/Checked 8 EPUB books/)).toBeInTheDocument();
  });
});

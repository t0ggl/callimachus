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

    const applyBtn = await screen.findByRole('button', { name: 'Apply ideal metadata now to The Final Empire' });
    expect(applyBtn).toHaveTextContent('Apply Now');
    const previewBtn = screen.getByRole('button', { name: 'Preview and edit metadata for The Final Empire' });
    expect(previewBtn).toHaveTextContent('Preview & Edit');
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

    await user.click(await screen.findByRole('button', { name: 'Apply ideal metadata now to The Final Empire' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('EPUB file is read-only.');
    expect(screen.getByTestId('mismatch-book-1')).toBeInTheDocument();
  });

  it('opens Preview & Edit directly in BookDetailModal and pre-selects ideal metadata when it differs from catalog', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/activity/metadata-mismatches') {
        return Response.json({ items: [sampleMismatches[0]], scannedBooks: 4 });
      }
      if (url === '/api/books?id=book-1') {
        return Response.json([
          {
            id: 'book-1',
            title: '01 - Mistborn [Retail]',
            authorId: 'a1',
            authorName: 'Brandon Sanderson',
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
      if (url === '/api/books/book-1/metadata-diff' && init?.method === 'POST') {
        return Response.json({
          preview: 'ideal-preview-hash',
          bookDiffs: [
            { field: 'title', fileValue: '01 - Mistborn [Retail]', libraryValue: 'The Final Empire', changed: true },
            { field: 'authors', fileValue: 'Brandon Sanderson', libraryValue: 'Brandon Sanderson', changed: false },
          ],
          files: [
            {
              id: 'asset-1',
              filename: 'The Final Empire (2006) - Brandon Sanderson.epub',
              diffs: [
                { field: 'title', fileValue: '01 - Mistborn [Retail]', libraryValue: 'The Final Empire', changed: true },
                { field: 'authors', fileValue: 'Brandon Sanderson', libraryValue: 'Brandon Sanderson', changed: false },
                { field: 'subtitle', fileValue: '', libraryValue: '', changed: false },
              ],
            },
          ],
        });
      }
      if (url === '/api/books/book-1/fix-metadata' && init?.method === 'POST') {
        return Response.json({ updated: 1 });
      }
      throw new Error(`Unexpected fetch ${init?.method ?? 'GET'} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<EpubMetadataMismatchesView />);

    await user.click(await screen.findByRole('button', { name: 'Preview and edit metadata for The Final Empire' }));

    const dialog = await screen.findByRole('dialog', { name: 'Details for 01 - Mistborn [Retail]' });
    expect(within(dialog).getByRole('heading', { name: 'Fix Metadata' })).toBeInTheDocument();
    const idealRadio = within(dialog).getByRole('radio', { name: /Use matched ideal metadata/ });
    expect(idealRadio).toBeChecked();

    // Preview displays both changed and unchanged fields that will be set, with accessible non-color cues.
    expect(await within(dialog).findByText('Preview metadata to apply')).toBeInTheDocument();
    expect(within(dialog).getAllByText('Changed').length).toBeGreaterThanOrEqual(2);
    expect(within(dialog).getAllByText('Unchanged').length).toBeGreaterThanOrEqual(2);
    expect(within(dialog).getAllByText('Brandon Sanderson').length).toBeGreaterThanOrEqual(2);
    expect(within(dialog).queryByText('Subtitle')).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Apply Metadata' }));
    expect(await within(dialog).findByText('Metadata applied to the library and EPUB files.')).toBeInTheDocument();

    const applyCall = fetchMock.mock.calls.find(([u]) => u === '/api/books/book-1/fix-metadata');
    expect(applyCall).toBeDefined();
    expect(JSON.parse(String(applyCall?.[1]?.body))).toEqual({
      candidate: {
        title: 'The Final Empire',
        series: 'Mistborn',
        series_index: 1,
      },
      preview: 'ideal-preview-hash',
    });
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

  it('surfaces duplicate EPUBs across differing series names and resolves explicitly after user selection', async () => {
    const user = userEvent.setup({ delay: null });
    const onCatalogChanged = vi.fn();
    const duplicateItem: EPUBMetadataMismatchItem = {
      bookId: 'book-dup',
      title: 'Altered Carbon',
      authorName: 'Richard K. Morgan',
      ideal: { title: 'Altered Carbon', series: 'Takeshi Kovacs', seriesIndex: 1 },
      idealSource: 'match',
      mismatchedFields: ['series'],
      hasDuplicates: true,
      files: [
        {
          assetId: 'asset-ac',
          bookId: 'book-dup',
          filename: 'Altered Carbon (2002) - Richard K. Morgan.epub',
          relativePath: 'Richard K. Morgan/Altered Carbon/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub',
          fileSizeBytes: 512 * 1024,
          seriesName: 'Altered Carbon',
          current: { title: 'Altered Carbon', series: 'Altered Carbon', seriesIndex: 1 },
          mismatches: [{ field: 'series', current: 'Altered Carbon', ideal: 'Takeshi Kovacs' }],
        },
        {
          assetId: 'asset-tk',
          bookId: 'book-dup',
          filename: 'Altered Carbon (2002) - Richard K. Morgan.epub',
          relativePath: 'Richard K. Morgan/Takeshi Kovacs/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub',
          fileSizeBytes: 640 * 1024,
          seriesName: 'Takeshi Kovacs',
          current: { title: 'Altered Carbon', series: 'Takeshi Kovacs', seriesIndex: 1 },
          mismatches: [],
        },
      ],
    };

    let resolved = false;
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/activity/metadata-mismatches') {
        return Response.json({ items: resolved ? [] : [sampleMismatches[0], duplicateItem], scannedBooks: 10 });
      }
      if (url === '/api/activity/metadata-mismatches/book-dup/resolve-duplicates' && init?.method === 'POST') {
        resolved = true;
        return Response.json({ bookId: 'book-dup', keptAssetId: 'asset-tk', removedAssets: 1, deletedFiles: 3 });
      }
      throw new Error(`Unexpected fetch ${init?.method ?? 'GET'} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<EpubMetadataMismatchesView onCatalogChanged={onCatalogChanged} />);

    // Filter to Duplicates only.
    const fieldGroup = await screen.findByRole('group', { name: 'Filter by mismatched field' });
    await user.click(within(fieldGroup).getByRole('button', { name: 'Duplicates' }));
    expect(screen.queryByTestId('mismatch-book-1')).not.toBeInTheDocument();

    const dupRow = within(screen.getByTestId('mismatch-book-dup'));
    expect(dupRow.getByText('Duplicate EPUBs (2)')).toBeInTheDocument();
    expect(dupRow.getByRole('button', { name: 'Apply ideal metadata now to Altered Carbon' })).toBeDisabled();
    expect(dupRow.queryByRole('button', { name: /Keep Selected & Remove Duplicates/ })).not.toBeInTheDocument();

    // Explicitly select the Takeshi Kovacs copy to keep.
    await user.click(
      dupRow.getByRole('radio', {
        name: 'Keep Richard K. Morgan/Takeshi Kovacs/Altered Carbon (2002)/Altered Carbon (2002) - Richard K. Morgan.epub',
      }),
    );

    const confirmBtn = dupRow.getByRole('button', { name: /Keep Selected & Remove Duplicates/ });
    expect(confirmBtn).toBeEnabled();
    await user.click(confirmBtn);

    await waitFor(() => {
      expect(screen.getByText('No significant EPUB metadata mismatches')).toBeInTheDocument();
    });
    expect(screen.getByRole('status')).toHaveTextContent('Resolved duplicate EPUBs for Altered Carbon.');
    expect(onCatalogChanged).toHaveBeenCalledOnce();

    const resolveCall = fetchMock.mock.calls.find(([u]) => u === '/api/activity/metadata-mismatches/book-dup/resolve-duplicates');
    expect(resolveCall).toBeDefined();
    expect(JSON.parse(String(resolveCall?.[1]?.body))).toEqual({
      keepAssetId: 'asset-tk',
      removeAssetIds: ['asset-ac'],
    });
  });
});

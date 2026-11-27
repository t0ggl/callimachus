import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { it, expect, vi, afterEach } from 'vitest';
import { ManualSearch } from '../components/ManualSearch';
afterEach(() => vi.unstubAllGlobals());
const candidate = {
    key: '1:g',
    release: { title: 'Dune EPUB', size: 1048576, seeders: 60 },
    format: 'epub',
    score: -50,
    penalty: -100,
    breakdown: { format: 30, seeders: 20 },
};
it('shows cached score breakdown, refreshes, and manually retries a penalized release', async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue({ ok: true, json: async () => [candidate] });
  vi.stubGlobal('fetch', fetcher);
  const user = userEvent.setup({ delay: null });
  const close = vi.fn();
  render(<ManualSearch requestId="7" onClose={close} />);
  expect(await screen.findByText('Dune EPUB')).toBeInTheDocument();
  expect(screen.getByText(/failure: -100/)).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Refresh' }));
  expect(fetcher).toHaveBeenCalledWith(
    '/api/requests/7/refresh',
    expect.objectContaining({ method: 'POST' }),
  );
  await user.click(await screen.findByRole('button', { name: 'Download' }));
  expect(fetcher).toHaveBeenCalledWith(
    '/api/requests/7/download',
    expect.objectContaining({ body: JSON.stringify({ key: '1:g' }) }),
  );
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(close).toHaveBeenCalled();
});

it('reuses the eBook request, creates the audiobook request once, and ignores late eBook results', async () => {
  const user = userEvent.setup({ delay: null });
  const book = { id: 'book', title: 'Dune', author: 'Frank Herbert' };
  const ebook = { id: 7, book_id: book.id, format: 'epub' };
  const audio = { id: 8, book_id: book.id, format: 'm4b' };
  const requests = [ebook];
  let finishEbook!: (response: Response) => void;
  const lateEbook = new Promise<Response>(resolve => { finishEbook = resolve; });
  let ebookFetches = 0;
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/requests' && init?.method === 'POST') {
      expect(JSON.parse(String(init.body))).toEqual({
        book_id: book.id, title: book.title, author: book.author, format: 'm4b', manual_only: true,
      });
      requests.push(audio);
      return Response.json(audio, { status: 201 });
    }
    if (url === '/api/requests') return Response.json(requests);
    if (url === '/api/requests/7/candidates') {
      return ++ebookFetches === 1 ? lateEbook : Response.json([candidate]);
    }
    if (url === '/api/requests/8/candidates') return Response.json([{
      ...candidate, format: 'm4b', release: { ...candidate.release, title: 'Dune M4B' },
    }]);
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetcher);
  render(<ManualSearch book={book} onClose={vi.fn()} />);
  expect(screen.getByRole('tablist', { name: 'Download format' })).toBeInTheDocument();
  const ebookTab = screen.getByRole('tab', { name: 'eBook', selected: true });
  const audioTab = screen.getByRole('tab', { name: 'Audiobook', selected: false });
  await waitFor(() => expect(ebookFetches).toBe(1));
  ebookTab.focus();
  await user.keyboard('{ArrowRight}');
  expect(audioTab).toHaveFocus();
  expect(audioTab).toHaveAttribute('aria-selected', 'true');
  expect(await screen.findByText('Dune M4B')).toBeInTheDocument();
  await act(async () => finishEbook(Response.json([candidate])));
  expect(screen.queryByText('Dune EPUB')).not.toBeInTheDocument();
  await user.click(ebookTab);
  expect(await screen.findByText('Dune EPUB')).toBeInTheDocument();
  expect(screen.queryByText('Dune M4B')).not.toBeInTheDocument();
  await user.click(audioTab);
  expect(await screen.findByText('Dune M4B')).toBeInTheDocument();
  expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
});

it('omits format score when format is unknown and avoids leading punctuation on empty breakdown', async () => {
  const unknown = {
    key: '1:u',
    release: { title: 'Dune', size: 1048576, seeders: 0 },
    format: '',
    score: 0,
    penalty: 0,
    breakdown: {},
  };
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => [unknown] }));
  render(<ManualSearch requestId="9" onClose={vi.fn()} />);
  expect(await screen.findByText('Unknown')).toBeInTheDocument();
  expect(screen.getByText('failure: 0')).toBeInTheDocument();
  expect(screen.queryByText(/format:/)).not.toBeInTheDocument();
});

it('updates the candidate list and shows a meaningful error when a download expires and cannot be found after refresh', async () => {
  const freshCandidate = {
    key: '1:fresh',
    release: { title: 'Dune EPUB Fresh', size: 2097152, seeders: 90 },
    format: 'epub',
    score: 50,
    penalty: 0,
    breakdown: { format: 30, seeders: 20 },
  };
  const fetcher = vi.fn(async (url: string) => {
    if (url === '/api/requests/7/candidates') {
      return Response.json([candidate]);
    }
    if (url === '/api/requests/7/download') {
      return Response.json(
        {
          error: 'Could not find the requested download after refreshing search results; please try again from the updated list',
          candidates: [freshCandidate],
        },
        { status: 404 },
      );
    }
    throw new Error(`Unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetcher);
  const user = userEvent.setup({ delay: null });

  render(<ManualSearch requestId="7" onClose={vi.fn()} />);
  expect(await screen.findByText('Dune EPUB')).toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'Download' }));

  expect(await screen.findByRole('alert')).toHaveTextContent(
    /could not find the requested download/i,
  );
  expect(screen.getByText('Dune EPUB Fresh')).toBeInTheDocument();
  expect(screen.queryByText('Dune EPUB')).not.toBeInTheDocument();
});

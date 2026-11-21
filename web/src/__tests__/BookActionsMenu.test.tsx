import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { testBooks } from './testFixtures';
import type { Book } from '../types';

const downloadedBook: Book = {
  ...testBooks[0],
  id: 'book-downloaded-1',
  editions: [
    {
      id: 'ed-epub',
      bookId: 'book-downloaded-1',
      format: 'EPUB',
      language: 'en',
      assets: [
        {
          id: 'asset-epub',
          editionId: 'ed-epub',
          filename: 'The Obsidian Path (2023) - Alden Thorne.epub',
          format: 'EPUB',
          fileSizeBytes: 512000,
        },
      ],
    },
  ],
};

const audioOnlyBook: Book = {
  ...downloadedBook,
  id: 'book-audio-1',
  formats: ['M4B'],
  editions: [
    {
      id: 'ed-m4b',
      bookId: 'book-audio-1',
      format: 'M4B',
      language: 'en',
      assets: [
        {
          id: 'asset-m4b',
          editionId: 'ed-m4b',
          filename: 'The Obsidian Path (2023) - Alden Thorne.m4b',
          format: 'M4B',
          fileSizeBytes: 1024 * 1024 * 250,
        },
      ],
    },
  ],
};

const noFilesBook: Book = {
  ...testBooks[0],
  id: 'book-empty-1',
  state: 'Not in library',
  monitored: false,
  formats: [],
  editions: [],
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('BookDetailModal actions overflow menu', () => {
  it('hides the overflow menu completely when the user has no available actions', () => {
    const { rerender } = render(
      <BookDetailModal book={downloadedBook} isAdmin={false} canManage={false} canRequest={false} onClose={vi.fn()} />
    );
    expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();

    // Requester on a book with no downloaded files gets inline Show Download Options and zero overflow items
    rerender(
      <BookDetailModal book={noFilesBook} isAdmin={false} canManage={false} canRequest={true} onClose={vi.fn()} />
    );
    expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show Download Options' })).toBeVisible();

    // Unpersisted book even for admins/curators has zero overflow items
    rerender(
      <BookDetailModal book={{ ...noFilesBook, id: '' }} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );
    expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();
  });

  it('enforces RBAC for menu items across requester, curator, and admin roles', async () => {
    const user = userEvent.setup({ delay: null });

    // Requester on book WITH downloaded files: only Show Download Options inside overflow menu
    const { rerender } = render(
      <BookDetailModal book={downloadedBook} isAdmin={false} canManage={false} canRequest={true} onClose={vi.fn()} />
    );
    expect(screen.queryByRole('button', { name: 'Show Download Options' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    let menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(within(menu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
      'Show Download Options',
    ]);
    expect(within(menu).queryByRole('separator')).not.toBeInTheDocument();
    await user.keyboard('{Escape}');

    // Curator on book WITH downloaded files: Refresh Metadata + Show Download Options (no Fix Metadata, no Delete)
    rerender(
      <BookDetailModal book={downloadedBook} isAdmin={false} canManage={true} canRequest={true} onClose={vi.fn()} />
    );
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(within(menu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
      'Refresh Metadata',
      'Show Download Options',
    ]);
    expect(within(menu).queryByRole('separator')).not.toBeInTheDocument();
    await user.keyboard('{Escape}');

    // Admin on EPUB book: Refresh Metadata, Fix Metadata, Show Download Options, separator, Delete Book
    rerender(
      <BookDetailModal book={downloadedBook} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(within(menu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
      'Refresh Metadata',
      'Fix Metadata',
      'Show Download Options',
      'Delete Book',
    ]);
    expect(within(menu).getByRole('separator')).toBeInTheDocument();
    await user.keyboard('{Escape}');

    // Admin on M4B-only book: Fix Metadata omitted
    rerender(
      <BookDetailModal book={audioOnlyBook} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(within(menu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
      'Refresh Metadata',
      'Show Download Options',
      'Delete Book',
    ]);
  });

  it('keeps Show Download Options to the right of Available files when no files are downloaded', async () => {
    const user = userEvent.setup({ delay: null });
    render(
      <BookDetailModal book={noFilesBook} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );

    const titleHeading = screen.getByRole('heading', { level: 2, name: noFilesBook.title });
    const filesHeading = screen.getByRole('heading', { name: 'Available files' });
    const downloadButton = screen.getByRole('button', { name: 'Show Download Options' });

    // Positioned after the modal title header and directly alongside Available files (not at top of window)
    expect(titleHeading.compareDocumentPosition(downloadButton)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(filesHeading.compareDocumentPosition(downloadButton)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(filesHeading.parentElement).toContainElement(downloadButton);

    // Not duplicated inside the overflow menu when no files are downloaded
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    const menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(within(menu).queryByRole('menuitem', { name: 'Show Download Options' })).not.toBeInTheDocument();
    expect(within(menu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
      'Refresh Metadata',
      'Delete Book',
    ]);
  });

  it('supports ARIA attributes, keyboard navigation, Escape, and outside-click dismissal', async () => {
    const user = userEvent.setup({ delay: null });
    const onClose = vi.fn();
    render(
      <BookDetailModal book={downloadedBook} isAdmin={true} canManage={true} canRequest={true} onClose={onClose} />
    );

    const trigger = screen.getByRole('button', { name: 'More actions' });
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).not.toHaveAttribute('aria-controls');

    // ArrowDown opens menu and focuses first item
    trigger.focus();
    await user.keyboard('{ArrowDown}');
    const menu = screen.getByRole('menu', { name: 'Book actions' });
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(trigger).toHaveAttribute('aria-controls', menu.id);

    const items = within(menu).getAllByRole('menuitem');
    expect(items[0]).toHaveFocus();

    // ArrowDown / ArrowUp / Home / End navigation with wrap-around
    await user.keyboard('{ArrowDown}');
    expect(items[1]).toHaveFocus();
    await user.keyboard('{End}');
    expect(items[3]).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(items[0]).toHaveFocus();
    await user.keyboard('{ArrowUp}');
    expect(items[3]).toHaveFocus();
    await user.keyboard('{Home}');
    expect(items[0]).toHaveFocus();

    // Escape closes ONLY the menu (not the modal) and returns focus to trigger
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveFocus();
    expect(onClose).not.toHaveBeenCalled();

    // ArrowUp on trigger opens menu and focuses last item
    await user.keyboard('{ArrowUp}');
    const reopenedItems = within(screen.getByRole('menu')).getAllByRole('menuitem');
    expect(reopenedItems[reopenedItems.length - 1]).toHaveFocus();

    // Outside click inside modal dismisses the menu without closing the modal
    await user.click(screen.getByRole('heading', { name: 'Available files' }));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('triggers Download Options from overflow menu when files exist and preserves cover hover actions', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/requests') {
        return Response.json([{ id: 9, book_id: downloadedBook.id, format: 'epub' }]);
      }
      if (url === '/api/requests/9/candidates') {
        return Response.json([]);
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <BookDetailModal book={downloadedBook} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );

    // Cover hover controls stay on the cover
    expect(screen.getByRole('button', { name: 'Upload cover' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Find cover' })).toBeInTheDocument();

    // Trigger Download Options from overflow menu
    await user.click(screen.getByRole('button', { name: 'More actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Show Download Options' }));

    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(await screen.findByRole('dialog', { name: 'Download Options' })).toBeInTheDocument();
  });

  it('restores focus to More actions after backing out of Fix Metadata', async () => {
    const user = userEvent.setup({ delay: null });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.endsWith('/metadata-candidates')) return Response.json([]);
        if (url.endsWith('/metadata-diff')) return Response.json({ preview: 'p', files: [] });
        throw new Error(`Unexpected fetch ${url}`);
      })
    );

    render(<BookDetailModal book={downloadedBook} isAdmin={true} onClose={vi.fn()} />);

    await user.click(screen.getByRole('button', { name: 'More actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Fix Metadata' }));

    expect(await screen.findByRole('heading', { name: 'Fix Metadata' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'More actions' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /Back/i }));
    const trigger = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('skips disabled menu items during keyboard navigation, closes on Tab, and toggles on trigger click', async () => {
    const user = userEvent.setup({ delay: null });
    let resolveRefresh!: (res: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>((resolve) => { resolveRefresh = resolve; }))
    );

    render(
      <BookDetailModal book={downloadedBook} isAdmin={true} canManage={true} canRequest={true} onClose={vi.fn()} />
    );

    const trigger = screen.getByRole('button', { name: 'More actions' });
    await user.click(trigger);
    expect(screen.getByRole('menu')).toBeInTheDocument();

    // Clicking trigger again toggles the menu closed
    await user.click(trigger);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();

    // Pressing Tab closes the menu
    await user.click(trigger);
    expect(screen.getByRole('menu')).toBeInTheDocument();
    await user.keyboard('{Tab}');
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();

    // Start Refresh Metadata so non-destructive items become disabled while refreshing
    await user.click(trigger);
    await user.click(screen.getByRole('menuitem', { name: 'Refresh Metadata' }));
    expect(screen.getByRole('status')).toHaveTextContent('Refreshing metadata…');

    // Reopen menu: disabled items are skipped and Delete Book (only enabled item) gets focus
    await user.click(trigger);
    expect(screen.getByRole('menuitem', { name: 'Refreshing…' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Fix Metadata' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Show Download Options' })).toBeDisabled();
    const deleteItem = screen.getByRole('menuitem', { name: 'Delete Book' });
    expect(deleteItem).toBeEnabled();
    expect(deleteItem).toHaveFocus();

    await user.keyboard('{ArrowDown}');
    expect(deleteItem).toHaveFocus();

    resolveRefresh(Response.json(downloadedBook));
    expect(await screen.findByRole('status')).toHaveTextContent('Metadata refreshed.');
  });
});

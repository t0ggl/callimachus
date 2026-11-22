import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { CommandPalette } from '../components/CommandPalette';
import { App } from '../App';
import { testBooks, testAuthors, testSeries } from './testFixtures';

afterEach(() => vi.unstubAllGlobals());

describe('CommandPalette Component', () => {
  it('renders nothing when isOpen is false', () => {
    const { container } = render(
      <CommandPalette
        isOpen={false}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders modal and autofocuses input when isOpen is true', async () => {
    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    expect(input).toBeInTheDocument();
    await waitFor(() => {
      expect(input).toHaveFocus();
    });
  });

  it('filters books and authors accurately based on query', async () => {
    const user = userEvent.setup({ delay: null });
    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    await user.type(input, 'Obsidian');

    // Should match Obsidian Path book and The Obsidian Archive series
    expect(screen.getByText('The Obsidian Path')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'The Obsidian Archive' })).toBeInTheDocument();
  });

  it('calls onSelectBook when a book is selected', async () => {
    const onSelectBookMock = vi.fn();
    const onCloseMock = vi.fn();
    const user = userEvent.setup({ delay: null });

    render(
      <CommandPalette
        isOpen={true}
        onClose={onCloseMock}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={onSelectBookMock}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    await user.type(input, 'Obsidian');

    const bookItem = screen.getByTestId('palette-item-book_book_1');
    await user.click(bookItem);

    expect(onSelectBookMock).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'book_1', title: 'The Obsidian Path' })
    );
    expect(onCloseMock).toHaveBeenCalled();
  });

  it('closes when Escape key is pressed', async () => {
    const onCloseMock = vi.fn();
    render(
      <CommandPalette
        isOpen={true}
        onClose={onCloseMock}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCloseMock).toHaveBeenCalled();
  });

  it('hides Settings navigation entry when canAccessSettings is false or omitted', () => {
    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );

    expect(screen.getByTestId('palette-item-nav_catalog')).toBeInTheDocument();
    expect(screen.getByTestId('palette-item-nav_activity')).toBeInTheDocument();
    expect(screen.queryByTestId('palette-item-nav_settings')).not.toBeInTheDocument();
  });

  it('shows Settings navigation entry when canAccessSettings is true', async () => {
    const onNavigate = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        canAccessSettings={true}
        onNavigate={onNavigate}
        onSelectBook={vi.fn()}
      />
    );

    const settingsItem = screen.getByTestId('palette-item-nav_settings');
    expect(settingsItem).toBeInTheDocument();
    await user.click(settingsItem);
    expect(onNavigate).toHaveBeenCalledWith('settings');
  });

  it('provides accessible Close and Clear search controls in the header', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup({ delay: null });

    render(
      <CommandPalette
        isOpen={true}
        onClose={onClose}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={vi.fn()}
        onSelectBook={vi.fn()}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument();

    await user.type(input, 'Obsidian');
    const clearBtn = screen.getByRole('button', { name: 'Clear search' });
    await user.click(clearBtn);
    expect(input).toHaveValue('');
    expect(input).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument();

    const closeBtn = screen.getByRole('button', { name: 'Close search' });
    await user.click(closeBtn);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('puts matching library books and commands before Discovery and keeps them keyboard-selectable', async () => {
    const onNavigate = vi.fn();
    const onSelectBook = vi.fn();
    const localBook = { ...testBooks[0], title: 'The Library at Mount Char' };
    const onSubmitRequest = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup({ delay: null });

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          { title: 'The Midnight Library', authors: ['Matt Haig'], year: 2020 },
        ],
      }),
    );

    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={[localBook]}
        authors={testAuthors}
        series={testSeries}
        onNavigate={onNavigate}
        onSelectBook={onSelectBook}
        onSubmitRequest={onSubmitRequest}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    await user.type(input, 'Library');

    const addButton = await screen.findByRole('button', { name: 'Add The Midnight Library' });
    for (const item of [screen.getByText(localBook.title), screen.getByText('Library')]) {
      expect(item.compareDocumentPosition(addButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }

    await user.keyboard('{ArrowDown}{Enter}');
    expect(onSelectBook).toHaveBeenCalledWith(localBook);

    await user.click(addButton);
    expect(onSubmitRequest).toHaveBeenCalledWith('The Midnight Library', 'Matt Haig', 'ebook', 2020, expect.any(Object));

    await user.clear(input);
    input.focus();
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onNavigate).toHaveBeenCalledWith('activity');
  });

  it('shows the first 5 matching library books with a control to view all, independent of download search', async () => {
    const user = userEvent.setup({ delay: null });
    const onSelectBook = vi.fn();
    const prolificBooks = Array.from({ length: 8 }, (_, index) => ({
      ...testBooks[0],
      id: `prolific_${index + 1}`,
      title: `Cosmere Novel ${index + 1}`,
      authorId: 'auth_prolific',
      authorName: 'Brandon Sanderson',
      seriesId: undefined,
      seriesTitle: undefined,
    }));
    const prolificAuthors = [
      { id: 'auth_prolific', name: 'Brandon Sanderson', sortName: 'Sanderson, Brandon', bookCount: 8 },
    ];
    const discoveryMatches = Array.from({ length: 16 }, (_, index) => ({
      title: `Discover Sanderson ${index + 1}`,
      authors: ['Brandon Sanderson'],
      year: 2010 + index,
    }));

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          results: discoveryMatches,
          page: 1,
          hasMore: false,
        }),
      }),
    );

    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={prolificBooks}
        authors={prolificAuthors}
        series={[]}
        onNavigate={vi.fn()}
        onSelectBook={onSelectBook}
        onSubmitRequest={vi.fn().mockResolvedValue(undefined)}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    await user.type(input, 'Brandon Sanderson');

    // Only the first 5 library matches appear initially (8 books + 1 author = 9 total -> 4 hidden)
    expect(screen.getByTestId('palette-item-book_prolific_5')).toBeInTheDocument();
    expect(screen.queryByTestId('palette-item-book_prolific_6')).not.toBeInTheDocument();
    expect(screen.queryByTestId('palette-item-auth_auth_prolific')).not.toBeInTheDocument();

    // Download search section renders separately with its first 5 results and View more control
    expect(await screen.findByRole('heading', { name: 'Discover Sanderson 5' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Discover Sanderson 6' })).not.toBeInTheDocument();
    const viewMoreDownload = screen.getByRole('button', { name: 'View more' });

    // Expanding library results does not alter download search pagination
    const showAllLibrary = screen.getByRole('button', { name: 'Show all library results (4 more)' });
    await user.click(showAllLibrary);

    expect(screen.getByTestId('palette-item-book_prolific_6')).toHaveFocus();
    expect(screen.getByTestId('palette-item-book_prolific_8')).toBeInTheDocument();
    expect(screen.getByTestId('palette-item-auth_auth_prolific')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Show all library results/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Discover Sanderson 6' })).not.toBeInTheDocument();

    // Pressing View more in download search reveals the next 10 (5 -> 15) without touching library results
    await user.click(viewMoreDownload);
    expect(screen.getByRole('heading', { name: 'Discover Sanderson 15' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Discover Sanderson 16' })).not.toBeInTheDocument();

    // Changing the query resets library expansion back to 5
    await user.clear(input);
    await user.type(input, 'Cosmere Novel');
    expect(screen.getByTestId('palette-item-book_prolific_5')).toBeInTheDocument();
    expect(screen.queryByTestId('palette-item-book_prolific_6')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show all library results (3 more)' })).toBeInTheDocument();

    // Keyboard navigation wraps across visible items only before expanding
    await user.keyboard('{ArrowUp}{Enter}');
    expect(onSelectBook).toHaveBeenCalledWith(prolificBooks[4]);
  });
});

describe('Global Palette Triggers in App', () => {
  it('opens palette when Ctrl+K is pressed', async () => {
    render(<App />);

    expect(screen.queryByTestId('command-palette-input')).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });

    await waitFor(() => {
      expect(screen.getByTestId('command-palette-input')).toBeInTheDocument();
    });
  });

  it('opens palette when / is pressed outside of input', async () => {
    render(<App />);

    expect(screen.queryByTestId('command-palette-input')).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: '/' });

    await waitFor(() => {
      expect(screen.getByTestId('command-palette-input')).toBeInTheDocument();
    });
  });
});

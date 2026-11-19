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

  it('supports keyboard navigation across palette items and renders DiscoveryResults when searching', async () => {
    const onNavigate = vi.fn();
    const onSubmitRequest = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup({ delay: null });

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => [
          { title: 'Hyperion', authors: ['Dan Simmons'], year: 1989 },
        ],
      }),
    );

    render(
      <CommandPalette
        isOpen={true}
        onClose={vi.fn()}
        books={testBooks}
        authors={testAuthors}
        series={testSeries}
        onNavigate={onNavigate}
        onSelectBook={vi.fn()}
        onSubmitRequest={onSubmitRequest}
      />
    );

    const input = screen.getByTestId('command-palette-input');
    await user.type(input, 'Hyperion');

    expect(await screen.findByRole('button', { name: 'Add Hyperion' })).toBeInTheDocument();
    expect(screen.getByText('Library & Commands')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Add Hyperion' }));
    expect(onSubmitRequest).toHaveBeenCalledWith('Hyperion', 'Dan Simmons', 'ebook', 1989, expect.any(Object));

    await user.clear(input);
    input.focus();
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onNavigate).toHaveBeenCalledWith('activity');
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

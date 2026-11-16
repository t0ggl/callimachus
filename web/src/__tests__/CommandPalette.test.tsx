import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { CommandPalette } from '../components/CommandPalette';
import { App } from '../App';
import { testBooks, testAuthors, testSeries } from './testFixtures';

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
    expect(screen.getByText('The Obsidian Archive')).toBeInTheDocument();
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

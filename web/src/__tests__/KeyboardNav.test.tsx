import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { useKeyboardNav } from '../hooks/useKeyboardNav';

const TestVimList: React.FC<{ itemsCount: number; onSelect?: (idx: number) => void }> = ({
  itemsCount,
  onSelect,
}) => {
  const { selectedIndex } = useKeyboardNav({
    itemsCount,
    onSelect,
  });

  return (
    <div>
      <input data-testid="search-input" placeholder="Type here..." />
      <ul>
        {Array.from({ length: itemsCount }).map((_, i) => (
          <li
            key={i}
            data-testid={`item-${i}`}
            data-selected={selectedIndex === i}
            className={selectedIndex === i ? 'selected' : ''}
          >
            Item {i} {selectedIndex === i ? '(ACTIVE)' : ''}
          </li>
        ))}
      </ul>
    </div>
  );
};

describe('Vim Keyboard Navigation (useKeyboardNav)', () => {
  it.each([
    ['j', 'k'],
    ['ArrowDown', 'ArrowUp'],
  ])('starts clear and navigates with %s and %s', (down, up) => {
    const onSelect = vi.fn();
    render(<TestVimList itemsCount={3} onSelect={onSelect} />);
    expect(screen.queryByText(/ACTIVE/)).not.toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: down });
    expect(screen.getByTestId('item-0')).toHaveClass('selected');
    fireEvent.keyDown(window, { key: up });
    expect(screen.getByTestId('item-2')).toHaveClass('selected');
    fireEvent.keyDown(window, { key: down });
    expect(screen.getByTestId('item-0')).toHaveClass('selected');
    fireEvent.keyDown(window, { key: down });
    expect(screen.getByTestId('item-1')).toHaveClass('selected');
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledExactlyOnceWith(1);

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByText(/ACTIVE/)).not.toBeInTheDocument();
    fireEvent.keyDown(window, { key: up });
    expect(screen.getByTestId('item-2')).toHaveClass('selected');
  });

  it('clears selection when the list empties', () => {
    const { rerender } = render(<TestVimList itemsCount={2} />);
    fireEvent.keyDown(window, { key: 'j' });
    rerender(<TestVimList itemsCount={0} />);
    rerender(<TestVimList itemsCount={2} />);
    expect(screen.queryByText(/ACTIVE/)).not.toBeInTheDocument();
  });

  it('does NOT hijack typing in an input element', async () => {
    const user = userEvent.setup({ delay: null });
    render(<TestVimList itemsCount={5} />);

    const input = screen.getByTestId('search-input');
    await user.click(input);
    await user.type(input, 'jkjk');

    // Input should contain 'jkjk'
    expect(input).toHaveValue('jkjk');

    expect(screen.queryByText(/ACTIVE/)).not.toBeInTheDocument();
  });
});

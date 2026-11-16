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
  it('navigates down and up using j and k keys', () => {
    render(<TestVimList itemsCount={5} />);

    // Initially 0 is selected
    expect(screen.getByTestId('item-0')).toHaveAttribute('data-selected', 'true');

    // Press 'j' -> index 1
    fireEvent.keyDown(window, { key: 'j' });
    expect(screen.getByTestId('item-1')).toHaveAttribute('data-selected', 'true');

    // Press 'j' again -> index 2
    fireEvent.keyDown(window, { key: 'j' });
    expect(screen.getByTestId('item-2')).toHaveAttribute('data-selected', 'true');

    // Press 'k' -> back to index 1
    fireEvent.keyDown(window, { key: 'k' });
    expect(screen.getByTestId('item-1')).toHaveAttribute('data-selected', 'true');
  });

  it('wraps around circularly when pressing j past the end and k before start', () => {
    render(<TestVimList itemsCount={3} />);

    // Initially at 0
    expect(screen.getByTestId('item-0')).toHaveAttribute('data-selected', 'true');

    // Press 'k' -> wraps to last index (2)
    fireEvent.keyDown(window, { key: 'k' });
    expect(screen.getByTestId('item-2')).toHaveAttribute('data-selected', 'true');

    // Press 'j' -> wraps to 0
    fireEvent.keyDown(window, { key: 'j' });
    expect(screen.getByTestId('item-0')).toHaveAttribute('data-selected', 'true');
  });

  it('triggers onSelect when Enter is pressed on active item', () => {
    const onSelectMock = vi.fn();
    render(<TestVimList itemsCount={4} onSelect={onSelectMock} />);

    // Navigate to index 2
    fireEvent.keyDown(window, { key: 'j' });
    fireEvent.keyDown(window, { key: 'j' });

    // Press Enter
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(onSelectMock).toHaveBeenCalledWith(2);
  });

  it('does NOT hijack typing in an input element', async () => {
    const user = userEvent.setup();
    render(<TestVimList itemsCount={5} />);

    const input = screen.getByTestId('search-input');
    await user.click(input);
    await user.type(input, 'jkjk');

    // Input should contain 'jkjk'
    expect(input).toHaveValue('jkjk');

    // Selection should remain at index 0 without being moved by typing 'j' and 'k' in input
    expect(screen.getByTestId('item-0')).toHaveAttribute('data-selected', 'true');
  });
});

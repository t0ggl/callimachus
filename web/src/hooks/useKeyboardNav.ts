import { useEffect, useState } from 'react';

interface UseKeyboardNavOptions {
  itemsCount: number;
  onSelect?: (index: number) => void;
  onEscape?: () => void;
  enabled?: boolean;
  initialIndex?: number;
}

export function useKeyboardNav({
  itemsCount,
  onSelect,
  onEscape,
  enabled = true,
  initialIndex = 0,
}: UseKeyboardNavOptions) {
  const [selectedIndex, setSelectedIndex] = useState<number>(initialIndex);

  // Keep index within bounds if itemsCount changes
  useEffect(() => {
    if (itemsCount === 0) {
      setSelectedIndex(0);
    } else if (selectedIndex >= itemsCount) {
      setSelectedIndex(itemsCount - 1);
    }
  }, [itemsCount, selectedIndex]);

  useEffect(() => {
    if (!enabled) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      // Do not hijack typing in text inputs or editable elements
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable)
      ) {
        if (e.key === 'Escape' && onEscape) {
          onEscape();
        }
        return;
      }

      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => (itemsCount > 0 ? (prev + 1) % itemsCount : 0));
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => (itemsCount > 0 ? (prev - 1 + itemsCount) % itemsCount : 0));
      } else if (e.key === 'Enter') {
        if (itemsCount > 0 && onSelect) {
          e.preventDefault();
          onSelect(selectedIndex);
        }
      } else if (e.key === 'Escape') {
        if (onEscape) {
          e.preventDefault();
          onEscape();
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [enabled, itemsCount, onSelect, onEscape, selectedIndex]);

  return {
    selectedIndex,
    setSelectedIndex,
  };
}

import { useEffect, useState } from 'react';

interface UseKeyboardNavOptions {
  itemsCount: number;
  onSelect?: (index: number) => void;
  enabled?: boolean;
}

export function useKeyboardNav({
  itemsCount,
  onSelect,
  enabled = true,
}: UseKeyboardNavOptions) {
  const [selectedIndex, setSelectedIndex] = useState(-1);

  // Keep index within bounds if itemsCount changes
  useEffect(() => {
    if (itemsCount === 0) {
      setSelectedIndex(-1);
    } else if (selectedIndex >= itemsCount) {
      setSelectedIndex(itemsCount - 1);
    }
  }, [itemsCount, selectedIndex]);

  useEffect(() => {
    if (!enabled) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;

      const target = e.target instanceof Element ? e.target : null;
      if (target?.closest('input, textarea, select') || (target instanceof HTMLElement && target.isContentEditable)) return;
      if (itemsCount === 0) return;

      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev + 1) % itemsCount);
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev <= 0 ? itemsCount - 1 : prev - 1));
      } else if (e.key === 'Enter') {
        if (target?.closest('button, a[href], summary, [role="button"], [role="link"]')) return;
        if (selectedIndex >= 0 && selectedIndex < itemsCount && onSelect) {
          e.preventDefault();
          onSelect(selectedIndex);
        }
      } else if (e.key === 'Escape') {
        setSelectedIndex(-1);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [enabled, itemsCount, onSelect, selectedIndex]);

  return {
    selectedIndex,
    setSelectedIndex,
  };
}

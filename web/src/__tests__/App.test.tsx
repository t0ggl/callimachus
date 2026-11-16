import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect } from 'vitest';
import { App } from '../App';

describe('App Integration', () => {
  it('renders Callimachus brand and default catalog view', () => {
    render(<App />);

    expect(screen.getByText('Callimachus')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Library & Catalog/i })).toBeInTheDocument();
    expect(screen.getByText('The Obsidian Path')).toBeInTheDocument();
  });

  it('navigates between views when clicking sidebar navigation', async () => {
    const user = userEvent.setup();
    render(<App />);

    // Navigate to Activity view
    const activityNav = screen.getByTestId('nav-activity');
    await user.click(activityNav);

    expect(screen.getByRole('heading', { name: /Activity & Queues/i })).toBeInTheDocument();
    expect(screen.getByText('Transcode Queue (3)')).toBeInTheDocument();

    // Navigate to Requests view
    const requestsNav = screen.getByTestId('nav-requests');
    await user.click(requestsNav);

    expect(screen.getByRole('heading', { name: /Book Request Portal/i })).toBeInTheDocument();
    expect(screen.getByText('Submit a New Request')).toBeInTheDocument();

    // Navigate to Settings view
    const settingsNav = screen.getByTestId('nav-settings');
    await user.click(settingsNav);

    expect(screen.getByRole('heading', { name: /System Settings/i })).toBeInTheDocument();
    expect(screen.getByText('Indexers & Prowlarr')).toBeInTheDocument();
  });

  it('opens and closes BookDetailModal when selecting a book', async () => {
    const user = userEvent.setup();
    render(<App />);

    // Click on book card
    const bookCard = screen.getByTestId('book-card-book_1');
    await user.click(bookCard);

    // Modal should be visible with metadata and file asset info
    await waitFor(() => {
      expect(screen.getByRole('dialog', { name: /Details for The Obsidian Path/i })).toBeInTheDocument();
    });
    expect(screen.getByText('Apex Horizon Press')).toBeInTheDocument();
    expect(screen.getByText(/Jonathan Bell/)).toBeInTheDocument();

    // Close modal via close button
    const closeBtn = screen.getByTestId('close-book-detail');
    await user.click(closeBtn);

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: /Details for The Obsidian Path/i })).not.toBeInTheDocument();
    });
  });

  it('switches between grid and list view in catalog', async () => {
    const user = userEvent.setup();
    render(<App />);

    // Default is grid view
    expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();

    // Switch to list view
    const listBtn = screen.getByTestId('view-list-btn');
    await user.click(listBtn);

    // Now table rows are present
    expect(screen.getByTestId('book-row-book_1')).toBeInTheDocument();

    // Switch back to grid view
    const gridBtn = screen.getByTestId('view-grid-btn');
    await user.click(gridBtn);
    expect(screen.getByTestId('book-card-book_1')).toBeInTheDocument();
  });
});

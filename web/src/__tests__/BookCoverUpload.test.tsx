import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BookDetailModal } from '../components/BookDetailModal';
import { testBooks } from './testFixtures';
import type { Book } from '../types';

const ownedBook: Book = {
  ...testBooks[0],
  id: 'book-1',
  coverUrl: '/api/books/book-1/cover',
  editions: [
    {
      id: 'ed-1',
      bookId: 'book-1',
      format: 'EPUB',
      language: 'en',
      assets: [
        { id: 'a1', editionId: 'ed-1', filename: 'Book.epub', format: 'EPUB', fileSizeBytes: 2048 },
      ],
    },
  ],
};

function stubImageBitmap(width: number, height: number) {
  const close = vi.fn();
  vi.stubGlobal(
    'createImageBitmap',
    vi.fn(async () => ({ width, height, close }))
  );
  return close;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('BookDetailModal cover upload', () => {
  it('shows upload cover action only to curators/admins on owned books with files', () => {
    const { rerender } = render(<BookDetailModal book={ownedBook} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: /upload cover/i })).toBeNull();

    // Unowned / no assets book hides upload even for admins
    rerender(
      <BookDetailModal
        book={{ ...ownedBook, editions: [{ ...ownedBook.editions[0], assets: [] }] }}
        canManage
        isAdmin
        onClose={vi.fn()}
      />
    );
    expect(screen.queryByRole('button', { name: /upload cover/i })).toBeNull();

    // Curator (canManage) sees it with hover, keyboard focus-within, and touch affordance classes
    rerender(<BookDetailModal book={ownedBook} canManage onClose={vi.fn()} />);
    const btn = screen.getByRole('button', { name: /upload cover/i });
    expect(btn).toBeInTheDocument();
    const overlay = btn.parentElement!;
    expect(overlay.className).toContain('group-hover:opacity-100');
    expect(overlay.className).toContain('group-focus-within:opacity-100');
    expect(overlay.className).toContain('[@media(hover:none)]:opacity-100');
    expect(overlay.className).toContain('[@media(pointer:coarse)]:opacity-100');

    // Admin (isAdmin) also sees it
    rerender(<BookDetailModal book={ownedBook} isAdmin onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /upload cover/i })).toBeInTheDocument();
  });

  it('activates file picker via keyboard on the Upload Cover button', async () => {
    const user = userEvent.setup({ delay: null });
    render(<BookDetailModal book={ownedBook} canManage onClose={vi.fn()} />);
    const input = screen.getByTestId('cover-upload-input') as HTMLInputElement;
    const clickSpy = vi.spyOn(input, 'click').mockImplementation(() => {});

    const btn = screen.getByRole('button', { name: /upload cover/i });
    btn.focus();
    expect(btn).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(clickSpy).toHaveBeenCalledTimes(1);
  });

  it('validates file size, type, dimensions, and aspect ratio client-side before uploading', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<BookDetailModal book={ownedBook} canManage onClose={vi.fn()} />);
    const input = screen.getByTestId('cover-upload-input') as HTMLInputElement;

    // Empty file
    const emptyFile = new File([], 'empty.jpg', { type: 'image/jpeg' });
    fireEvent.change(input, { target: { files: [emptyFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('Cover image file is empty.');

    // Unsupported type
    const gifFile = new File([new Uint8Array([71, 73, 70])], 'anim.gif', { type: 'image/gif' });
    fireEvent.change(input, { target: { files: [gifFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('JPEG, PNG, or WebP');

    // Too large (> 10 MiB)
    const bigFile = new File([new Uint8Array(10 * 1024 * 1024 + 1)], 'huge.jpg', { type: 'image/jpeg' });
    fireEvent.change(input, { target: { files: [bigFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('10 MB or smaller');

    // Dimensions too small
    stubImageBitmap(120, 180);
    const smallFile = new File([new Uint8Array([1, 2, 3])], 'small.jpg', { type: 'image/jpeg' });
    fireEvent.change(input, { target: { files: [smallFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('too small (120×180 px)');

    // Dimensions too large
    stubImageBitmap(5000, 6000);
    const giantFile = new File([new Uint8Array([1, 2, 3])], 'giant.jpg', { type: 'image/jpeg' });
    fireEvent.change(input, { target: { files: [giantFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('too large (5000×6000 px)');

    // Aspect ratio outside book-cover range
    stubImageBitmap(1200, 400);
    const wideFile = new File([new Uint8Array([1, 2, 3])], 'banner.jpg', { type: 'image/jpeg' });
    fireEvent.change(input, { target: { files: [wideFile] } });
    expect(await screen.findByRole('alert')).toHaveTextContent('aspect ratio (3.00:1)');

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('uploads a valid cover, updates the displayed image immediately, and notifies onBookUpdated', async () => {
    const closeBitmap = stubImageBitmap(600, 900);
    const onBookUpdated = vi.fn();
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      expect(url).toBe('/api/books/book-1/cover');
      expect(init?.method).toBe('POST');
      expect(init?.body).toBeInstanceOf(FormData);
      const fd = init?.body as FormData;
      expect(fd.get('cover')).toBeInstanceOf(File);
      return Response.json({
        coverUrl: '/api/books/book-1/cover?v=abc123def456',
        width: 600,
        height: 900,
        epubUpdated: 1,
      });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<BookDetailModal book={ownedBook} canManage onBookUpdated={onBookUpdated} onClose={vi.fn()} />);
    const input = screen.getByTestId('cover-upload-input') as HTMLInputElement;
    const validFile = new File([new Uint8Array([0xff, 0xd8, 0xff])], 'new-cover.jpg', { type: 'image/jpeg' });

    fireEvent.change(input, { target: { files: [validFile] } });

    await waitFor(() => {
      expect(screen.getByRole('img', { name: ownedBook.title })).toHaveAttribute(
        'src',
        '/api/books/book-1/cover?v=abc123def456'
      );
    });
    expect(screen.getByRole('status')).toHaveTextContent('Cover updated and embedded in EPUB.');
    expect(onBookUpdated).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'book-1', coverUrl: '/api/books/book-1/cover?v=abc123def456' })
    );
    expect(closeBitmap).toHaveBeenCalledOnce();
  });

  it('shows server error clearly and preserves the existing cover on failure', async () => {
    stubImageBitmap(600, 900);
    const onBookUpdated = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('Unable to embed cover into Book.epub. Existing files were not modified.', {
        status: 400,
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      }))
    );

    render(<BookDetailModal book={ownedBook} canManage onBookUpdated={onBookUpdated} onClose={vi.fn()} />);
    const input = screen.getByTestId('cover-upload-input') as HTMLInputElement;
    const file = new File([new Uint8Array([0xff, 0xd8, 0xff])], 'cover.jpg', { type: 'image/jpeg' });

    fireEvent.change(input, { target: { files: [file] } });

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Unable to embed cover into Book.epub. Existing files were not modified.'
    );
    expect(screen.getByRole('img', { name: ownedBook.title })).toHaveAttribute('src', '/api/books/book-1/cover');
    expect(onBookUpdated).not.toHaveBeenCalled();
  });
});

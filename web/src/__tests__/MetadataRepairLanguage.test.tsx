import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MetadataRepairPanel } from '../components/MetadataRepairPanel';
import {
  ALL_LANGUAGES,
  buildLanguageOptions,
  filterCandidatesByLanguage,
  normalizeLanguageCode,
  normalizeLanguageFilter,
} from '../utils/language';

afterEach(() => vi.unstubAllGlobals());

describe('language normalization utilities', () => {
  it('normalizes equivalent ISO 639-1, ISO 639-2/B/T, BCP-47, and language names', () => {
    const cases: [string | undefined, string][] = [
      ['en', 'en'],
      ['eng', 'en'],
      ['en-US', 'en'],
      ['en_GB', 'en'],
      ['English', 'en'],
      ['/languages/eng', 'en'],
      ['es', 'es'],
      ['spa', 'es'],
      ['es-MX', 'es'],
      ['Spanish', 'es'],
      ['fr', 'fr'],
      ['fra', 'fr'],
      ['fre', 'fr'],
      ['fr-CA', 'fr'],
      ['French', 'fr'],
      ['de', 'de'],
      ['deu', 'de'],
      ['ger', 'de'],
      ['German', 'de'],
      ['pt-BR', 'pt'],
      ['por', 'pt'],
      ['it', 'it'],
      ['ita', 'it'],
      ['ja', 'ja'],
      ['jpn', 'ja'],
      ['zh-CN', 'zh'],
      ['zho', 'zh'],
      ['chi', 'zh'],
      ['und', ''],
      ['unknown', ''],
      ['', ''],
      [undefined, ''],
    ];
    for (const [input, expected] of cases) {
      expect(normalizeLanguageCode(input)).toBe(expected);
    }
    expect(normalizeLanguageFilter('all')).toBe(ALL_LANGUAGES);
    expect(normalizeLanguageFilter('*')).toBe(ALL_LANGUAGES);
  });

  it('filters candidates to matching normalized language plus untagged results', () => {
    const mixed = [
      { title: 'English ISO2', language: 'eng' },
      { title: 'English Locale', language: 'en-US' },
      { title: 'Untagged Book' },
      { title: 'Undetermined Book', language: 'und' },
      { title: 'French Edition', language: 'fre' },
      { title: 'German Edition', language: 'de-DE' },
    ];

    const english = filterCandidatesByLanguage(mixed, 'en');
    expect(english.map((c) => [c.title, c.language])).toEqual([
      ['English ISO2', 'en'],
      ['English Locale', 'en'],
      ['Untagged Book', undefined],
      ['Undetermined Book', undefined],
    ]);

    const french = filterCandidatesByLanguage(mixed, 'fr');
    expect(french.map((c) => [c.title, c.language])).toEqual([
      ['Untagged Book', undefined],
      ['Undetermined Book', undefined],
      ['French Edition', 'fr'],
    ]);

    const all = filterCandidatesByLanguage(mixed, ALL_LANGUAGES);
    expect(all).toHaveLength(6);
    expect(all.find((c) => c.title === 'German Edition')?.language).toBe('de');
  });

  it('builds deduplicated options including All languages, default profile language, and available result languages', () => {
    const options = buildLanguageOptions('de', ['eu', 'fr', 'de'], 'de');
    expect(options[0]).toEqual({ value: 'all', label: 'All languages' });
    expect(options[1]).toEqual({ value: 'de', label: 'German (de)' });
    expect(options.some((o) => o.value === 'eu')).toBe(true);
    expect(options.some((o) => o.value === 'fr')).toBe(true);
    expect(new Set(options.map((o) => o.value)).size).toBe(options.length);
  });
});

describe('MetadataRepairPanel language filtering', () => {
  it('initializes language selector from the default quality profile language and displays matching plus untagged results', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/books/b1/metadata-diff') {
        return Response.json({ preview: 'p1', files: [] });
      }
      if (url === '/api/books/b1/metadata-candidates') {
        return Response.json({
          defaultLanguage: 'de',
          language: 'de',
          availableLanguages: ['de', 'en', 'fr', 'eu'],
          candidates: [
            { title: 'Der Wüstenplanet', authors: ['Frank Herbert'], language: 'ger' },
            { title: 'Dune Untagged', authors: ['Frank Herbert'] },
            { title: 'Should Be Filtered Out', authors: ['Frank Herbert'], language: 'fr-CA' },
          ],
        });
      }
      if (url === '/api/books/b1/metadata-candidates?language=en') {
        return Response.json({
          defaultLanguage: 'de',
          language: 'en',
          availableLanguages: ['de', 'en', 'fr', 'eu'],
          candidates: [
            { title: 'Dune', authors: ['Frank Herbert'], language: 'en-US' },
            { title: 'Dune Untagged', authors: ['Frank Herbert'] },
          ],
        });
      }
      if (url === '/api/books/b1/metadata-candidates?language=all') {
        return Response.json({
          defaultLanguage: 'de',
          language: 'all',
          availableLanguages: ['de', 'en', 'fr', 'eu'],
          candidates: [
            { title: 'Der Wüstenplanet', authors: ['Frank Herbert'], language: 'de' },
            { title: 'Dune', authors: ['Frank Herbert'], language: 'en' },
            { title: 'Dune French', authors: ['Frank Herbert'], language: 'fr' },
            { title: 'Dune Untagged', authors: ['Frank Herbert'] },
          ],
        });
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MetadataRepairPanel bookID="b1" onBack={vi.fn()} />);

    const select = screen.getByRole('combobox', { name: 'Language' });
    await waitFor(() => expect(select).toHaveValue('de'));

    // Matching German candidate (normalized from 'ger' -> 'de') and untagged candidate are shown;
    // mixed foreign candidate ('fr-CA') is filtered out.
    expect(await screen.findByRole('radio', { name: /Der Wüstenplanet.*de/ })).toBeVisible();
    expect(screen.getByRole('radio', { name: /Dune Untagged/ })).toBeVisible();
    expect(screen.queryByRole('radio', { name: /Should Be Filtered Out/ })).not.toBeInTheDocument();

    // Result-discovered language ('eu') is present in selector options.
    expect(screen.getByRole('option', { name: /eu/i })).toBeInTheDocument();

    // Switching to English queries ?language=en and displays normalized English + untagged results.
    await user.selectOptions(select, 'en');
    expect(select).toHaveValue('en');
    expect(await screen.findByRole('radio', { name: /Dune.*Frank Herbert · en/ })).toBeVisible();
    expect(screen.getByRole('radio', { name: /Dune Untagged/ })).toBeVisible();
    expect(screen.queryByRole('radio', { name: /Der Wüstenplanet/ })).not.toBeInTheDocument();

    // Switching to All languages queries ?language=all and displays mixed-language results.
    await user.selectOptions(select, 'all');
    expect(await screen.findByRole('radio', { name: /Dune French/ })).toBeVisible();
    expect(screen.getByRole('radio', { name: /Der Wüstenplanet/ })).toBeVisible();
  });

  it('preserves active search query when changing language and hints when matches exist in other languages', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/books/b2/metadata-diff') {
        return Response.json({ preview: 'p2', files: [] });
      }
      if (url === '/api/books/b2/metadata-candidates') {
        return Response.json({
          defaultLanguage: 'en',
          language: 'en',
          availableLanguages: ['fr'],
          candidates: [],
        });
      }
      if (url === '/api/books/b2/metadata-candidates?q=Le%20Petit%20Prince') {
        return Response.json({
          defaultLanguage: 'en',
          language: 'en',
          availableLanguages: ['fr'],
          candidates: [],
        });
      }
      if (url === '/api/books/b2/metadata-candidates?q=Le%20Petit%20Prince&language=fr') {
        return Response.json({
          defaultLanguage: 'en',
          language: 'fr',
          availableLanguages: ['fr'],
          candidates: [
            { title: 'Le Petit Prince', authors: ['Antoine de Saint-Exupéry'], language: 'fr' },
          ],
        });
      }
      throw new Error(`Unexpected fetch ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MetadataRepairPanel bookID="b2" defaultLanguage="en-US" onBack={vi.fn()} />);

    const select = screen.getByRole('combobox', { name: 'Language' });
    expect(select).toHaveValue('en');

    expect(
      await screen.findByText('No matches found for English. Try another title or switch languages.'),
    ).toBeVisible();

    const searchInput = screen.getByRole('searchbox', { name: 'Search metadata by title' });
    await user.type(searchInput, 'Le Petit Prince{enter}');
    expect(
      await screen.findByText('No matches found for English. Try another title or switch languages.'),
    ).toBeVisible();

    await user.selectOptions(select, 'fr');
    expect(await screen.findByRole('radio', { name: /Le Petit Prince/ })).toBeVisible();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/books/b2/metadata-candidates?q=Le%20Petit%20Prince&language=fr',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });
});

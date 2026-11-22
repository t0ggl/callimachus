import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { ArrowRight, Check } from 'lucide-react';
import type { MetadataCandidate, MetadataCandidatesResponse, MetadataDiff, MetadataPreview } from '../types';
import {
  ALL_LANGUAGES,
  buildLanguageOptions,
  filterCandidatesByLanguage,
  formatLanguageName,
  normalizeLanguageCode,
  normalizeLanguageFilter,
} from '../utils/language';

const fields: Record<string, string> = {
  title: 'Title', subtitle: 'Subtitle', authors: 'Authors', series: 'Series',
  seriesIndex: 'Series index', releaseDate: 'Publication date', publisher: 'Publisher',
  language: 'Language', isbn13: 'ISBN-13', description: 'Synopsis',
};
const focus = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300';
const secondary = `rounded-md border border-zinc-700 bg-zinc-800 px-3 py-2 text-sm font-medium hover:bg-zinc-700 disabled:opacity-50 ${focus}`;

async function metadataRequest<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, options);
  if (!response.ok) {
    const message = response.headers.get('Content-Type')?.startsWith('text/plain') && (await response.text()).trim();
    throw new Error(message || 'The metadata request failed. Try again.');
  }
  return response.json();
}

function MetadataPreviewTable({ rows, before, after }: { rows: MetadataDiff[]; before: string; after: string }) {
  const visibleRows = rows.filter((row) => row.changed || row.libraryValue !== '');
  if (visibleRows.length === 0) return <p className="py-2 text-sm text-zinc-300">No metadata fields to apply.</p>;
  const changedCount = visibleRows.filter((row) => row.changed).length;
  return (
    <div className="space-y-2">
      <p className="text-xs tabular-nums">
        {changedCount > 0 ? (
          <span className="font-medium text-amber-300">
            {changedCount} of {visibleRows.length} {visibleRows.length === 1 ? 'field' : 'fields'} changing
          </span>
        ) : (
          <span className="text-zinc-400">
            All {visibleRows.length} {visibleRows.length === 1 ? 'field' : 'fields'} unchanged
          </span>
        )}
      </p>
      <table className="w-full table-fixed text-left text-sm [overflow-wrap:anywhere]">
        <thead className="text-xs text-zinc-300">
          <tr>
            <th scope="col" className="w-[30%] sm:w-[26%] py-2 pr-2">Field</th>
            <th scope="col" className="py-2 pr-2">{before}</th>
            <th scope="col" className="py-2">{after}</th>
          </tr>
        </thead>
        <tbody>
          {visibleRows.map((row) => (
            <tr
              key={row.field}
              data-changed={row.changed ? 'true' : 'false'}
              className={`border-t border-zinc-800 align-top ${row.changed ? 'bg-amber-500/[0.07]' : ''}`}
            >
              <th scope="row" className="py-2 pr-2 font-medium">
                <span className={`block ${row.changed ? 'text-zinc-100' : 'text-zinc-300'}`}>
                  {fields[row.field] ?? row.field}
                </span>
                {row.changed ? (
                  <span className="mt-1 inline-flex items-center gap-1 rounded-sm border border-amber-500/40 bg-amber-500/15 px-1.5 py-0.5 text-[11px] font-medium text-amber-200">
                    <ArrowRight className="w-3 h-3 shrink-0" aria-hidden="true" />
                    Changed
                  </span>
                ) : (
                  <span className="mt-1 inline-flex items-center gap-1 rounded-sm border border-zinc-700/80 bg-zinc-800/70 px-1.5 py-0.5 text-[11px] font-medium text-zinc-400">
                    <Check className="w-3 h-3 shrink-0" aria-hidden="true" />
                    Unchanged
                  </span>
                )}
              </th>
              <td className={`whitespace-pre-wrap py-2 pr-2 ${row.changed ? 'text-zinc-300' : 'text-zinc-400'}`}>
                {row.fileValue ? row.fileValue : <span className="italic text-zinc-400">Empty</span>}
              </td>
              <td className={`whitespace-pre-wrap py-2 ${row.changed ? 'font-semibold text-amber-200' : 'text-zinc-400'}`}>
                {row.libraryValue ? row.libraryValue : <span className="italic text-zinc-400">Empty</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

interface Props {
  bookID: string;
  defaultLanguage?: string;
  initialCandidate?: MetadataCandidate;
  onBack: () => void;
  onApplied?: () => void;
}

function parseCandidatesPayload(
  payload: MetadataCandidatesResponse | MetadataCandidate[],
): {
  candidates: MetadataCandidate[];
  defaultLanguage?: string;
  language?: string;
  availableLanguages: string[];
} {
  if (Array.isArray(payload)) {
    const discovered = new Set<string>();
    for (const item of payload) {
      const code = normalizeLanguageCode(item.language);
      if (code) discovered.add(code);
    }
    return {
      candidates: payload,
      availableLanguages: Array.from(discovered).sort(),
    };
  }
  const candidates = Array.isArray(payload.candidates) ? payload.candidates : [];
  const discovered = new Set<string>();
  for (const lang of payload.availableLanguages ?? []) {
    const code = normalizeLanguageCode(lang);
    if (code) discovered.add(code);
  }
  for (const item of candidates) {
    const code = normalizeLanguageCode(item.language);
    if (code) discovered.add(code);
  }
  return {
    candidates,
    defaultLanguage: payload.defaultLanguage,
    language: payload.language,
    availableLanguages: Array.from(discovered).sort(),
  };
}

export function MetadataRepairPanel({ bookID, defaultLanguage, initialCandidate, onBack, onApplied }: Props) {
  const heading = useRef<HTMLHeadingElement>(null);
  const id = useId();
  const [query, setQuery] = useState('');
  const [search, setSearch] = useState({ term: '', attempt: 0 });
  const [defaultLang, setDefaultLang] = useState(
    () => normalizeLanguageFilter(defaultLanguage) || 'en',
  );
  const [selectedLanguage, setSelectedLanguage] = useState<string | null>(null);
  const [availableLanguages, setAvailableLanguages] = useState<string[]>([]);
  const [choices, setChoices] = useState<MetadataCandidate[]>();
  const [searchError, setSearchError] = useState('');
  const [candidate, setCandidate] = useState<MetadataCandidate | undefined>(initialCandidate);
  const [preview, setPreview] = useState<MetadataPreview>();
  const [previewError, setPreviewError] = useState('');
  const [previewAttempt, setPreviewAttempt] = useState(0);
  const [applyError, setApplyError] = useState('');
  const [applying, setApplying] = useState(false);
  const [completed, setCompleted] = useState(false);
  const base = `/api/books/${encodeURIComponent(bookID)}`;
  const effectiveLanguage = selectedLanguage ?? defaultLang;
  const languageOptions = useMemo(
    () => buildLanguageOptions(defaultLang, availableLanguages, effectiveLanguage),
    [defaultLang, availableLanguages, effectiveLanguage],
  );
  const otherAvailableLanguages = useMemo(
    () => availableLanguages.filter((lang) => lang !== effectiveLanguage),
    [availableLanguages, effectiveLanguage],
  );

  useEffect(() => { heading.current?.focus(); }, []);

  useEffect(() => {
    const controller = new AbortController();
    const queryParts: string[] = [];
    if (search.term) queryParts.push(`q=${encodeURIComponent(search.term)}`);
    if (selectedLanguage !== null) queryParts.push(`language=${encodeURIComponent(selectedLanguage)}`);
    const suffix = queryParts.length > 0 ? `?${queryParts.join('&')}` : '';
    void metadataRequest<MetadataCandidatesResponse | MetadataCandidate[]>(
      base + '/metadata-candidates' + suffix,
      { signal: controller.signal },
    )
      .then((payload) => {
        if (controller.signal.aborted) return;
        const parsed = parseCandidatesPayload(payload);
        const resolvedDefault = normalizeLanguageFilter(parsed.defaultLanguage);
        if (resolvedDefault) {
          setDefaultLang(resolvedDefault);
        }
        const activeFilter =
          selectedLanguage ??
          normalizeLanguageFilter(parsed.language) ??
          (resolvedDefault || defaultLang);
        setAvailableLanguages(parsed.availableLanguages);
        setChoices(filterCandidatesByLanguage(parsed.candidates, activeFilter));
      })
      .catch((error: Error) => {
        if (!controller.signal.aborted) setSearchError(error.message);
      });
    return () => controller.abort();
  }, [base, search, selectedLanguage]);

  function handleLanguageChange(nextLanguage: string) {
    const normalized = normalizeLanguageFilter(nextLanguage) || ALL_LANGUAGES;
    setSearchError('');
    setChoices(undefined);
    select(undefined);
    setSelectedLanguage(normalized);
  }

  useEffect(() => {
    const controller = new AbortController();
    const options: RequestInit = { signal: controller.signal };
    if (candidate) {
      options.method = 'POST';
      options.headers = { 'Content-Type': 'application/json' };
      options.body = JSON.stringify({ candidate });
    }
    void metadataRequest<MetadataPreview>(base + '/metadata-diff', options)
      .then((result) => { if (!controller.signal.aborted) setPreview(result); })
      .catch((error: Error) => { if (!controller.signal.aborted) setPreviewError(error.message); });
    return () => controller.abort();
  }, [base, candidate, previewAttempt]);

  function select(next?: MetadataCandidate) {
    if (next === candidate) return;
    setPreview(undefined);
    setPreviewError('');
    setApplyError('');
    setCandidate(next);
  }

  async function apply() {
    if (!preview || applying) return;
    setApplying(true);
    setApplyError('');
    try {
      await metadataRequest(base + '/fix-metadata', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ candidate, preview: preview.preview }),
      });
      setCompleted(true);
      onApplied?.();
    } catch (error) {
      setApplyError(error instanceof Error ? error.message : 'Unable to apply metadata.');
      setPreview(undefined);
      setPreviewAttempt((attempt) => attempt + 1);
    } finally {
      setApplying(false);
    }
  }

  return (
    <section aria-labelledby={id} className="flex min-h-0 flex-1 flex-col">
      <div className="space-y-5 overflow-y-auto p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 id={id} ref={heading} tabIndex={-1} className="text-lg font-semibold">Fix Metadata</h3>
        <button type="button" className={secondary} disabled={applying} onClick={onBack}>Back to details</button>
      </div>
      {completed ? (
        <p role="status" className="text-sm text-emerald-300">Metadata applied to {candidate ? 'the library and EPUB files.' : 'the EPUB files.'}</p>
      ) : <>
        <form className="space-y-3" onSubmit={(event) => {
          event.preventDefault();
          setSearchError('');
          setChoices(undefined);
          select(undefined);
          setSearch({ term: query.trim(), attempt: search.attempt + 1 });
        }}>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
            <div className="space-y-1.5">
              <label htmlFor={id + '-query'} className="block text-sm font-medium text-zinc-200">Search metadata by title</label>
              <div className="flex gap-2">
                <input id={id + '-query'} type="search" value={query} maxLength={500} disabled={applying}
                  onChange={(event) => setQuery(event.target.value)}
                  className={`min-w-0 flex-1 rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 ${focus}`} />
                <button type="submit" className={secondary} disabled={applying}>Search</button>
              </div>
            </div>
            <div className="space-y-1.5 sm:min-w-[11rem]">
              <label htmlFor={id + '-language'} className="block text-sm font-medium text-zinc-200">Language</label>
              <select
                id={id + '-language'}
                value={effectiveLanguage}
                disabled={applying}
                onChange={(event) => handleLanguageChange(event.target.value)}
                className={`w-full rounded-md border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 ${focus}`}
              >
                {languageOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </form>
        <fieldset disabled={applying} className="min-w-0 space-y-1">
          <legend className="mb-2 text-sm font-semibold">{search.term ? 'Search results' : 'Suggested matches'}</legend>
          <label className="flex items-start gap-2 rounded-md px-2 py-2 text-sm hover:bg-zinc-800">
            <input type="radio" name={id} checked={!candidate} onChange={() => select(undefined)}
              className={`mt-0.5 shrink-0 accent-blue-400 ${focus}`} />
            <span>Use current library metadata</span>
          </label>
          {initialCandidate && (
            <label className="flex items-start gap-2 rounded-md px-2 py-2 text-sm hover:bg-zinc-800">
              <input type="radio" name={id} checked={candidate === initialCandidate} onChange={() => select(initialCandidate)}
                className={`mt-0.5 shrink-0 accent-blue-400 ${focus}`} />
              <span className="min-w-0 [overflow-wrap:anywhere]">
                <span className="block font-medium">Use matched ideal metadata</span>
                <span className="block text-xs text-zinc-300">
                  {[
                    initialCandidate.title,
                    initialCandidate.series
                      ? `${initialCandidate.series}${initialCandidate.series_index !== undefined ? ` #${initialCandidate.series_index}` : ''}`
                      : undefined,
                  ].filter(Boolean).join(' · ')}
                </span>
              </span>
            </label>
          )}
          {choices?.map((choice, index) => {
            const normalizedLang = normalizeLanguageCode(choice.language);
            const edition = [
              choice.authors?.join(', '), choice.publish_date, choice.publisher, normalizedLang || choice.language,
              choice.isbn13 && `ISBN ${choice.isbn13}`,
            ].filter(Boolean).join(' · ');
            return (
              <label key={index} className="flex items-start gap-2 rounded-md px-2 py-2 text-sm hover:bg-zinc-800">
                <input type="radio" name={id} checked={candidate === choice} onChange={() => select(choice)}
                  className={`mt-0.5 shrink-0 accent-blue-400 ${focus}`} />
                <span className="min-w-0 [overflow-wrap:anywhere]">
                  <span className="block font-medium">{[choice.title, choice.subtitle].filter(Boolean).join(' — ')}</span>
                  {edition && <span className="block text-xs text-zinc-300">{edition}</span>}
                </span>
              </label>
            );
          })}
          {searchError && <p role="alert" className="text-sm text-red-300">{searchError}</p>}
          {!choices && !searchError && <p role="status" className="text-sm text-zinc-300">Searching for matches…</p>}
          {choices?.length === 0 && (
            <p role="status" className="text-sm text-zinc-300">
              {effectiveLanguage !== ALL_LANGUAGES && otherAvailableLanguages.length > 0
                ? `No matches found for ${formatLanguageName(effectiveLanguage)}. Try another title or switch languages.`
                : 'No matches found. Try another title.'}
            </p>
          )}
        </fieldset>
        <div className="space-y-3 border-t border-zinc-800 pt-4">
          <h4 className="text-sm font-semibold">Preview metadata to apply</h4>
          {previewError && (
            <div className="space-y-2">
              <p role="alert" className="text-sm text-red-300">{previewError}</p>
              <button type="button" className={secondary} onClick={() => {
                setPreviewError('');
                setPreviewAttempt((attempt) => attempt + 1);
              }}>Reload preview</button>
            </div>
          )}
          {!preview && !previewError && <p role="status" className="text-sm text-zinc-300">Loading preview…</p>}
          {preview?.bookDiffs && preview.bookDiffs.length > 0 && (
            <div className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/60 p-3">
              <h5 className="text-sm font-semibold">Library book</h5>
              <MetadataPreviewTable rows={preview.bookDiffs} before="Current library" after="Selected metadata" />
            </div>
          )}
          {preview?.files.map((file) => (
            <div key={file.id} className="space-y-2 rounded-lg border border-zinc-800 bg-zinc-950/60 p-3">
              <h5 className="text-sm font-semibold [overflow-wrap:anywhere]">{file.filename}</h5>
              <MetadataPreviewTable rows={file.diffs} before="Current file" after={candidate ? 'Selected metadata' : 'Library metadata'} />
              {file.libraryDiffs && file.libraryDiffs.length > 0 && (
                <div className="space-y-2 border-t border-zinc-800 pt-3">
                  <h6 className="text-xs font-semibold text-zinc-300">Library edition</h6>
                  <MetadataPreviewTable rows={file.libraryDiffs} before="Current library edition" after="Selected metadata" />
                </div>
              )}
            </div>
          ))}
        </div>
      </>}
      </div>
      {!completed && (
        <div className="flex flex-wrap items-center justify-end gap-x-4 gap-y-2 border-t border-zinc-800 bg-zinc-950/60 px-6 py-3">
          {applyError && <p role="alert" className="grow text-sm text-red-300">{applyError}</p>}
          <button type="button" onClick={() => void apply()} disabled={!preview || applying}
            className={`rounded-md bg-blue-400 px-4 py-2 text-sm font-semibold text-zinc-950 hover:bg-blue-300 disabled:opacity-50 ${focus}`}>
            {applying ? 'Applying metadata…' : 'Apply Metadata'}
          </button>
        </div>
      )}
    </section>
  );
}

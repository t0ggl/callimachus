import { useContext, useEffect, useId, useState } from 'react';
import { AlertCircle, Loader2 } from 'lucide-react';
import { RequestEvent } from '../types';
import { ActivityItem, BookActivityContext, isFinished, stages } from '../utils/activity';

export function BookAcquisition({ bookId }: { bookId: string }) {
  const activity = useContext(BookActivityContext);
  const [seenIds, setSeenIds] = useState<string[]>([]);
  const items = activity.filter(item => item.bookId === bookId);
  const newIds = items.filter(item => !isFinished(item) && !seenIds.includes(item.id)).map(item => item.id);
  if (newIds.length) {
    // Keep jobs visible through completion while their book details are open.
    setSeenIds([...seenIds, ...newIds]);
  }
  return items.filter(item => !isFinished(item) || seenIds.includes(item.id)).map(item => (
    <AcquisitionProgress key={item.id} item={item} />
  ));
}

export function AcquisitionProgress({ item }: { item: ActivityItem }) {
  const logId = useId();
  const [showLog, setShowLog] = useState(false);
  const [events, setEvents] = useState<RequestEvent[] | null>(null);
  const [logError, setLogError] = useState(false);
  const requestId = item.request?.id;

  useEffect(() => {
    if (!showLog || !requestId) return;
    const controller = new AbortController();
    let next: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const response = await fetch(`/api/requests/${encodeURIComponent(requestId)}`, { signal: controller.signal });
        if (!response.ok) throw new Error();
        const detail = await response.json();
        if (!controller.signal.aborted) {
          setEvents(detail.events || []);
          setLogError(false);
        }
      } catch {
        if (!controller.signal.aborted) setLogError(true);
      } finally {
        if (!controller.signal.aborted) next = setTimeout(load, 2500);
      }
    };
    void load();
    return () => {
      controller.abort();
      clearTimeout(next);
    };
  }, [showLog, requestId]);

  const current = stages.findIndex(step => step.id === item.stage);

  return (
    <section aria-label="Acquisition progress" className="space-y-4 border-t border-zinc-800 pt-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-zinc-100">{item.format || 'Acquisition'} · {item.phase}</h3>
        {requestId && <button type="button" aria-expanded={showLog} aria-controls={logId}
          className="inline-flex items-center justify-center min-h-11 sm:min-h-9 rounded-md border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-xs font-medium text-blue-300 transition-colors hover:bg-zinc-700 hover:text-blue-200 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-300"
          onClick={() => setShowLog(visible => !visible)}>
          {showLog ? 'Hide Search Log' : 'View Search Log'}
        </button>}
      </div>
      <ol aria-label="Acquisition stages" className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-3 lg:grid-cols-6">
        {stages.map((step, index) => <li key={step.id} aria-current={index === current ? 'step' : undefined}
          className={`space-y-1.5 text-xs ${index === current ? 'font-semibold text-blue-200' : index < current ? 'text-zinc-300' : 'text-zinc-400'}`}>
          <span aria-hidden="true" className="flex items-center gap-1.5">
            <span className={`size-2.5 shrink-0 rounded-full border ${index === current ? 'border-blue-400 bg-blue-400' : index < current ? 'border-emerald-400 bg-emerald-400' : 'border-zinc-500'}`} />
            {index < stages.length - 1 && <span className={`h-0.5 flex-1 rounded-full ${index < current ? 'bg-emerald-400' : 'bg-zinc-700'}`} />}
          </span>
          <span>{step.label}</span>
        </li>)}
      </ol>
      {item.progress !== undefined && <p className="text-xs tabular-nums text-zinc-300">
        {item.detail ? `${item.detail} · ` : ''}{item.progress.toFixed(1)}% overall
      </p>}
      {item.error && <p role="alert" className="text-xs text-red-300">{item.error}</p>}
      {requestId && <div id={logId} hidden={!showLog} className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3.5 text-xs text-zinc-300">
        {logError && <div role="alert" className="flex items-center gap-2 text-red-200">
          <AlertCircle className="w-4 h-4 text-red-400 shrink-0" aria-hidden="true" />
          <span>Unable to load the search log. Retrying…</span>
        </div>}
        {!logError && events === null && <div role="status" className="flex items-center gap-2 text-zinc-300">
          <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin text-blue-400 shrink-0" aria-hidden="true" />
          <span>Loading search log…</span>
        </div>}
        {events?.length === 0 && <p className="text-zinc-400">No search events recorded yet.</p>}
        {!!events?.length && <ol aria-label="Search log" className="max-h-56 divide-y divide-zinc-800/80 overflow-y-auto">
          {events.map(event => <li key={event.id} className="grid gap-1 py-2 first:pt-0 last:pb-0 sm:grid-cols-[11rem_1fr] sm:gap-3">
            <time dateTime={event.occurred_at} className="text-xs font-mono tabular-nums text-zinc-400">{new Date(event.occurred_at).toLocaleString()}</time>
            <span className="min-w-0 wrap-anywhere text-zinc-200">{event.message}</span>
          </li>)}
        </ol>}
      </div>}
    </section>
  );
}

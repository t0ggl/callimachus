import type { SeriesSelection } from '../types';
import { entityURL } from './EntityLink';

interface Props {
  title?: string;
  index?: number | null;
  total?: number;
  id?: string;
  author?: string;
  hardcoverID?: number;
  onSelect: (series: SeriesSelection) => void;
}

export function SeriesBadge({ title, index, total, id, author, hardcoverID, onSelect }: Props) {
  if (!title) return null;
  return <span className="block mt-2 text-xs text-zinc-300">
    {index != null && Number.isFinite(index) ? `Book ${index}${total && total > 0 ? ` of ${total}` : ''} in ` : 'In '}
    &quot;<button type="button" className="text-purple-300 underline underline-offset-2 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-purple-300" onKeyDown={e => e.stopPropagation()} onClick={(e) => { e.stopPropagation(); onSelect({ title, id, author, hardcoverID }); if (id) window.location.hash = entityURL('series', title, id); }}>{title}</button>&quot; series
  </span>;
}

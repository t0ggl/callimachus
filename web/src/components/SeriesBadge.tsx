
interface Props {
  title?: string;
  index?: number | null;
  total?: number;
  onSelect: (title: string) => void;
}

export function SeriesBadge({ title, index, total, onSelect }: Props) {
  if (!title) return null;
  return <span className="block mt-2 text-xs text-zinc-300">
    {index != null && Number.isFinite(index) ? `Book ${index}${total && total > 0 ? ` of ${total}` : ''} in ` : 'In '}
    &quot;<button type="button" className="text-purple-300 underline underline-offset-2 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-purple-300" onKeyDown={e => e.stopPropagation()} onClick={(e) => { e.stopPropagation(); onSelect(title); window.location.hash = `#/series/${encodeURIComponent(title)}`; }}>{title}</button>&quot; series
  </span>;
}

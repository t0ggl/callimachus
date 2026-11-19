import type { ReactNode } from 'react';
export function entityURL(kind: 'authors' | 'series', name: string, id?: string) {
  return `#/${kind}/${encodeURIComponent(name)}${id ? `?id=${encodeURIComponent(id)}` : ''}`;
}
export function EntityLink({ kind, name, id, children }: { kind: 'authors' | 'series'; name: string; id?: string; children?: ReactNode }) {
  return <a href={entityURL(kind, name, id)} onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()} className="text-blue-300 underline underline-offset-2 rounded-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-300">{children || name}</a>;
}

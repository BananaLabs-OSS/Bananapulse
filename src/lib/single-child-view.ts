import type { ScopeView } from './view';

/** Skip scopes with no choice to make; stop at the first branch or leaf. */
export async function singleChildDestination(
  initial: ScopeView | null,
  currentPath: string,
  load: (segments: string[]) => ScopeView | null | Promise<ScopeView | null>,
): Promise<string | null> {
  let view = initial;
  let destination: string | null = null;
  const visited = new Set([currentPath.replace(/\/$/, '') || '/']);
  while (view?.children.length === 1) {
    const href = view.children[0].href;
    if (!href.startsWith('/') || href.startsWith('//')) break;
    const path = href.replace(/\/+$/, '') || '/';
    if (visited.has(path)) break;
    visited.add(path);
    const next = await load(path.split('/').filter(Boolean));
    if (!next) break;
    destination = path;
    view = next;
  }
  return destination;
}

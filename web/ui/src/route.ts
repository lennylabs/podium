// Hash routing. The bundle is served by a static file server that answers
// one document, so the location hash carries the route and no server-side
// rewrite is involved. An artifact ID is a directory path that nests to
// arbitrary depth, so every identifier in a route is encoded.

import { useEffect, useState } from 'react';

export type Route =
  | { name: 'domain'; path: string }
  | { name: 'search'; query: string }
  | { name: 'artifact'; id: string }
  | { name: 'layers' };

export function parseRoute(hash: string): Route {
  const raw = hash.replace(/^#\/?/, '');
  const [head, ...rest] = raw.split('/');
  const tail = decodeURIComponent(rest.join('/'));
  switch (head) {
    case 'search':
      return { name: 'search', query: tail };
    case 'artifact':
      return { name: 'artifact', id: tail };
    case 'layers':
      return { name: 'layers' };
    case 'domain':
      return { name: 'domain', path: tail };
    default:
      // The registry root is addressed by the empty path, which is the
      // route the page opens on.
      return { name: 'domain', path: '' };
  }
}

export function domainHref(path: string): string {
  return path === '' ? '#/' : `#/domain/${encodeURIComponent(path)}`;
}

export function artifactHref(id: string): string {
  return `#/artifact/${encodeURIComponent(id)}`;
}

export function searchHref(query: string): string {
  return `#/search/${encodeURIComponent(query)}`;
}

export const layersHref = '#/layers';

/** useRoute tracks the location hash. */
export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.hash));
  useEffect(() => {
    const onChange = () => {
      setRoute(parseRoute(window.location.hash));
    };
    window.addEventListener('hashchange', onChange);
    return () => {
      window.removeEventListener('hashchange', onChange);
    };
  }, []);
  return route;
}

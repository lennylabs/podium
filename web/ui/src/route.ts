// Hash routing. The bundle is served by a static file server that answers
// one document, so the location hash carries the route and no server-side
// rewrite is involved. An artifact ID is a directory path that nests to
// arbitrary depth, so every identifier in a route is encoded.

import { useEffect, useState } from 'react';

import { heldRoute } from './components/focus';

export type Route =
  | { name: 'domain'; path: string }
  | { name: 'search'; query: string }
  | { name: 'artifact'; id: string }
  // deleted selects the recovery surface, which is a page of its own under
  // the panel rather than a section inside it: it carries its own table, and
  // stacking that table above the panel's pushes the precedence label and the
  // layer rows off the first screen.
  | { name: 'layers'; deleted: boolean };

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
      return { name: 'layers', deleted: tail === 'deleted' };
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

/** atCatalogRoute reports whether `hash` already addresses the registry root,
 * which is where `domainHref('')` leads. A control that offers the reader the
 * catalog from the catalog navigates to where they are and leaves the screen
 * unchanged, which reads as an action that failed. The registry root is
 * addressed by more than one hash, so the answer comes from the parsed route
 * rather than from a string comparison against `#/`. */
export function atCatalogRoute(hash: string): boolean {
  const route = parseRoute(hash);
  return route.name === 'domain' && route.path === '';
}

export function artifactHref(id: string): string {
  return `#/artifact/${encodeURIComponent(id)}`;
}

/** artifactLeaf is the artifact's own name inside its §4.2 path, which is
 * what a row states when the domains above it are already on the page. */
export function artifactLeaf(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut < 0 ? id : id.slice(cut + 1);
}

/** artifactDomain is the §4.2 domain the artifact hangs under, which is where
 * the sidebar tree puts an open artifact. An artifact registered at the root
 * carries no domain above it, so the answer there is the empty path. */
export function artifactDomain(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut < 0 ? '' : id.slice(0, cut);
}

export function searchHref(query: string): string {
  return `#/search/${encodeURIComponent(query)}`;
}

export const layersHref = '#/layers';

/** deletedLayersHref addresses the recovery surface. It hangs under the panel
 * because a restore is a layer write and the trail above it leads back there. */
export const deletedLayersHref = '#/layers/deleted';

/**
 * replaceRoute rewrites the current history entry to `href` without pushing a
 * new one and without firing `hashchange`, so a surface can hold its state in
 * the address bar as the reader edits it. Following a link stays a push, and
 * the reader's back step returns to where they came from rather than to the
 * previous keystroke.
 */
export function replaceRoute(href: string): void {
  if (window.location.hash === href) {
    return;
  }
  window.history.replaceState(null, '', href);
}

/** routeKey names the surface a route selects. `useRoute` parses a fresh
 * object on every hash event, so the object's identity moves even when the
 * reader stayed where they were; an effect that has to run once per entered
 * surface keys on this instead. */
export function routeKey(route: Route): string {
  switch (route.name) {
    case 'domain':
      return `domain/${route.path}`;
    case 'search':
      return `search/${route.query}`;
    case 'artifact':
      return `artifact/${route.id}`;
    case 'layers':
      return route.deleted ? 'layers/deleted' : 'layers';
  }
}

/** useRoute tracks the location hash. A dialog that withholds every dismissal
 * route withholds the history step with it: the reader's Back gesture fires no
 * key and no press the dialog can see, and leaving the route unmounts the
 * surface the dialog is rendered from, which for the one-time webhook secret
 * discards a credential recoverable only by rotating it. While such a dialog
 * holds, the route it opened on is written back over the entry the gesture
 * landed on and the shell stays where it was. `replaceState` fires no
 * `hashchange`, so the correction does not re-enter this handler. */
export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.hash));
  useEffect(() => {
    const onChange = () => {
      const held = heldRoute();
      if (held !== null) {
        if (window.location.hash !== held) {
          window.history.replaceState(null, '', held === '' ? '#/' : held);
        }
        return;
      }
      setRoute(parseRoute(window.location.hash));
    };
    window.addEventListener('hashchange', onChange);
    return () => {
      window.removeEventListener('hashchange', onChange);
    };
  }, []);
  return route;
}

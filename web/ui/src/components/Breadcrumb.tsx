// The trail of domains above the page. The domain browser and the artifact
// viewer both open on one, and both address the same §4.2 hierarchy, so the
// trail is rendered once here rather than per surface.

import { domainHref } from '../route';

/** Breadcrumb links every domain above and including `path` back to the
 * domain browser. An empty path is the registry root, which is the single
 * link the trail then carries. */
export function Breadcrumb({ path }: { path: string }) {
  const segments = path === '' ? [] : path.split('/');
  return (
    <nav className="breadcrumb" aria-label="Breadcrumb">
      <a href={domainHref('')}>root</a>
      {segments.map((segment, index) => (
        <a key={segment + String(index)} href={domainHref(segments.slice(0, index + 1).join('/'))}>
          {segment}
        </a>
      ))}
    </nav>
  );
}

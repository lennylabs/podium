// The trail of domains above the page. The domain browser and the artifact
// viewer both open on one, and both address the same §4.2 hierarchy, so the
// trail is rendered once here rather than per surface.

import { Fragment } from 'react';

import { domainHref } from '../route';

/** Breadcrumb links every domain above the page back to the domain browser,
 * separated by the slash the §4.2 path itself uses. The registry root opens
 * the trail as `catalog`, because the root has no segment of its own to name.
 *
 * The trail's last entry is where the reader already stands, so it is plain
 * text rather than a link to the page being read. A surface whose page is not
 * a domain, such as the artifact viewer, names that entry through `current`,
 * and every domain in `path` stays a link. */
export function Breadcrumb({ path, current }: { path: string; current?: string }) {
  const segments = path === '' ? [] : path.split('/');
  const trail = [
    { label: 'catalog', href: domainHref('') },
    ...segments.map((segment, index) => ({
      label: segment,
      href: domainHref(segments.slice(0, index + 1).join('/')),
    })),
  ];
  if (current !== undefined) {
    trail.push({ label: current, href: '' });
  }
  const last = trail.length - 1;
  return (
    <nav className="breadcrumb" aria-label="Breadcrumb">
      {trail.map((entry, index) => (
        <Fragment key={entry.label + String(index)}>
          {index > 0 && (
            <span className="breadcrumb-sep" aria-hidden="true">
              /
            </span>
          )}
          {index === last ? (
            <span className="breadcrumb-here" aria-current="page">
              {entry.label}
            </span>
          ) : (
            <a href={entry.href}>{entry.label}</a>
          )}
        </Fragment>
      ))}
    </nav>
  );
}

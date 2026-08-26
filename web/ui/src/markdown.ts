// The single rendering path for an artifact body. An artifact body is
// markdown authored by whoever can write to a layer's source, and the viewer
// renders it as a document on the registry's own origin, which is the origin
// the session cookie is scoped to.
//
// The sanitizer runs on the rendered output rather than on the markdown
// source, so a construct the renderer emits cannot bypass it and a construct
// the renderer passes through as markup is neutralized. No executable node
// and no event-handler attribute survives, and the allowlist admits no URL
// scheme other than http, https, and mailto on any attribute it keeps.
// Beyond that rule, the path drops the form controls and drops every
// attribute the browser fetches on its own, so an author can neither prompt a
// reader for a credential on the origin the session cookie is scoped to nor
// turn a view of an artifact into a request the reader never asked for.
//
// Frontmatter does not reach this path. It is parsed into a property table
// whose values render as text.

import DOMPurify from 'dompurify';
import { marked } from 'marked';

import { artifactHref } from './route';

// allowedURI admits an http, https, or mailto URL and a URL carrying no
// scheme at all, which is what a relative link inside a manifest looks like.
// Every other scheme, javascript: and data: among them, fails the test and
// the sanitizer drops the attribute carrying it.
//
// The test decides scheme-bearing against relative before it consults the
// allowlist, and it decides it on the production RFC 3986 spells: a scheme is
// a letter followed by letters, digits, and the three punctuation characters,
// terminated by a colon. The negative lookahead is that production, so a
// value carrying any scheme reaches the allowlist and only the three named
// there survive. A run that admitted the relative forms by their own spelling
// would have to enumerate every character a scheme cannot hold, and a
// character it missed, a digit among them, would admit the scheme carrying it
// as if it were relative.
const allowedURI = /^(?:(?:https?|mailto):|(?![a-z][a-z0-9+.\-]*:))/i;

// The attributes that carry a single URL the reader follows by acting on it,
// plus the form attributes the HTML profile would otherwise keep. Every one
// of them is re-tested against the allowlist below.
const linkAttributes = ['href', 'action', 'formaction'];

// The attributes the browser fetches on its own as it lays the document out,
// whether they carry a single URL or a comma-separated candidate list. None
// of them survives, whatever URL it carries.
//
// A fetch the reader never asked for reaches a foreign host with the reader's
// IP address, User-Agent, and Referer. A same-origin one is worse rather than
// safer: the registry serves its API on the origin the UI is served from, so
// a body carrying `<img src="/v1/catalog?probe=1">` makes every reader's
// browser issue an author-chosen credentialed GET against the API, carrying
// whatever session cookie that reader holds. Confining these attributes to an
// asset prefix that cannot reach `/v1/` would need such a prefix to exist,
// and the registry serves no per-artifact asset route, so a body has nothing
// legitimate to name on one of them and the whole class is dropped.
//
// A link attribute is a different case and keeps its allowlist test above:
// following one takes a deliberate act by the reader, and the destination is
// visible before the act.
const fetchAttributes = [
  'src',
  'xlink:href',
  'background',
  'poster',
  'srcset',
  'imagesrcset',
];

// A browser ignores leading and embedded whitespace and control characters
// when it resolves a URL's scheme, so the test runs on the value with those
// removed rather than on the authored bytes.
const attributeWhitespace = /[\u0000-\u0020\u00A0\u1680\u180E\u2000-\u2029\u205F\u3000]/g;

/** allows reports whether one URL passes the allowlist. The value is tested
 * with whitespace and control characters removed, because a browser ignores
 * them when it resolves a scheme. */
function allows(url: string): boolean {
  return allowedURI.test(url.replace(attributeWhitespace, ''));
}

// The allowlist above is expressed to the sanitizer as its URI pattern, but
// that pattern is one of several branches the sanitizer admits a URL on: it
// keeps a data: URL on a media element's source attribute whatever the
// pattern says, and it tests a candidate list by its leading candidate alone.
// The rule admits no scheme other than http, https, and mailto on any
// attribute the sanitizer keeps, so this hook re-tests every link attribute
// that survived and drops the ones no scheme in the allowlist covers. It
// closes the element branches at once rather than naming the media elements,
// so a sanitizer release that adds one does not reopen them. The hook also
// drops the fetching attributes outright, which the sanitizer's URI pattern
// cannot express because it runs on every attribute alike and a link the
// reader chooses to follow stays legitimate.
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (!(node instanceof Element)) {
    return;
  }
  for (const name of linkAttributes) {
    const value = node.getAttribute(name);
    if (value !== null && !allows(value)) {
      node.removeAttribute(name);
    }
  }
  for (const name of fetchAttributes) {
    node.removeAttribute(name);
  }
  clearMarkers(node);
  markStrippedLink(node);
});

// The class an anchor left without a destination carries. An anchor whose
// href the allowlist refused keeps its element and its text, and without a
// marker it draws in the link colour and invites a click that goes nowhere.
// The class drops it to body text and names the removal beside it, so the
// reader sees that the destination is gone rather than a link that is merely
// broken.
const strippedLinkClass = 'link-stripped';

// The class the note left in place of an image carries. An image whose source
// the hook removed has nothing left to draw, and the element on its own draws
// the browser's broken-image placeholder, which tells the reader that
// something failed rather than that the viewer refused the source. The note
// names the removal in the same terms the stripped link uses.
const strippedImageClass = 'image-stripped';

/** clearMarkers strips both removal markers from a node, so a body that
 * writes one on a live element of its own cannot pass that element off as a
 * neutralized one. */
function clearMarkers(node: Element): void {
  for (const marker of [strippedLinkClass, strippedImageClass]) {
    node.classList.remove(marker);
  }
  if (node.hasAttribute('class') && node.classList.length === 0) {
    node.removeAttribute('class');
  }
}

/** markStrippedLink marks an anchor that carries no destination. */
function markStrippedLink(node: Element): void {
  if (node.tagName === 'A' && !node.hasAttribute('href')) {
    node.classList.add(strippedLinkClass);
  }
}

/** replaceStrippedImages replaces every image left without a source by a note
 * holding the image's alt text. The element is replaced rather than marked
 * because an image draws no text of its own, so a marker class on it would
 * leave the reader the broken-image placeholder it exists to remove.
 *
 * The pass runs on the sanitizer's output rather than inside its hook,
 * because the hook clears the marker classes from every node it visits and
 * would clear the marker off the note this pass inserts. */
function replaceStrippedImages(root: DocumentFragment): void {
  for (const image of root.querySelectorAll('img:not([src])')) {
    const note = document.createElement('span');
    note.className = strippedImageClass;
    note.textContent = image.getAttribute('alt') ?? '';
    image.replaceWith(note);
  }
}

// A §4.4 prose reference is written as an ordinary relative markdown link,
// and `lint.prose_reference` admits one only when it resolves to a file the
// artifact bundles or to another artifact's canonical ID. The second form is
// the one an author is directed to write for a cross-artifact reference, and
// left as authored the browser resolves it against the `/ui/` mount, where
// the registry serves no such path: following it leaves the SPA for a
// plain-text 404 and the shell disappears. So a reference that names no
// bundled file is rewritten to the artifact route, which is the route the
// relations rail already builds for the same target.
//
// The pass runs on the sanitizer's output, so it only ever sees an anchor
// whose href the allowlist already admitted, and it rewrites that href to a
// hash route on this same document.

/** referenceScheme is the RFC 3986 scheme production. A reference carrying
 * one addresses something outside this registry and is left as authored. */
const referenceScheme = /^[a-z][a-z0-9+.\-]*:/i;

/** artifactReference returns the artifact ID a relative href names, or null
 * when the href names something this UI has no route for: a fragment, a
 * root-relative path, an absolute URL, a file the artifact bundles, or one of
 * the artifact's own manifest files. It mirrors the resolution order
 * `lint.prose_reference` applies, so the viewer routes exactly the references
 * the linter resolves to another artifact. */
function artifactReference(href: string, resources: ReadonlySet<string>): string | null {
  const trimmed = href.trim();
  if (trimmed === '' || trimmed.startsWith('#') || trimmed.startsWith('/') || referenceScheme.test(trimmed)) {
    return null;
  }
  // A query or a fragment is not part of an artifact ID, and neither is a
  // §4.7.6 version pin, which the linter strips before the catalog lookup.
  const target = trimmed.split(/[?#]/, 1)[0].replace(/^\.\//, '');
  if (target === '' || target === '.' || target === '..' || target.startsWith('../')) {
    return null;
  }
  if (resources.has(target) || target === 'ARTIFACT.md' || target === 'SKILL.md') {
    return null;
  }
  const pin = target.indexOf('@');
  const id = pin < 0 ? target : target.slice(0, pin);
  return id === '' ? null : id;
}

/** routeArtifactReferences rewrites every cross-artifact prose reference in
 * the rendered body to the viewer's own route. */
function routeArtifactReferences(root: DocumentFragment, resources: ReadonlySet<string>): void {
  for (const anchor of root.querySelectorAll('a[href]')) {
    const id = artifactReference(anchor.getAttribute('href') ?? '', resources);
    if (id !== null) {
      anchor.setAttribute('href', artifactHref(id));
    }
  }
}

/**
 * renderArtifactBody renders an artifact's markdown body to sanitized markup.
 * The return value is the only markup this UI hands to the browser as markup,
 * and it is safe to insert because it has been through the sanitizer here.
 *
 * `resources` names the files the artifact bundles, which is what decides
 * whether a relative reference names a bundled file or another artifact.
 */
export function renderArtifactBody(body: string, resources: readonly string[] = []): string {
  const rendered = marked.parse(body, { async: false, gfm: true }) as string;
  const sanitized = DOMPurify.sanitize(rendered, {
    // The HTML profile drops SVG and MathML, which no markdown renderer
    // emits and which carry their own script-bearing constructs.
    USE_PROFILES: { html: true },
    ALLOWED_URI_REGEXP: allowedURI,
    // A stylesheet is not executable, but it is author-controlled markup on
    // the registry's origin and the viewer renders the body inside its own
    // layout, so the body does not get to restyle the page around it.
    // The form controls are dropped whole. No markdown renderer emits one, and
    // a body that carries one renders a working input on the registry's origin
    // that a reader cannot tell from the UI's own chrome, which is the
    // credential prompt this control exists to prevent.
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select', 'option'],
    FORBID_ATTR: ['style'],
    // The fragment is taken rather than the string so the pass below runs on
    // the sanitizer's own output. Nothing between the two adds an element or
    // an attribute the sanitizer did not pass.
    RETURN_DOM_FRAGMENT: true,
  });
  replaceStrippedImages(sanitized);
  routeArtifactReferences(sanitized, new Set(resources));
  const holder = document.createElement('div');
  holder.append(sanitized);
  return holder.innerHTML;
}

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

// The class the note left in place of an embedded document carries. An
// `iframe` or an `embed` draws nothing of its own once its source is gone, so
// without a note the paragraph an author wrote it in disappears and the
// reader cannot tell a refusal from a body that failed to ingest. The note
// names the removal in the same terms the stripped link and the stripped
// image use.
const strippedEmbedClass = 'embed-stripped';

/** clearMarkers strips every removal marker from a node, so a body that
 * writes one on a live element of its own cannot pass that element off as a
 * neutralized one. */
function clearMarkers(node: Element): void {
  for (const marker of [strippedLinkClass, strippedImageClass, strippedEmbedClass]) {
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

// The embedded-document elements a body can carry. The sanitizer's HTML
// profile drops both outright, which leaves nothing where the author wrote
// one, so they are admitted as tags and replaced here instead. Admitting them
// costs nothing: the hook above removes the attributes a browser fetches on
// its own, this pass removes the elements themselves before the markup
// leaves the module, and the fragment they pass through is detached, so
// neither one is ever laid out.
const embedTags = ['iframe', 'embed'];

/** replaceStrippedEmbeds replaces every embedded document by a note holding
 * its title, which is the only text such an element carries. The replacement
 * is unconditional, so no embedded document survives this pass whatever the
 * sanitizer left on it.
 *
 * The pass runs on the sanitizer's output for the reason
 * `replaceStrippedImages` does: the hook clears the marker classes from every
 * node it visits and would clear the marker off the note. */
function replaceStrippedEmbeds(root: DocumentFragment): void {
  for (const embed of root.querySelectorAll(embedTags.join(','))) {
    const note = document.createElement('span');
    note.className = strippedEmbedClass;
    note.textContent = embed.getAttribute('title') ?? '';
    embed.replaceWith(note);
  }
}

// A §4.4 prose reference is written as an ordinary relative markdown link,
// and `lint.prose_reference` admits one only when it resolves to a file the
// artifact bundles or to another artifact's canonical ID. Left as authored,
// the browser resolves either form against the `/ui/` mount, where the
// registry serves no such path: following it leaves the SPA for a plain-text
// 404 and the shell disappears. So both forms are resolved here to something
// the viewer answers itself. A reference that names another artifact is
// rewritten to the artifact route, which is the route the relations rail
// already builds for the same target. A reference that names a bundled file
// becomes a control that opens the Resources tab on that file, which is the
// viewer's only delivery of it: the registry serves no per-artifact asset
// route, and the file's own bytes reach the reader through that tab's
// download.
//
// The pass runs on the sanitizer's output, so it only ever sees an anchor
// whose href the allowlist already admitted, and what it writes is a hash
// route on this same document or a control that navigates nowhere.

/** referenceScheme is the RFC 3986 scheme production. A reference carrying
 * one addresses something outside this registry and is left as authored. */
const referenceScheme = /^[a-z][a-z0-9+.\-]*:/i;

/** Reference is what a relative prose reference resolves to: another
 * artifact the viewer has a route for, or a file this artifact bundles. */
type Reference = { kind: 'artifact'; id: string } | { kind: 'resource'; name: string };

/** resolveReference returns what a relative href names, or null when the href
 * names something this UI has no answer for: a fragment, a root-relative
 * path, an absolute URL, or one of the artifact's own manifest files. It
 * mirrors the resolution order `lint.prose_reference` applies, so the viewer
 * resolves exactly the references the linter admits. */
function resolveReference(href: string, resources: ReadonlySet<string>): Reference | null {
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
  if (resources.has(target)) {
    return { kind: 'resource', name: target };
  }
  if (target === 'ARTIFACT.md' || target === 'SKILL.md') {
    return null;
  }
  const pin = target.indexOf('@');
  const id = pin < 0 ? target : target.slice(0, pin);
  return id === '' ? null : { kind: 'artifact', id };
}

/** resourceReferenceAttribute names the bundled file a reference control
 * opens. The viewer reads it off the activated control, so the attribute is
 * the contract between this module and the surface that renders its output. */
export const resourceReferenceAttribute = 'data-resource';

/** resourceReferenceClass is the class a reference control carries, which is
 * what draws it as the inline link the author wrote. */
const resourceReferenceClass = 'resource-reference';

/** resourceControl replaces one anchor by a button naming the bundled file it
 * referenced, keeping the text the author wrote inside it. A button rather
 * than an anchor, because the file has no address of its own on this origin
 * and the control acts on the page instead of navigating; it stays in the tab
 * order, so the reference is reachable by keyboard the way the anchor was. */
function resourceControl(anchor: Element, name: string): void {
  const control = document.createElement('button');
  control.type = 'button';
  control.className = resourceReferenceClass;
  control.setAttribute(resourceReferenceAttribute, name);
  control.append(...anchor.childNodes);
  anchor.replaceWith(control);
}

/** routeReferences resolves every prose reference in the rendered body to the
 * viewer's own answer for it. */
function routeReferences(root: DocumentFragment, resources: ReadonlySet<string>): void {
  for (const anchor of root.querySelectorAll('a[href]')) {
    const reference = resolveReference(anchor.getAttribute('href') ?? '', resources);
    if (reference === null) {
      continue;
    }
    if (reference.kind === 'artifact') {
      anchor.setAttribute('href', artifactHref(reference.id));
      continue;
    }
    resourceControl(anchor, reference.name);
  }
}

// A table and a code fence are the two constructs a body renders into a box
// that scrolls sideways rather than wrapping, so the columns past the box's
// edge and the rest of a long command are reachable only by scrolling that
// box. A pointer scrolls it; a keyboard reaches it only when the box is in
// the tab order, which is what the layer panel's `.table-scroll` container
// already does for the same reason. So each one is made focusable and named,
// and the arrow keys scroll it once it holds focus (WCAG 2.1.1).
//
// The name is derived rather than fixed, because a body can render several
// of these boxes and a reader tabbing through them is told which one holds
// focus. A fence carries its language in the `language-*` class the renderer
// writes on the inner `code` element.
const languageClass = /^language-([a-z0-9+#.\-]+)$/i;

/** codeBlockLabel names one code fence by the language it declares. */
function codeBlockLabel(pre: Element): string {
  const code = pre.querySelector('code');
  for (const name of code?.classList ?? []) {
    const match = languageClass.exec(name);
    if (match !== null) {
      return `${match[1]} code block`;
    }
  }
  return 'Code block';
}

/** tableLabel names one table by its first header row, so the reader tabbing
 * into it is told what it holds rather than only that it is a table. */
function tableLabel(table: Element): string {
  const headers = [...table.querySelectorAll('th')]
    .map((header) => header.textContent?.trim() ?? '')
    .filter((text) => text !== '');
  return headers.length === 0 ? 'Table' : `Table: ${headers.join(', ')}`;
}

/** markScrollableRegions puts every sideways-scrolling box in the rendered
 * body into the tab order under a name. */
function markScrollableRegions(root: DocumentFragment): void {
  for (const table of root.querySelectorAll('table')) {
    // The table is wrapped rather than named in place, because `role="region"`
    // on the element itself replaces the table semantics a screen reader
    // navigates the cells by. The wrapper is the scroll container, which is
    // what `.prose .table-scroll` styles.
    const wrapper = document.createElement('div');
    wrapper.className = 'table-scroll';
    nameRegion(wrapper, tableLabel(table));
    table.replaceWith(wrapper);
    wrapper.append(table);
  }
  for (const pre of root.querySelectorAll('pre')) {
    nameRegion(pre, codeBlockLabel(pre));
  }
}

/** nameRegion makes one element a focusable, named region. */
function nameRegion(element: Element, label: string): void {
  element.setAttribute('tabindex', '0');
  element.setAttribute('role', 'region');
  element.setAttribute('aria-label', label);
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
    // The embedded-document elements are admitted so `replaceStrippedEmbeds`
    // below can leave a note where the author wrote one. `srcdoc` is refused
    // with them: it carries a whole document inline rather than a URL, so no
    // attribute test reaches what it holds.
    ADD_TAGS: embedTags,
    FORBID_ATTR: ['style', 'srcdoc'],
    // The fragment is taken rather than the string so the pass below runs on
    // the sanitizer's own output. Nothing between the two adds an element or
    // an attribute the sanitizer did not pass.
    RETURN_DOM_FRAGMENT: true,
  });
  replaceStrippedImages(sanitized);
  replaceStrippedEmbeds(sanitized);
  routeReferences(sanitized, new Set(resources));
  markScrollableRegions(sanitized);
  const holder = document.createElement('div');
  holder.append(sanitized);
  return holder.innerHTML;
}

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
// Beyond that rule, the path drops the form controls and holds every
// attribute the browser fetches on its own to the registry's own origin, so
// an author can neither prompt a reader for a credential on the origin the
// session cookie is scoped to nor turn a view of an artifact into a request
// to a host the author picked.
//
// Frontmatter does not reach this path. It is parsed into a property table
// whose values render as text.

import DOMPurify from 'dompurify';
import { marked } from 'marked';

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

// The attributes that carry a single URL the browser fetches on its own as it
// lays the document out. A fetch the reader never asked for reaches the host
// it names with the reader's IP address, User-Agent, and Referer, so an
// author-controlled body does not get to name a foreign host on one of them.
const fetchAttributes = ['src', 'xlink:href', 'background', 'poster'];

// The attributes that carry a candidate list rather than a single URL, each
// of them a fetching attribute. A candidate is a URL followed by an optional
// descriptor, and the candidates are separated by commas, so a test that
// reads the whole value inspects the leading candidate alone and keeps every
// later one verbatim.
const candidateListAttributes = ['srcset', 'imagesrcset'];

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

// A URL that names a host resolves to a foreign origin, whether it spells the
// scheme out or leaves it to the document in the scheme-relative form. The
// two productions are the whole of what a value has to avoid to resolve
// against the registry's own origin.
const hostBearingURL = /^(?:[a-z][a-z0-9+.\-]*:|\/\/)/i;

/** allowsLocal reports whether one URL passes the allowlist and resolves
 * against the registry's own origin. It governs the attributes a browser
 * fetches without the reader acting, so a body cannot turn a view of an
 * artifact into a request to a host the author picked. */
function allowsLocal(url: string): boolean {
  const value = url.replace(attributeWhitespace, '');
  return allows(value) && !hostBearingURL.test(value);
}

/** allowsEveryCandidate reports whether every candidate in a candidate list
 * passes the test a fetching attribute carries, because every candidate list
 * is one. A candidate's descriptor is separated from its URL by
 * whitespace, which the test removes, so the descriptor joins the relative
 * path it follows and changes no verdict. A URL that itself carries a comma
 * splits into fragments that are each tested, so a data: URL whose payload
 * carries one fails on its own scheme-bearing fragment. The attribute is
 * dropped whole when any candidate fails, so a list is never rewritten into a
 * shorter one the author did not write. */
function allowsEveryCandidate(value: string): boolean {
  const candidates = value.split(',').filter((candidate) => candidate.trim() !== '');
  return candidates.length > 0 && candidates.every(allowsLocal);
}

// The allowlist above is expressed to the sanitizer as its URI pattern, but
// that pattern is one of several branches the sanitizer admits a URL on: it
// keeps a data: URL on a media element's source attribute whatever the
// pattern says, and it tests a candidate list by its leading candidate alone.
// The rule admits no scheme other than http, https, and mailto on any
// attribute the sanitizer keeps, so this hook re-tests every URL attribute
// that survived and drops the ones no scheme in the allowlist covers. It
// closes the element branches at once rather than naming the media elements,
// so a sanitizer release that adds one does not reopen them. The hook also
// holds the fetching attributes to the registry's own origin, which the
// sanitizer's URI pattern cannot express because it runs on every attribute
// alike and a link to a foreign host stays legitimate.
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
    const value = node.getAttribute(name);
    if (value !== null && !allowsLocal(value)) {
      node.removeAttribute(name);
    }
  }
  for (const name of candidateListAttributes) {
    const value = node.getAttribute(name);
    if (value !== null && !allowsEveryCandidate(value)) {
      node.removeAttribute(name);
    }
  }
  markStrippedLink(node);
});

// The class an anchor left without a destination carries. An anchor whose
// href the allowlist refused keeps its element and its text, and without a
// marker it draws in the link colour and invites a click that goes nowhere.
// The class drops it to body text and names the removal beside it, so the
// reader sees that the destination is gone rather than a link that is merely
// broken.
const strippedLinkClass = 'link-stripped';

/** markStrippedLink marks an anchor that carries no destination. The class is
 * stripped from every node first, so a body that writes the class on a live
 * link of its own cannot pass that link off as a neutralized one. */
function markStrippedLink(node: Element): void {
  if (node.classList.contains(strippedLinkClass)) {
    node.classList.remove(strippedLinkClass);
    if (node.classList.length === 0) {
      node.removeAttribute('class');
    }
  }
  if (node.tagName === 'A' && !node.hasAttribute('href')) {
    node.classList.add(strippedLinkClass);
  }
}

/**
 * renderArtifactBody renders an artifact's markdown body to sanitized markup.
 * The return value is the only markup this UI hands to the browser as markup,
 * and it is safe to insert because it has been through the sanitizer here.
 */
export function renderArtifactBody(body: string): string {
  const rendered = marked.parse(body, { async: false, gfm: true }) as string;
  return DOMPurify.sanitize(rendered, {
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
  });
}

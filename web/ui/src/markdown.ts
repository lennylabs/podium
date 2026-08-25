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
//
// Frontmatter does not reach this path. It is parsed into a property table
// whose values render as text.

import DOMPurify from 'dompurify';
import { marked } from 'marked';

// allowedURI admits an http, https, or mailto URL and a URL carrying no
// scheme at all, which is what a relative link inside a manifest looks like.
// Every other scheme, javascript: and data: among them, fails the test and
// the sanitizer drops the attribute carrying it. The trailing alternatives
// are the relative forms: a value whose first character cannot open a scheme,
// and a value whose leading scheme-shaped run is not terminated by a colon.
const allowedURI = /^(?:(?:https?|mailto):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i;

// The attributes that carry a single URL on the markup a markdown renderer
// emits, plus the form attributes the HTML profile would otherwise keep.
// Every one of them is re-tested against the allowlist below.
const urlAttributes = ['href', 'src', 'xlink:href', 'action', 'formaction', 'background', 'poster'];

// The attributes that carry a candidate list rather than a single URL. Each
// candidate is a URL followed by an optional descriptor, and the candidates
// are separated by commas, so a test that reads the whole value inspects the
// leading candidate alone and keeps every later one verbatim.
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

/** allowsEveryCandidate reports whether every candidate in a candidate list
 * passes the allowlist. A candidate's descriptor is separated from its URL by
 * whitespace, which the test removes, so the descriptor joins the relative
 * path it follows and changes no verdict. A URL that itself carries a comma
 * splits into fragments that are each tested, so a data: URL whose payload
 * carries one fails on its own scheme-bearing fragment. The attribute is
 * dropped whole when any candidate fails, so a list is never rewritten into a
 * shorter one the author did not write. */
function allowsEveryCandidate(value: string): boolean {
  const candidates = value.split(',').filter((candidate) => candidate.trim() !== '');
  return candidates.length > 0 && candidates.every(allows);
}

// The allowlist above is expressed to the sanitizer as its URI pattern, but
// that pattern is one of several branches the sanitizer admits a URL on: it
// keeps a data: URL on a media element's source attribute whatever the
// pattern says, and it tests a candidate list by its leading candidate alone.
// The rule admits no scheme other than http, https, and mailto on any
// attribute the sanitizer keeps, so this hook re-tests every URL attribute
// that survived and drops the ones no scheme in the allowlist covers. It
// closes the element branches at once rather than naming the media elements,
// so a sanitizer release that adds one does not reopen them.
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (!(node instanceof Element)) {
    return;
  }
  for (const name of urlAttributes) {
    const value = node.getAttribute(name);
    if (value !== null && !allows(value)) {
      node.removeAttribute(name);
    }
  }
  for (const name of candidateListAttributes) {
    const value = node.getAttribute(name);
    if (value !== null && !allowsEveryCandidate(value)) {
      node.removeAttribute(name);
    }
  }
});

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
    FORBID_TAGS: ['style'],
    FORBID_ATTR: ['style'],
  });
}

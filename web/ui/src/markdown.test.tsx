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
    ];
    for (const body of bodies) {
      const container = renderBody(body);
      expect(attributeValues(container).some((value) => value.toLowerCase().includes('data:'))).toBe(false);
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

  it('renders a markup-carrying frontmatter value as literal text', () => {
    const container = render(<PropertyTable raw={'title: <img src=x onerror="window.hijacked=1">\n'} />).container;
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('[onerror]')).toBeNull();
    expect(container.textContent).toContain('<img src=x onerror="window.hijacked=1">');
  });
});

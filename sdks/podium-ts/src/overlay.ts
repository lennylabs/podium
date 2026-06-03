// Workspace local overlay merge for the SDK (spec §6.4, §6.4.1).
//
// The overlay is the highest-precedence layer in the caller's effective
// view, merged client-side. search_artifacts fans out to both the registry
// and a local BM25 index over overlay manifests and fuses the two via
// reciprocal rank fusion; load_artifact resolves an overlay artifact ahead
// of the registry. Node's fs/path load lazily so the SDK stays importable
// in edge bundles that never touch the overlay.

const OVERLAY_SUBDIR = ".podium/overlay";
const WORD_RE = /[a-z0-9]+/g;

function tokenize(text: string): string[] {
  return text.toLowerCase().match(WORD_RE) ?? [];
}

// resolveOverlayPath applies the §6.4 lookup order: an explicit
// Client(overlayPath) wins over PODIUM_OVERLAY_PATH, which wins over the
// <CWD>/.podium/overlay/ fallback. Returns null when the layer is disabled.
export async function resolveOverlayPath(
  explicit: string | undefined,
  env: string | undefined,
  cwd: string,
): Promise<string | null> {
  if (explicit) return explicit;
  if (env) return env;
  const fs = await import("node:fs/promises");
  const path = await import("node:path");
  const candidate = path.join(cwd || ".", OVERLAY_SUBDIR);
  try {
    const st = await fs.stat(candidate);
    if (st.isDirectory()) return candidate;
  } catch {
    // no CWD overlay; layer disabled
  }
  return null;
}

interface FrontmatterFields {
  type: string;
  version: string;
  description: string;
  tags: string[];
}

function parseFrontmatter(text: string): { fields: FrontmatterFields; body: string } {
  const fields: FrontmatterFields = { type: "", version: "", description: "", tags: [] };
  if (!text.startsWith("---")) return { fields, body: text };
  const lines = text.split(/\r?\n/);
  let end = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === "---") {
      end = i;
      break;
    }
  }
  if (end === -1) return { fields, body: text };
  const body = lines.slice(end + 1).join("\n");
  let pendingTags = false;
  const tags: string[] = [];
  const unquote = (s: string): string => s.trim().replace(/^['"]|['"]$/g, "");
  for (const raw of lines.slice(1, end)) {
    if (pendingTags && raw.trimStart().startsWith("- ")) {
      tags.push(unquote(raw.trimStart().slice(2)));
      continue;
    }
    pendingTags = false;
    if (raw.startsWith(" ") || !raw.trim() || raw.trimStart().startsWith("#")) continue;
    const sep = raw.indexOf(":");
    if (sep === -1) continue;
    const key = raw.slice(0, sep).trim();
    const val = raw.slice(sep + 1).trim();
    if (key === "tags") {
      if (val.startsWith("[") && val.endsWith("]")) {
        for (const t of val.slice(1, -1).split(",")) {
          if (t.trim()) tags.push(unquote(t));
        }
      } else if (val) {
        for (const t of val.split(",")) if (t.trim()) tags.push(unquote(t));
      } else {
        pendingTags = true;
      }
    } else if (key === "type" || key === "version" || key === "description") {
      fields[key] = unquote(val);
    }
  }
  fields.tags = tags.filter(Boolean);
  return { fields, body };
}

export interface OverlayArtifact {
  id: string;
  type: string;
  version: string;
  description: string;
  tags: string[];
  frontmatter: string;
  body: string;
  skillRaw: string;
  resources: Record<string, string>;
  tokens: string[];
}

// OverlayDomain holds the workspace-overlay DOMAIN.md fields the §4.5.4
// load_domain merge reads: the frontmatter description, the prose body, the
// include/exclude globs, the unlisted flag, and the discovery knobs (keywords,
// featured, notable_count). It mirrors pkg/manifest.Domain as the merge
// consumes it.
export interface OverlayDomain {
  description: string;
  body: string;
  include: string[];
  exclude: string[];
  unlisted: boolean;
  keywords: string[];
  featured: string[];
  notableCount: number;
}

// parseDomainFrontmatter hand-parses a DOMAIN.md frontmatter block the same way
// parseFrontmatter reads an ARTIFACT.md, with no YAML dependency. It reads the
// top-level description, unlisted (bool), include/exclude lists (inline
// `[a, b]` and block `- x` forms), and the nested discovery: block's keywords,
// featured, and notable_count. The body is everything after the closing `---`.
// Returns null for a malformed file (no frontmatter) so the caller can skip it.
function parseDomainFrontmatter(text: string): OverlayDomain | null {
  if (!text.startsWith("---")) return null;
  const lines = text.split(/\r?\n/);
  let end = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === "---") {
      end = i;
      break;
    }
  }
  if (end === -1) return null;
  const body = lines.slice(end + 1).join("\n");
  const dom: OverlayDomain = {
    description: "",
    body,
    include: [],
    exclude: [],
    unlisted: false,
    keywords: [],
    featured: [],
    notableCount: 0,
  };
  const unquote = (s: string): string => s.trim().replace(/^['"]|['"]$/g, "");
  const inlineList = (val: string): string[] => {
    const out: string[] = [];
    if (val.startsWith("[") && val.endsWith("]")) {
      for (const t of val.slice(1, -1).split(",")) {
        if (t.trim()) out.push(unquote(t));
      }
    }
    return out;
  };
  // indentOf returns the leading-space count of a line.
  const indentOf = (s: string): number => s.length - s.trimStart().length;
  // pending tracks which list a `- x` block item appends to. discovery tracks
  // whether the cursor is inside the nested discovery: block.
  let pending: "include" | "exclude" | "keywords" | "featured" | null = null;
  let inDiscovery = false;
  for (const raw of lines.slice(1, end)) {
    const trimmed = raw.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const indent = indentOf(raw);
    // A block list item appends to the pending list.
    if (pending && trimmed.startsWith("- ")) {
      const item = unquote(trimmed.slice(2));
      if (item) {
        if (pending === "include") dom.include.push(item);
        else if (pending === "exclude") dom.exclude.push(item);
        else if (pending === "keywords") dom.keywords.push(item);
        else dom.featured.push(item);
      }
      continue;
    }
    pending = null;
    const sep = raw.indexOf(":");
    if (sep === -1) continue;
    const key = raw.slice(0, sep).trim();
    const val = raw.slice(sep + 1).trim();
    if (indent === 0) {
      // A new top-level key closes the discovery: block.
      inDiscovery = false;
      if (key === "description") {
        dom.description = unquote(val);
      } else if (key === "unlisted") {
        dom.unlisted = unquote(val).toLowerCase() === "true";
      } else if (key === "include") {
        if (val) dom.include.push(...inlineList(val));
        else pending = "include";
      } else if (key === "exclude") {
        if (val) dom.exclude.push(...inlineList(val));
        else pending = "exclude";
      } else if (key === "discovery") {
        inDiscovery = true;
      }
      continue;
    }
    // Nested keys belong to the discovery: block.
    if (inDiscovery) {
      if (key === "keywords") {
        if (val) dom.keywords.push(...inlineList(val));
        else pending = "keywords";
      } else if (key === "featured") {
        if (val) dom.featured.push(...inlineList(val));
        else pending = "featured";
      } else if (key === "notable_count") {
        const n = parseInt(unquote(val), 10);
        if (Number.isFinite(n)) dom.notableCount = n;
      }
    }
  }
  return dom;
}

// match reports whether the §4.5.2 glob pattern matches the canonical artifact
// id. `*` matches one path segment, `**` matches zero or more segments
// (recursive), `{a,b,c}` matches any alternative. Mirrors pkg/domain.Match.
export function match(pattern: string, id: string): boolean {
  for (const alt of expandAlternatives(pattern)) {
    if (matchSegments(alt.split("/"), id.split("/"))) return true;
  }
  return false;
}

// matchAny reports whether any non-empty pattern matches id.
export function matchAny(patterns: string[], id: string): boolean {
  return patterns.some((p) => p !== "" && match(p, id));
}

// resolveImports computes the §4.5.2 import set: every id in ids matching an
// include pattern, with anything matching an exclude pattern removed (exclude
// after include). The result preserves the input order of ids.
export function resolveImports(include: string[], exclude: string[], ids: string[]): string[] {
  return ids.filter((id) => matchAny(include, id) && !matchAny(exclude, id));
}

// fallbackDescription synthesizes the §4.5.5 description fallback for a domain
// with no DOMAIN.md description: the directory basename, de-slugged (hyphens
// and underscores become spaces) and title-cased. Mirrors
// pkg/domain.FallbackDescription.
export function fallbackDescription(path: string): string {
  let base = path;
  const i = path.lastIndexOf("/");
  if (i >= 0) base = path.slice(i + 1);
  base = base.replace(/-/g, " ").replace(/_/g, " ");
  return base
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

// globLiteralPrefix returns the leading literal path segments of a §4.5.2 glob,
// stopping at the first segment carrying a glob metacharacter. It scopes a
// catalog fetch to the slice an include pattern can match
// ("finance/ap/*" -> "finance/ap"); a leading glob yields "" (the whole
// catalog). Mirrors cmd/podium-mcp.globLiteralPrefix.
export function globLiteralPrefix(pattern: string): string {
  const out: string[] = [];
  for (const seg of pattern.split("/")) {
    if (/[*{},]/.test(seg)) break;
    out.push(seg);
  }
  return out.join("/");
}

// expandAlternatives expands a single `{a,b,c}` group into its alternatives,
// recursing so multiple groups all expand. A pattern with no group returns
// itself.
function expandAlternatives(pattern: string): string[] {
  const open = pattern.indexOf("{");
  if (open < 0) return [pattern];
  const rel = pattern.slice(open).indexOf("}");
  if (rel < 0) return [pattern];
  const closeIdx = rel + open;
  const prefix = pattern.slice(0, open);
  const suffix = pattern.slice(closeIdx + 1);
  const out: string[] = [];
  for (const choice of pattern.slice(open + 1, closeIdx).split(",")) {
    out.push(...expandAlternatives(prefix + choice + suffix));
  }
  return out;
}

// matchSegments matches pattern segments against target segments, honoring `*`
// (one segment) and `**` (zero or more segments).
function matchSegments(pat: string[], tgt: string[]): boolean {
  for (let i = 0; i < pat.length; i++) {
    const seg = pat[i];
    if (seg === "**") {
      if (i === pat.length - 1) return true;
      const rest = pat.slice(i + 1);
      for (let j = 0; j <= tgt.length; j++) {
        if (matchSegments(rest, tgt.slice(j))) return true;
      }
      return false;
    }
    if (i >= tgt.length) return false;
    if (seg !== "*" && seg !== tgt[i]) return false;
  }
  return pat.length === tgt.length;
}

// LocalOverlay indexes an overlay directory and ranks it with BM25 (§6.4.1).
export class LocalOverlay {
  // spec §6.4.1 — BM25 is the default overlay ranker (standard Okapi params).
  private static readonly K1 = 1.2;
  private static readonly B = 0.75;

  readonly artifacts = new Map<string, OverlayArtifact>();
  // §4.5.4 — every DOMAIN.md under the overlay keyed by canonical domain path
  // (the dir relative to the overlay root, "/"-joined; "" for the root level).
  readonly domains = new Map<string, OverlayDomain>();
  private avgLen = 0;
  private readonly docFreq = new Map<string, number>();

  private constructor() {}

  static async load(overlayPath: string): Promise<LocalOverlay> {
    const overlay = new LocalOverlay();
    if (!overlayPath) return overlay;
    const fs = await import("node:fs/promises");
    const path = await import("node:path");
    let exists = false;
    try {
      exists = (await fs.stat(overlayPath)).isDirectory();
    } catch {
      exists = false;
    }
    if (!exists) return overlay;

    // Walk for directories that directly contain an ARTIFACT.md or a
    // DOMAIN.md. The canonical key is the dir relative to the overlay root
    // ("/"-joined); the root level is keyed "".
    const walk = async (dir: string): Promise<void> => {
      const entries = await fs.readdir(dir, { withFileTypes: true });
      const rel = path.relative(overlayPath, dir).split(path.sep).join("/");
      const key = rel === "." ? "" : rel;
      const hasManifest = entries.some((e) => e.isFile() && e.name === "ARTIFACT.md");
      if (hasManifest && key) {
        const art = await overlay.readPackage(fs, path, dir, key);
        if (art) overlay.artifacts.set(key, art);
      }
      if (entries.some((e) => e.isFile() && e.name === "DOMAIN.md")) {
        try {
          const text = await fs.readFile(path.join(dir, "DOMAIN.md"), "utf-8");
          const dom = parseDomainFrontmatter(text);
          if (dom) overlay.domains.set(key, dom);
        } catch {
          // skip a malformed or unreadable DOMAIN.md
        }
      }
      for (const e of entries) {
        if (e.isDirectory()) await walk(path.join(dir, e.name));
      }
    };
    await walk(overlayPath);
    overlay.index();
    return overlay;
  }

  private async readPackage(
    fs: typeof import("node:fs/promises"),
    path: typeof import("node:path"),
    dir: string,
    id: string,
  ): Promise<OverlayArtifact | null> {
    let frontmatter: string;
    try {
      frontmatter = await fs.readFile(path.join(dir, "ARTIFACT.md"), "utf-8");
    } catch {
      return null;
    }
    const { fields, body } = parseFrontmatter(frontmatter);
    let skillRaw = "";
    if (fields.type === "skill") {
      try {
        skillRaw = await fs.readFile(path.join(dir, "SKILL.md"), "utf-8");
      } catch {
        skillRaw = "";
      }
    }
    const skip = new Set(["ARTIFACT.md"]);
    if (fields.type === "skill") skip.add("SKILL.md");
    const resources: Record<string, string> = {};
    const collect = async (sub: string): Promise<void> => {
      for (const e of await fs.readdir(sub, { withFileTypes: true })) {
        const full = path.join(sub, e.name);
        if (e.isDirectory()) {
          await collect(full);
          continue;
        }
        const rel = path.relative(dir, full).split(path.sep).join("/");
        if (skip.has(rel)) continue;
        try {
          resources[rel] = await fs.readFile(full, "utf-8");
        } catch {
          resources[rel] = "";
        }
      }
    };
    await collect(dir);
    const text = [id, fields.type, fields.description, fields.tags.join(" "), body].join(" ");
    return {
      id,
      type: fields.type,
      version: fields.version,
      description: fields.description,
      tags: fields.tags,
      frontmatter,
      body,
      skillRaw,
      resources,
      tokens: tokenize(text),
    };
  }

  private index(): void {
    if (this.artifacts.size === 0) return;
    let total = 0;
    for (const art of this.artifacts.values()) {
      total += art.tokens.length;
      for (const term of new Set(art.tokens)) {
        this.docFreq.set(term, (this.docFreq.get(term) ?? 0) + 1);
      }
    }
    this.avgLen = total / this.artifacts.size;
  }

  private bm25(queryTokens: string[], art: OverlayArtifact): number {
    if (queryTokens.length === 0 || art.tokens.length === 0) return 0;
    const n = this.artifacts.size;
    const dl = art.tokens.length;
    const counts = new Map<string, number>();
    for (const t of art.tokens) counts.set(t, (counts.get(t) ?? 0) + 1);
    let score = 0;
    for (const term of queryTokens) {
      const tf = counts.get(term) ?? 0;
      if (tf === 0) continue;
      const df = this.docFreq.get(term) ?? 0;
      const idf = Math.log(1 + (n - df + 0.5) / (df + 0.5));
      const denom = tf + LocalOverlay.K1 * (1 - LocalOverlay.B + (LocalOverlay.B * dl) / (this.avgLen || 1));
      score += (idf * (tf * (LocalOverlay.K1 + 1))) / denom;
    }
    return score;
  }

  // spec §6.4: the overlay is the highest-precedence layer in the caller's
  // effective view, so a scoped query excludes overlay artifacts whose id
  // falls outside the requested domain path. The `scope` prefix match mirrors
  // the MCP server's overlay filter (cmd/podium-mcp/local_search.go).
  search(
    query: string,
    opts: { type?: string; scope?: string; tags?: string[]; topK?: number } = {},
  ): OverlayArtifact[] {
    const topK = opts.topK ?? 10;
    const wantTags = new Set(opts.tags ?? []);
    const candidates = [...this.artifacts.values()].filter(
      (a) =>
        (!opts.type || a.type === opts.type) &&
        (!opts.scope || a.id.startsWith(opts.scope)) &&
        (wantTags.size === 0 || [...wantTags].every((t) => a.tags.includes(t))),
    );
    if (query) {
      const q = tokenize(query);
      return candidates
        .map((a) => ({ score: this.bm25(q, a), a }))
        .filter((p) => p.score > 0)
        .sort((x, y) => y.score - x.score || (x.a.id < y.a.id ? -1 : 1))
        .slice(0, topK)
        .map((p) => p.a);
    }
    return candidates.sort((x, y) => (x.id < y.id ? -1 : 1)).slice(0, topK);
  }

  get(id: string): OverlayArtifact | undefined {
    return this.artifacts.get(id);
  }
}

// rrfFuse performs reciprocal rank fusion over ranked id lists (§6.4.1):
// the fused score of an id is the sum of 1 / (k + rank) across the lists
// that contain it (rank 0 is best).
export function rrfFuse(rankedLists: string[][], k = 60): Map<string, number> {
  const scores = new Map<string, number>();
  for (const ranked of rankedLists) {
    ranked.forEach((id, rank) => {
      scores.set(id, (scores.get(id) ?? 0) + 1 / (k + rank + 1));
    });
  }
  return scores;
}

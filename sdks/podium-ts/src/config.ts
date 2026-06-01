// Client-side sync.yaml registry resolution (spec §7.5.2, §13.10).
//
// The SDK keeps no YAML dependency, so this reads only the single
// `defaults:` mapping the client needs. It mirrors the Go client's
// merged-config lookup: the registry resolves from PODIUM_REGISTRY first,
// then the project-local, project-shared, and user-global sync.yaml scopes
// in descending precedence. Node's fs/path load lazily so importing the
// SDK stays safe in edge bundles that never call fromEnv.

const PODIUM_DIR = ".podium";

// discoverWorkspace walks up from start to the first directory holding a
// .podium/ directory (spec §7.5.2). Returns null when none is found.
export async function discoverWorkspace(start: string): Promise<string | null> {
  if (!start) return null;
  const fs = await import("node:fs/promises");
  const path = await import("node:path");
  let cur = path.resolve(start);
  for (;;) {
    try {
      const st = await fs.stat(path.join(cur, PODIUM_DIR));
      if (st.isDirectory()) return cur;
    } catch {
      // not here; keep walking up
    }
    const parent = path.dirname(cur);
    if (parent === cur) return null;
    cur = parent;
  }
}

// scalarValue decodes a YAML scalar: strips quotes and any inline comment.
function scalarValue(value: string): string {
  const v = value.trim();
  if (v && (v[0] === '"' || v[0] === "'")) {
    const quote = v[0];
    const end = v.indexOf(quote, 1);
    return end !== -1 ? v.slice(1, end) : v.slice(1);
  }
  // An unquoted inline comment starts at " #" per YAML; a registry URL never
  // contains a bare " #", so this is safe for the defaults block.
  const idx = v.indexOf(" #");
  return (idx !== -1 ? v.slice(0, idx) : v).trim();
}

// parseDefaults extracts the top-level `defaults:` mapping from a document.
export function parseDefaults(text: string): Record<string, string> {
  const defaults: Record<string, string> = {};
  let inDefaults = false;
  let baseIndent: number | null = null;
  for (const raw of text.split(/\r?\n/)) {
    if (!raw.trim() || raw.trimStart().startsWith("#")) continue;
    const indent = raw.length - raw.trimStart().length;
    const stripped = raw.trim();
    if (indent === 0) {
      inDefaults = stripped.startsWith("defaults:");
      baseIndent = null;
      continue;
    }
    if (!inDefaults) continue;
    if (baseIndent === null) baseIndent = indent;
    if (indent < baseIndent) {
      inDefaults = false;
      continue;
    }
    const sep = stripped.indexOf(":");
    if (sep === -1) continue;
    defaults[stripped.slice(0, sep).trim()] = scalarValue(stripped.slice(sep + 1));
  }
  return defaults;
}

async function readRegistry(file: string): Promise<string> {
  const fs = await import("node:fs/promises");
  let text: string;
  try {
    text = await fs.readFile(file, "utf-8");
  } catch {
    return "";
  }
  return parseDefaults(text).registry ?? "";
}

// resolveRegistry resolves the registry across all §7.5.2 scopes.
// Precedence (highest first): PODIUM_REGISTRY, the workspace
// .podium/sync.local.yaml, the workspace .podium/sync.yaml, and the
// user-global ~/.podium/sync.yaml. Returns "" when unset everywhere.
export async function resolveRegistry(
  envRegistry: string | undefined,
  cwd: string,
  home: string | undefined,
): Promise<string> {
  if (envRegistry) return envRegistry;
  const path = await import("node:path");
  const candidates: string[] = [];
  const workspace = await discoverWorkspace(cwd);
  if (workspace) {
    candidates.push(path.join(workspace, PODIUM_DIR, "sync.local.yaml"));
    candidates.push(path.join(workspace, PODIUM_DIR, "sync.yaml"));
  }
  if (home) candidates.push(path.join(home, PODIUM_DIR, "sync.yaml"));
  for (const file of candidates) {
    const reg = await readRegistry(file);
    if (reg) return reg;
  }
  return "";
}

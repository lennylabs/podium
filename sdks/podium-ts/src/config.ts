// Client-side sync.yaml resolution (spec §7.5.2, §13.10) and the §4.7.9
// verification policy and key set (spec §7.6.3).
//
// The SDK keeps no YAML dependency, so this reads only the single
// `defaults:` mapping the client needs. It mirrors the Go client's
// merged-config lookup: the registry resolves from PODIUM_REGISTRY first,
// then the project-local, project-shared, and user-global sync.yaml scopes
// in descending precedence. Node's fs/path load lazily so importing the
// SDK stays safe in edge bundles that never call fromEnv.

import {
  CODE_UNAVAILABLE,
  POLICIES,
  parseKeyFile,
  parseVerifyKeyList,
  type Verification,
  type VerifyPolicy,
} from "./delivery.js";
import { RegistryError } from "./errors.js";

const PODIUM_DIR = ".podium";

// The registry-managed key file's default location below the home directory.
const DEFAULT_KEY_PATH = [PODIUM_DIR, "standalone", "registry-signing.key"];

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

type FsModule = typeof import("node:fs/promises");
type PathModule = typeof import("node:path");

// readDefaults returns the `defaults:` mapping of one sync.yaml file, or {}
// when the file cannot be read.
async function readDefaults(fs: FsModule, file: string): Promise<Record<string, string>> {
  let text: string;
  try {
    text = await fs.readFile(file, "utf-8");
  } catch {
    return {};
  }
  return parseDefaults(text);
}

// scopePaths lists the §7.5.2 sync.yaml files, highest precedence first: the
// workspace .podium/sync.local.yaml, the workspace .podium/sync.yaml, and the
// user-global ~/.podium/sync.yaml.
async function scopePaths(path: PathModule, cwd: string, home: string | undefined): Promise<string[]> {
  const candidates: string[] = [];
  const workspace = await discoverWorkspace(cwd);
  if (workspace) {
    candidates.push(path.join(workspace, PODIUM_DIR, "sync.local.yaml"));
    candidates.push(path.join(workspace, PODIUM_DIR, "sync.yaml"));
  }
  if (home) candidates.push(path.join(home, PODIUM_DIR, "sync.yaml"));
  return candidates;
}

// firstDefault returns the first non-empty defaults.<key> across the scopes,
// with the path of the file that carried it.
async function firstDefault(
  fs: FsModule,
  path: PathModule,
  key: string,
  cwd: string,
  home: string | undefined,
): Promise<{ value: string; file: string } | null> {
  for (const file of await scopePaths(path, cwd, home)) {
    const value = (await readDefaults(fs, file))[key] ?? "";
    if (value) return { value, file };
  }
  return null;
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
  const fs = await import("node:fs/promises");
  const path = await import("node:path");
  return (await firstDefault(fs, path, "registry", cwd, home))?.value ?? "";
}

// VerificationOptions are the client's own explicit delivery-check settings.
export interface VerificationOptions {
  verifySignatures?: string;
  verifyKeys?: string;
}

type Env = Record<string, string | undefined>;

interface NodeFs {
  fs: FsModule;
  path: PathModule;
}

// loadNodeFs loads node:fs/promises and node:path, or returns null when the
// runtime has neither. A runtime without them has no filesystem, so no
// sync.yaml scope and no key file exists there.
async function loadNodeFs(): Promise<NodeFs | null> {
  try {
    const fs = await import("node:fs/promises");
    const path = await import("node:path");
    return { fs, path };
  } catch {
    return null;
  }
}

function checkedPolicy(value: string, source: string): VerifyPolicy {
  if (!(POLICIES as readonly string[]).includes(value)) {
    throw new Error(`${source} must be never or always, got ${JSON.stringify(value)}`);
  }
  return value as VerifyPolicy;
}

// keyConfigured reports whether a verification key is configured for the SDK
// default. It fails closed: a key list, a non-empty
// PODIUM_SIGNATURE_VERIFY_KEY, a non-empty PODIUM_SIGN_KEY_PATH, or a file
// present at the default key path counts, whether or not the material decodes.
async function keyConfigured(opts: VerificationOptions, env: Env, home: string | undefined, node: NodeFs | null) {
  if (opts.verifyKeys || env.PODIUM_SIGNATURE_VERIFY_KEY || env.PODIUM_SIGN_KEY_PATH) return true;
  if (!node || !home) return false;
  try {
    await node.fs.lstat(node.path.join(home, ...DEFAULT_KEY_PATH));
    return true;
  } catch {
    return false;
  }
}

// resolvePolicy resolves the §4.7.9 policy in the SDK order: the client's
// verifySignatures, PODIUM_VERIFY_SIGNATURES, defaults.verify_signatures across
// the §7.5.2 scopes, and then the SDK default, which is always when a key is
// configured and never otherwise. An invalid value throws an Error naming its
// source and is never treated as unset. A never from sync.yaml emits the
// stale-never warning through process.emitWarning.
async function resolvePolicy(
  opts: VerificationOptions,
  env: Env,
  cwd: string,
  home: string | undefined,
  node: NodeFs | null,
): Promise<VerifyPolicy> {
  if (opts.verifySignatures !== undefined) return checkedPolicy(opts.verifySignatures, "verifySignatures");
  if (env.PODIUM_VERIFY_SIGNATURES) {
    return checkedPolicy(env.PODIUM_VERIFY_SIGNATURES, "PODIUM_VERIFY_SIGNATURES");
  }
  const setting = node ? await firstDefault(node.fs, node.path, "verify_signatures", cwd, home) : null;
  if (setting) {
    const policy = checkedPolicy(setting.value, `defaults.verify_signatures in ${setting.file}`);
    if (policy === "never") {
      // Reached only after a sync.yaml read succeeded, so a Node runtime.
      globalThis.process?.emitWarning(
        `signature verification is off because defaults.verify_signatures is never in ${setting.file}; ` +
          "remove defaults.verify_signatures from that file to verify under the SDK default (§4.7.9)",
      );
    }
    return policy;
  }
  return (await keyConfigured(opts, env, home, node)) ? "always" : "never";
}

// resolveKeys resolves the §4.7.9 key set: the verifyKeys option, then
// PODIUM_SIGNATURE_VERIFY_KEY, then the key file at PODIUM_SIGN_KEY_PATH or
// the default path. Every failure names the source it tried.
async function resolveKeys(
  opts: VerificationOptions,
  env: Env,
  home: string | undefined,
  node: NodeFs | null,
): Promise<Uint8Array[]> {
  let source: string;
  let load: () => Promise<Uint8Array[]>;
  if (opts.verifyKeys) {
    const value = opts.verifyKeys;
    source = "verifyKeys";
    load = async () => parseVerifyKeyList(value);
  } else if (env.PODIUM_SIGNATURE_VERIFY_KEY) {
    const value = env.PODIUM_SIGNATURE_VERIFY_KEY;
    source = "PODIUM_SIGNATURE_VERIFY_KEY";
    load = async () => parseVerifyKeyList(value);
  } else {
    const file = env.PODIUM_SIGN_KEY_PATH || (node && home ? node.path.join(home, ...DEFAULT_KEY_PATH) : "");
    source = `key file ${file || "(no home directory)"} (PODIUM_SIGN_KEY_PATH)`;
    load = async () => {
      if (!node || !file) throw new RegistryError(CODE_UNAVAILABLE, "no filesystem to read the key file from");
      let text: string;
      try {
        text = await node.fs.readFile(file, "utf-8");
      } catch (e) {
        throw new RegistryError(CODE_UNAVAILABLE, `read: ${(e as Error).message}`);
      }
      return parseKeyFile(text);
    };
  }
  try {
    return await load();
  } catch (e) {
    const detail = e instanceof RegistryError ? e.message.replace(`${e.code}: `, "") : String(e);
    throw new RegistryError(
      CODE_UNAVAILABLE,
      `${source}: ${detail}; supply the verification material or set PODIUM_VERIFY_SIGNATURES=never`,
    );
  }
}

// resolveVerification resolves a client's §4.7.9 policy and, under always, its
// key set. An unusable key under always rejects with a RegistryError carrying
// config.signature_provider_unavailable; an invalid policy value rejects with
// an Error naming its source. Empty strings count as unset. node:fs/promises
// and node:path load only through a guarded import, and a runtime without
// them reads as one with no sync.yaml scope and no key file.
//
// Spec: §4.7.9, §7.6.3
export async function resolveVerification(
  opts: VerificationOptions,
  env: Env,
  cwd: string,
  home: string | undefined,
): Promise<Verification> {
  const node = await loadNodeFs();
  const policy = await resolvePolicy(opts, env, cwd, home, node);
  if (policy === "never") return { policy, keys: [] };
  return { policy, keys: await resolveKeys(opts, env, home, node) };
}

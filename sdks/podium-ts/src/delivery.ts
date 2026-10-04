// The §4.7.10 delivery check and the §4.7.9 verifier for the TypeScript SDK.
//
// Spec: §4.7.9, §4.7.10, §7.6.3
//
// This module ports the Go decoding steps (pkg/version/served.go), the record
// framing (pkg/version.DeliveryHash), the manifest-body derivation
// (pkg/manifest.ManifestBodyOf), and the registry-managed envelope and key
// rules (pkg/sign). test/vectors/delivery-record.json pins every outcome
// against the Go libraries.
//
// JSON.parse is not the §4.7.10 JSON rule on its own: it keeps unpaired
// surrogate escapes and drops the earlier of two same-named members before any
// check on the parsed value can see it. decodeJsonText therefore scans the
// decoded text before parsing. Every path-keyed collection is a Map, because
// assigning a "__proto__" key on a plain object sets its prototype.
//
// The module uses only Web Crypto, TextEncoder, TextDecoder, atob, and btoa.
// It imports no Node module and reads no Node global, so a load that reaches
// no Node touchpoint today still runs where none exists (the CODE-8 runtime
// boundary of proposal 0041).

import { RegistryError } from "./errors.js";
import type { LargeResourceLink } from "./index.js";

// The §6.10 codes the delivery check raises.
export const CODE_MISMATCH = "materialize.content_hash_mismatch";
export const CODE_SIGNATURE_INVALID = "materialize.signature_invalid";
export const CODE_SIGNATURE_MISSING = "materialize.signature_missing";
export const CODE_UNAVAILABLE = "config.signature_provider_unavailable";

// The §4.7.9 policy values.
export type VerifyPolicy = "never" | "always";
export const POLICIES: readonly VerifyPolicy[] = ["never", "always"];

// The framed leading value of every delivery stream, and the record's scalar
// fields in framing order. Both mirror pkg/version.DeliveryHash, and each
// field name is the wire member §7.2 "Record fields" maps it to.
export const RECORD_TAG = "podium/delivery-record/2";
export const RECORD_FIELDS = [
  "id",
  "version",
  "type",
  "content_hash",
  "sensitivity",
  "artifact_revision",
  "frontmatter",
  "manifest_body",
  "skill_raw",
] as const;

type RecordField = (typeof RECORD_FIELDS)[number];

// A record field is served text, framed as its UTF-8 encoding, or the bytes
// of a fetched manifest document, framed as is.
export type DeliveryRecord = Record<RecordField, string | Uint8Array> & {
  resources: Map<string, string>;
};

// The §4.7.10 step 1 nesting limit; the top-level container is level 1.
const MAX_JSON_DEPTH = 64;

const SKILL_TYPE = "skill";
const STATUS_OK = "ok";

const ED25519_PUBLIC_KEY_SIZE = 32;
const ED25519_PRIVATE_KEY_SIZE = 64;
const ED25519_SIGNATURE_SIZE = 64;

const encoder = new TextEncoder();

function malformed(message: string): RegistryError {
  return new RegistryError(CODE_MISMATCH, message);
}

// ---------------------------------------------------------------------------
// Step 1: the JSON rule.
// ---------------------------------------------------------------------------

function isHexDigit(c: string): boolean {
  return /^[0-9A-Fa-f]$/.test(c);
}

// escapeCodeUnit returns the code unit of the \u escape whose backslash is at
// i, or -1 when the escape at i is not a \u escape.
function escapeCodeUnit(text: string, i: number): number {
  if (text[i + 1] !== "u") return -1;
  const hex = text.slice(i + 2, i + 6);
  if (hex.length !== 4 || ![...hex].every(isHexDigit)) return -1;
  return parseInt(hex, 16);
}

// scanJsonText is the port of the Go checkJSONText byte scan over decoded
// text. It counts each [ and { outside a string as one level deeper, refuses a
// 65th open level, and refuses each \u escape in D800 to DBFF that is not
// directly followed by a \u escape in DC00 to DFFF and each \u escape in DC00
// to DFFF that does not directly follow one. It runs before JSON.parse, so it
// covers a member that a later member of the same name replaces. Text the scan
// cannot tokenize is refused by the parse.
function scanJsonText(text: string): void {
  let depth = 0;
  let inString = false;
  // pendingHighEnd is the index just past an unpaired high-surrogate escape,
  // or -1 when none is pending.
  let pendingHighEnd = -1;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (!inString) {
      if (c === '"') inString = true;
      else if (c === "[" || c === "{") {
        depth++;
        if (depth > MAX_JSON_DEPTH) throw malformed(`body nests deeper than ${MAX_JSON_DEPTH} levels`);
      } else if (c === "]" || c === "}") depth--;
      continue;
    }
    if (c === '"') {
      if (pendingHighEnd >= 0) throw malformed("unpaired surrogate escape");
      inString = false;
      continue;
    }
    if (c !== "\\") {
      if (pendingHighEnd >= 0) throw malformed("unpaired surrogate escape");
      continue;
    }
    const cu = escapeCodeUnit(text, i);
    if (pendingHighEnd >= 0) {
      if (i !== pendingHighEnd || cu < 0xdc00 || cu > 0xdfff) throw malformed("unpaired surrogate escape");
      pendingHighEnd = -1;
    } else if (cu >= 0xdc00 && cu <= 0xdfff) {
      throw malformed("unpaired surrogate escape");
    } else if (cu >= 0xd800 && cu <= 0xdbff) {
      pendingHighEnd = i + 6;
    }
    // Skip the escaped character, and the four hex digits of a \u escape.
    i += cu >= 0 ? 5 : 1;
  }
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// toMapTree copies a parsed value with every object rebuilt as a Map. It walks
// the value with an explicit stack.
function toMapTree(root: unknown): unknown {
  const convert = (v: unknown): unknown =>
    Array.isArray(v) ? new Array<unknown>(v.length) : isPlainObject(v) ? new Map<string, unknown>() : v;
  const out = convert(root);
  const stack: Array<[unknown, unknown]> = [[root, out]];
  while (stack.length > 0) {
    const [src, dst] = stack.pop() as [unknown, unknown];
    const entries: Array<[string | number, unknown]> = Array.isArray(src)
      ? src.map((v, i) => [i, v])
      : Object.entries(src as Record<string, unknown>);
    for (const [k, v] of entries) {
      const c = convert(v);
      if (dst instanceof Map) dst.set(k as string, c);
      else (dst as unknown[])[k as number] = c;
      if (c !== v) stack.push([v, c]);
    }
  }
  return out;
}

// decodeJsonText applies the §4.7.10 step 1 JSON rule to bytes and returns the
// value with every object as a Map. When two members share a name the later
// one is kept. Every refusal is a RegistryError with
// materialize.content_hash_mismatch.
export function decodeJsonText(bytes: Uint8Array): unknown {
  let text: string;
  try {
    // ignoreBOM keeps a byte order mark in the text, where JSON.parse refuses it.
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  } catch {
    throw malformed("body is not valid UTF-8");
  }
  scanJsonText(text);
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (e) {
    throw malformed(`body is not a JSON text: ${(e as Error).message}`);
  }
  return toMapTree(value);
}

// plainJson copies a Map tree back to plain objects. Each object is built with
// Object.fromEntries, which defines every key as an own property, so a
// "__proto__" member stays a member.
export function plainJson(value: unknown): unknown {
  if (value instanceof Map) {
    return Object.fromEntries([...value].map(([k, v]) => [k, plainJson(v)]));
  }
  if (Array.isArray(value)) return value.map(plainJson);
  return value;
}

// ---------------------------------------------------------------------------
// Step 3: canonical base64.
// ---------------------------------------------------------------------------

// decodeBase64 decodes standard base64 accepted only in canonical form. atob
// accepts whitespace, missing padding, and a non-zero pad bit, so a value is
// accepted only when btoa of its decoded bytes reproduces it.
export function decodeBase64(s: string): Uint8Array {
  let bin: string;
  try {
    bin = atob(s);
  } catch (e) {
    throw malformed(`base64: ${(e as Error).message}`);
  }
  if (btoa(bin) !== s) throw malformed("base64 value is not in canonical form");
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// ---------------------------------------------------------------------------
// Step 2: exact members with typed values.
// ---------------------------------------------------------------------------

type Members = Map<string, unknown>;

function optStr(m: Members, name: string, where = ""): string {
  const v = m.get(name);
  if (v === undefined || v === null) return "";
  if (typeof v !== "string") throw malformed(`${where}${name} is not a string`);
  return v;
}

function optBool(m: Members, name: string, where = ""): boolean {
  const v = m.get(name);
  if (v === undefined || v === null) return false;
  if (typeof v !== "boolean") throw malformed(`${where}${name} is not a boolean`);
  return v;
}

function optObject(m: Members, name: string): Members {
  const v = m.get(name);
  if (v === undefined || v === null) return new Map();
  if (!(v instanceof Map)) throw malformed(`${name} is not an object`);
  return v;
}

function optArray(m: Members, name: string): unknown[] {
  const v = m.get(name);
  if (v === undefined || v === null) return [];
  if (!Array.isArray(v)) throw malformed(`${name} is not an array`);
  return v;
}

function bodyOf(value: string, b64: boolean): Uint8Array {
  return b64 ? decodeBase64(value) : encoder.encode(value);
}

// typed copies a member outside the record only when it has the declared JSON
// type, and otherwise leaves it undefined without a refusal.
function typed<T>(m: Members, name: string, type: "string" | "number" | "boolean"): T | undefined {
  const v = m.get(name);
  return typeof v === type ? (v as T) : undefined;
}

// ---------------------------------------------------------------------------
// Steps 5 to 7: the record, the body checks, and the hash.
// ---------------------------------------------------------------------------

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

async function sha256(bytes: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", bytes as BufferSource));
}

// resourceDigest returns "sha256:" and the lowercase hex SHA-256 digest of body.
export async function resourceDigest(body: Uint8Array): Promise<string> {
  return "sha256:" + hex(await sha256(body));
}

// checkLinked applies the §4.7.10 step 6 check to one fetched or decoded body.
// An empty want is not checked on its own, because the record frames the
// empty value and the delivery-hash comparison refuses it.
export async function checkLinked(body: Uint8Array, want: string, what: string): Promise<void> {
  if (!want) return;
  const got = await resourceDigest(body);
  if (got !== want) throw malformed(`${what}: content hash ${got} does not match ${want}`);
}

function compareBytes(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

function asBytes(value: string | Uint8Array): Uint8Array {
  return typeof value === "string" ? encoder.encode(value) : value;
}

// deliveryHash returns the §4.7.10 delivery hash of record as sha256:<hex>.
// Paths are sorted by their UTF-8 encodings, because the default string sort
// orders UTF-16 code units and places a path in U+E000 to U+FFFF after a
// non-BMP path.
export async function deliveryHash(record: DeliveryRecord): Promise<string> {
  const parts: Uint8Array[] = [encoder.encode(RECORD_TAG)];
  for (const name of RECORD_FIELDS) parts.push(asBytes(record[name] ?? ""));
  const paths = [...record.resources.keys()]
    .map((p) => ({ p, b: encoder.encode(p) }))
    .sort((x, y) => compareBytes(x.b, y.b));
  for (const { p, b } of paths) parts.push(b, encoder.encode(record.resources.get(p) ?? ""));
  const size = parts.reduce((n, part) => n + 8 + part.length, 0);
  const stream = new Uint8Array(size);
  const view = new DataView(stream.buffer);
  let off = 0;
  for (const part of parts) {
    view.setBigUint64(off, BigInt(part.length), false);
    stream.set(part, off + 8);
    off += 8 + part.length;
  }
  return "sha256:" + hex(await sha256(stream));
}

// Lifecycle holds the §4.7.4 members each load and batch entry carries, each
// copied only when it has its declared JSON type.
export interface Lifecycle {
  deprecated?: boolean;
  replaced_by?: string;
  deprecation_warning?: string;
}

function lifecycleOf(m: Members): Lifecycle {
  return {
    deprecated: typed<boolean>(m, "deprecated", "boolean"),
    replaced_by: typed<string>(m, "replaced_by", "string"),
    deprecation_warning: typed<string>(m, "deprecation_warning", "string"),
  };
}

// Served is one decoded load_artifact body or ok batch entry. For a body that
// carries manifest_body_url, the caller fetches, checks, and places the
// document with placeManifestDocument before it recomputes the hash.
export interface Served {
  record: DeliveryRecord;
  hash: string;
  signature: string;
}

// ManifestLink is the manifest_body_url link, read by exact name.
export interface ManifestLink {
  presigned_url: string;
  content_hash: string;
}

// ParsedLoad is a decoded load_artifact body: the record and the exposed
// members as plain values.
export interface ParsedLoad {
  served: Served;
  resources: Record<string, string>;
  resourcesBase64: boolean;
  largeResources: Record<string, LargeResourceLink>;
  manifestLink?: ManifestLink;
  lifecycle: Lifecycle;
}

function readRecordMembers(m: Members): Served {
  const record = Object.fromEntries(RECORD_FIELDS.map((name) => [name, optStr(m, name)])) as unknown as DeliveryRecord;
  record.resources = new Map();
  return { record, hash: optStr(m, "delivery_hash"), signature: optStr(m, "delivery_signature") };
}

function parseLink(raw: unknown, where: string): Members {
  if (!(raw instanceof Map)) throw malformed(`${where} is not an object`);
  const url = optStr(raw, "presigned_url", where + ".");
  optStr(raw, "content_hash", where + ".");
  if (!url) throw malformed(`${where} has no presigned_url`);
  return raw;
}

function publicLink(link: Members): LargeResourceLink {
  return {
    url: optStr(link, "presigned_url"),
    content_hash: typed<string>(link, "content_hash", "string"),
    size: typed<number>(link, "size", "number"),
    content_type: typed<string>(link, "content_type", "string"),
  };
}

// parseLoadResponse applies §4.7.10 steps 1 to 5 to a load_artifact body, the
// port of version.ParseLoadResponse. A body that carries manifest_body_url
// returns with manifestLink set and the document unplaced. Every refusal is a
// RegistryError with materialize.content_hash_mismatch.
export async function parseLoadResponse(bytes: Uint8Array): Promise<ParsedLoad> {
  const m = decodeJsonText(bytes);
  if (!(m instanceof Map)) throw malformed("top-level value is not an object");
  const served = readRecordMembers(m);
  const b64 = optBool(m, "resources_base64");
  const hashes = served.record.resources;
  const resources: Array<[string, string]> = [];
  for (const [path, value] of optObject(m, "resources")) {
    if (value === null) continue;
    if (typeof value !== "string") throw malformed(`resources[${JSON.stringify(path)}] is not a string`);
    resources.push([path, value]);
    hashes.set(path, await resourceDigest(bodyOf(value, b64)));
  }
  const large: Array<[string, LargeResourceLink]> = [];
  for (const [path, raw] of optObject(m, "large_resources")) {
    if (raw === null) continue;
    const link = parseLink(raw, `large_resources[${JSON.stringify(path)}]`);
    if (hashes.has(path)) {
      throw malformed(`resource ${JSON.stringify(path)} is in both resources and large_resources`);
    }
    large.push([path, publicLink(link)]);
    hashes.set(path, optStr(link, "content_hash"));
  }
  const out: ParsedLoad = {
    served,
    resources: Object.fromEntries(resources),
    resourcesBase64: b64,
    largeResources: Object.fromEntries(large),
    lifecycle: lifecycleOf(m),
  };
  const mbu = m.get("manifest_body_url");
  if (mbu !== undefined && mbu !== null) {
    const link = parseLink(mbu, "manifest_body_url");
    out.manifestLink = { presigned_url: optStr(link, "presigned_url"), content_hash: optStr(link, "content_hash") };
  }
  return out;
}

// placeManifestDocument completes the record with a fetched manifest document
// and its body, the port of Served.PlaceManifestDocument: the document goes to
// skill_raw for a skill and to frontmatter otherwise, framed as the fetched
// bytes. No other code in the SDK builds or completes a record.
export function placeManifestDocument(served: Served, doc: Uint8Array, body: Uint8Array): void {
  const name = served.record.type === SKILL_TYPE ? "skill_raw" : "frontmatter";
  served.record[name] = doc;
  served.record.manifest_body = body;
}

// BatchReference is a batch reference holding only the members step 2 reads
// for its class.
export type BatchReference =
  | { path: string; presigned_url: string; content_hash: string }
  | { path: string; content_hash: string; inline: string; inline_base64: boolean };

// BatchEntry is one element of a §7.6.2 batch body. served is set for an ok
// entry that decoded; error is the step 2 to 6 refusal of an ok entry.
export interface BatchEntry {
  status: string;
  id: string;
  members: Members;
  served?: Served;
  references: BatchReference[];
  lifecycle: Lifecycle;
  error?: RegistryError;
}

async function readReference(ref: unknown, i: number, seen: Map<string, string>): Promise<BatchReference> {
  const where = `resources[${i}].`;
  if (!(ref instanceof Map)) throw malformed(`resources[${i}] is not an object`);
  const rawPath = ref.get("path");
  if (rawPath === undefined || rawPath === null) throw malformed(`resources[${i}] has no path`);
  const path = optStr(ref, "path", where);
  if (seen.has(path)) throw malformed(`resource ${JSON.stringify(path)} is named more than once`);
  const contentHash = optStr(ref, "content_hash", where);
  const url = optStr(ref, "presigned_url", where);
  const inline = optStr(ref, "inline", where);
  const b64 = optBool(ref, "inline_base64", where);
  seen.set(path, contentHash);
  if (url) return { path, presigned_url: url, content_hash: contentHash };
  await checkLinked(bodyOf(inline, b64), contentHash, `resource ${JSON.stringify(path)}`);
  return { path, content_hash: contentHash, inline, inline_base64: b64 };
}

async function parseBatchEntry(m: Members): Promise<BatchEntry> {
  const id = m.get("id");
  const entry: BatchEntry = {
    status: "",
    id: typeof id === "string" ? id : "",
    members: m,
    references: [],
    lifecycle: lifecycleOf(m),
  };
  try {
    entry.status = optStr(m, "status");
    if (entry.status !== STATUS_OK) return entry;
    const served = readRecordMembers(m);
    const refs = optArray(m, "resources");
    for (let i = 0; i < refs.length; i++) {
      entry.references.push(await readReference(refs[i], i, served.record.resources));
    }
    entry.served = served;
  } catch (e) {
    // Steps 2 to 6 refuse only with RegistryError; anything else is a defect
    // that must surface rather than become an entry refusal.
    if (!(e instanceof RegistryError)) throw e;
    entry.error = e;
    entry.references = [];
  }
  return entry;
}

// parseBatchResponse applies step 1 to a whole batch body and steps 2 to 6 to
// each ok entry, the port of version.ParseBatchResponse. A body that fails
// step 1, or whose top-level value is not an array of objects, is refused as a
// whole; an ok entry that fails a later step carries its refusal in
// BatchEntry.error.
export async function parseBatchResponse(bytes: Uint8Array): Promise<BatchEntry[]> {
  const v = decodeJsonText(bytes);
  if (!Array.isArray(v)) throw malformed("batch body is not an array");
  v.forEach((m, i) => {
    if (!(m instanceof Map)) throw malformed(`batch entry ${i} is not an object`);
  });
  const out: BatchEntry[] = [];
  for (const m of v as Members[]) out.push(await parseBatchEntry(m));
  return out;
}

// ---------------------------------------------------------------------------
// Manifest-body derivation.
// ---------------------------------------------------------------------------

// The port of pkg/manifest's frontmatter delimiter rule. It runs over a binary
// string holding one character per byte, so the match offsets are byte
// offsets; the delimiters are ASCII.
const FRONTMATTER_RE = /^---\r?\n[\s\S]*?\r?\n---\r?\n?([\s\S]*)$/;

function binaryString(bytes: Uint8Array): string {
  let s = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    s += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return s;
}

// manifestBodyOf derives the manifest body of a manifest document by the
// §4.7.10 rule: every byte after the closing --- with leading CR and LF bytes
// removed. A document with no frontmatter block has an empty body.
export function manifestBodyOf(doc: Uint8Array): Uint8Array {
  const m = FRONTMATTER_RE.exec(binaryString(doc));
  if (!m) return new Uint8Array(0);
  let start = doc.length - m[1].length;
  while (start < doc.length && (doc[start] === 0x0d || doc[start] === 0x0a)) start++;
  return doc.slice(start);
}

// ---------------------------------------------------------------------------
// §4.7.9 keys and envelope.
// ---------------------------------------------------------------------------

function unavailable(message: string): RegistryError {
  return new RegistryError(CODE_UNAVAILABLE, message);
}

// decodeKey decodes configured key material as pkg/sign does, with
// base64.StdEncoding, which skips CR and LF. The canonical rule applies only
// to served values.
function decodeKey(text: string, size: number, kind: string): Uint8Array {
  const cleaned = text.trim().replace(/[\r\n]/g, "");
  if (cleaned.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(cleaned)) {
    throw unavailable(`decode ${kind} key: not standard base64`);
  }
  const bin = atob(cleaned);
  if (bin.length !== size) throw unavailable(`${kind} key is ${bin.length} bytes, want ${size}`);
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

// parseVerifyKeyList decodes a PODIUM_SIGNATURE_VERIFY_KEY list of Ed25519
// public keys. An empty entry, a trailing comma included, and an entry that
// does not decode refuse the whole list with
// config.signature_provider_unavailable.
export function parseVerifyKeyList(value: string): Uint8Array[] {
  return value.split(",").map((entry, i) => {
    if (!entry.trim()) throw unavailable(`entry ${i + 1} is empty`);
    return decodeKey(entry, ED25519_PUBLIC_KEY_SIZE, "public");
  });
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  return compareBytes(a, b) === 0;
}

// parseKeyFile parses the registry-managed key file and returns its
// verification key set: the public: key followed by each verify: key. A
// missing or repeated public: line, a repeated private: line, and a private:
// line whose public half differs from public: are refused; unknown prefixes
// are ignored, as pkg/sign.ParseKeyFile does.
export function parseKeyFile(text: string): Uint8Array[] {
  let priv: Uint8Array | undefined;
  let pub: Uint8Array | undefined;
  const verify: Uint8Array[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (line.startsWith("private:")) {
      if (priv) throw unavailable('key file carries more than one "private:" line');
      priv = decodeKey(line.slice("private:".length), ED25519_PRIVATE_KEY_SIZE, "private");
    } else if (line.startsWith("public:")) {
      if (pub) throw unavailable('key file carries more than one "public:" line');
      pub = decodeKey(line.slice("public:".length), ED25519_PUBLIC_KEY_SIZE, "public");
    } else if (line.startsWith("verify:")) {
      verify.push(decodeKey(line.slice("verify:".length), ED25519_PUBLIC_KEY_SIZE, "verify"));
    }
  }
  if (!pub) throw unavailable('key file carries no "public:" line');
  // An Ed25519 private key is the seed followed by its public half.
  if (priv && !bytesEqual(priv.subarray(32), pub)) {
    throw unavailable('the "public:" line is not the public half of the "private:" line');
  }
  return [pub, ...verify];
}

// keyIdOf returns the §4.7.9 key_id of a 32-byte Ed25519 public key.
export async function keyIdOf(publicKey: Uint8Array): Promise<string> {
  return hex((await sha256(publicKey)).subarray(0, 8));
}

function signatureInvalid(message: string): RegistryError {
  return new RegistryError(CODE_SIGNATURE_INVALID, message);
}

// envelopeMembers applies the §4.7.9 envelope rule and returns the key_id and
// the signature. It reads both members with Map.get by exact name, so a
// case-variant name is an ignored unknown member, and a null member is absent.
function envelopeMembers(envelope: string): { keyId: string; sig: Uint8Array } {
  let keyId: string;
  let sig: Uint8Array;
  try {
    const m = decodeJsonText(encoder.encode(envelope));
    if (!(m instanceof Map)) throw malformed("envelope is not an object");
    const s = m.get("signature");
    if (s === undefined || s === null) throw malformed("envelope has no signature");
    const sigText = optStr(m, "signature", "envelope member ");
    keyId = optStr(m, "key_id", "envelope member ");
    sig = decodeBase64(sigText);
  } catch (e) {
    throw signatureInvalid(`envelope: ${(e as Error).message}`);
  }
  if (sig.length !== ED25519_SIGNATURE_SIZE) {
    throw signatureInvalid(`signature is ${sig.length} bytes, want ${ED25519_SIGNATURE_SIZE}`);
  }
  return { keyId, sig };
}

function hashBytes(signedHash: string): Uint8Array {
  const sep = signedHash.indexOf(":");
  const h = sep >= 0 ? signedHash.slice(sep + 1) : "";
  if (!h || h.length % 2 !== 0 || !/^[0-9A-Fa-f]*$/.test(h)) {
    throw signatureInvalid(`signed hash ${JSON.stringify(signedHash)} is not alg:hex`);
  }
  return Uint8Array.from(h.match(/../g) as string[], (b) => parseInt(b, 16));
}

async function ed25519Verifies(key: Uint8Array, sig: Uint8Array, digest: Uint8Array): Promise<boolean> {
  try {
    const k = await crypto.subtle.importKey("raw", key as BufferSource, { name: "Ed25519" }, false, ["verify"]);
    return await crypto.subtle.verify({ name: "Ed25519" }, k, sig as BufferSource, digest as BufferSource);
  } catch {
    return false;
  }
}

// verifyEnvelope verifies a registry-managed envelope over signedHash
// (sha256:<hex>) and returns the key_id of the key that verified it. The
// signed message is the 32-byte digest. The key the envelope's key_id names is
// tried first and then every other key, because the key_id is
// unauthenticated. Every refusal is a RegistryError with
// materialize.signature_invalid; an empty key set is
// config.signature_provider_unavailable.
export async function verifyEnvelope(envelope: string, signedHash: string, keys: Uint8Array[]): Promise<string> {
  if (keys.length === 0) throw unavailable("registry-managed key not configured");
  const { keyId, sig } = envelopeMembers(envelope);
  const digest = hashBytes(signedHash);
  const ids = await Promise.all(keys.map(keyIdOf));
  const order = keys.map((_, i) => i).sort((a, b) => Number(ids[a] !== keyId) - Number(ids[b] !== keyId));
  for (const i of order) {
    if (await ed25519Verifies(keys[i], sig, digest)) return ids[i];
  }
  throw signatureInvalid("signature does not verify under any trusted key");
}

// Verification is a client's resolved §4.7.9 policy and verification key set.
export interface Verification {
  policy: VerifyPolicy;
  keys: Uint8Array[];
}

// checkDelivery runs the §4.7.10 step 7 comparison and applies the §4.7.9
// policy. The comparison runs first and under every policy, so a record whose
// bytes were altered reports materialize.content_hash_mismatch whatever its
// signature.
export async function checkDelivery(served: Served, verification: Verification): Promise<void> {
  if (!served.hash) throw malformed("the response carries no delivery_hash");
  const got = await deliveryHash(served.record);
  if (got !== served.hash) throw malformed(`recomputed delivery hash ${got} does not match served ${served.hash}`);
  if (verification.policy === "never") return;
  if (!served.signature) {
    throw new RegistryError(CODE_SIGNATURE_MISSING, `policy "${verification.policy}" requires a signature`);
  }
  await verifyEnvelope(served.signature, served.hash, verification.keys);
}

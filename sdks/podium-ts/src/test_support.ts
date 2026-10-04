// Shared fixtures for the TypeScript SDK suite. Test-only: no SDK module
// imports this file.
//
// served() completes a stub load_artifact response or batch entry with the
// delivery hash the registry would compute (§4.7.10) and, on request, a
// registry-managed envelope (§4.7.9). stubRegistry() and objectStore() are
// request-recording fetchers, and isolateVerification() keeps the developer's
// key file, sync.yaml, and signing variables out of every test.

import { createPrivateKey, generateKeyPairSync, sign } from "node:crypto";
import { readFileSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, beforeEach, vi } from "vitest";

import { deliveryHash, keyIdOf, manifestBodyOf, RECORD_FIELDS, type DeliveryRecord } from "./delivery.js";

// The Go-generated cross-language vectors (TEST-1).
export const VECTORS = JSON.parse(
  readFileSync(new URL("../../../test/vectors/delivery-record.json", import.meta.url), "utf-8"),
);

export function b64(bytes: Uint8Array | string): string {
  return Buffer.from(typeof bytes === "string" ? new TextEncoder().encode(bytes) : bytes).toString("base64");
}

export function unb64(s: string): Uint8Array {
  return new Uint8Array(Buffer.from(s, "base64"));
}

function b64url(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64url");
}

// An Ed25519 signing key as the JWK members d (the seed) and x (the public key).
export interface SigningKey {
  d: string;
  x: string;
}

// VECTOR_KEY is the vector seed every vector signature is made with, and
// VECTOR_PUBLIC_KEY its public key in the PODIUM_SIGNATURE_VERIFY_KEY syntax.
export const VECTOR_PUBLIC_KEY: string = VECTORS.public_key;
export const VECTOR_KEY: SigningKey = {
  d: b64url(Buffer.from(VECTORS.signing_seed_hex, "hex")),
  x: b64url(unb64(VECTOR_PUBLIC_KEY)),
};

// unrelatedKey returns a fresh Ed25519 key that no test configures.
export function unrelatedKey(): SigningKey {
  const jwk = generateKeyPairSync("ed25519").privateKey.export({ format: "jwk" }) as { d?: string; x?: string };
  return { d: jwk.d as string, x: jwk.x as string };
}

// publicKeyOf returns the base64 public key of key in the key-list syntax.
export function publicKeyOf(key: SigningKey): string {
  return Buffer.from(key.x, "base64url").toString("base64");
}

// keyFileOf returns the registry-managed key file text for key.
export function keyFileOf(key: SigningKey): string {
  const seed = Buffer.from(key.d, "base64url");
  const pub = Buffer.from(key.x, "base64url");
  return `private: ${Buffer.concat([seed, pub]).toString("base64")}\npublic: ${pub.toString("base64")}\n`;
}

// signEnvelope returns the registry-managed envelope key makes over a hash.
export async function signEnvelope(key: SigningKey, hash: string): Promise<string> {
  const priv = createPrivateKey({ key: { kty: "OKP", crv: "Ed25519", d: key.d, x: key.x }, format: "jwk" });
  const digest = Buffer.from(hash.split(":", 2)[1], "hex");
  const sig = sign(null, digest, priv).toString("base64");
  const keyId = await keyIdOf(new Uint8Array(Buffer.from(key.x, "base64url")));
  return JSON.stringify({ key_id: keyId, signature: sig });
}

async function digest(body: Uint8Array): Promise<string> {
  const { createHash } = await import("node:crypto");
  return "sha256:" + createHash("sha256").update(body).digest("hex");
}

type Body = Record<string, unknown>;

function recordOf(body: Body): DeliveryRecord {
  const rec = Object.fromEntries(
    RECORD_FIELDS.map((name) => [name, typeof body[name] === "string" ? body[name] : ""]),
  ) as unknown as DeliveryRecord;
  rec.resources = new Map();
  return rec;
}

async function fillLink(link: Body, fetched: Record<string, Uint8Array>): Promise<string> {
  if (!("content_hash" in link)) link.content_hash = await digest(fetched[link.presigned_url as string]);
  return link.content_hash as string;
}

async function loadRecord(out: Body, fetched: Record<string, Uint8Array>): Promise<DeliveryRecord> {
  const rec = recordOf(out);
  for (const [path, value] of Object.entries((out.resources ?? {}) as Record<string, string>)) {
    rec.resources.set(path, await digest(out.resources_base64 ? unb64(value) : new TextEncoder().encode(value)));
  }
  for (const [path, link] of Object.entries((out.large_resources ?? {}) as Record<string, Body>)) {
    rec.resources.set(path, await fillLink(link, fetched));
  }
  const mbu = out.manifest_body_url as Body | undefined;
  if (mbu) {
    const doc = fetched[mbu.presigned_url as string];
    await fillLink(mbu, fetched);
    rec[rec.type === "skill" ? "skill_raw" : "frontmatter"] = doc;
    rec.manifest_body = manifestBodyOf(doc);
  }
  return rec;
}

async function entryRecord(out: Body, fetched: Record<string, Uint8Array>): Promise<DeliveryRecord> {
  const rec = recordOf(out);
  for (const ref of (out.resources ?? []) as Body[]) {
    if (!("content_hash" in ref)) {
      if (ref.presigned_url) {
        ref.content_hash = await digest(fetched[ref.presigned_url as string]);
      } else {
        const inline = (ref.inline ?? "") as string;
        ref.content_hash = await digest(ref.inline_base64 ? unb64(inline) : new TextEncoder().encode(inline));
      }
    }
    rec.resources.set(ref.path as string, ref.content_hash as string);
  }
  return rec;
}

// served completes a stub response with its delivery hash and link hashes.
// body is a load_artifact response, or a batch entry when it carries status. A
// link or reference without a content_hash gets the digest of its body: the
// bytes fetched serves at its URL, or its inline value. A manifest_body_url
// document is taken from fetched and framed as §4.7.10 frames it. signWith
// signs the delivery hash with that key.
export async function served(
  body: Body,
  opts: { fetched?: Record<string, Uint8Array>; signWith?: SigningKey } = {},
): Promise<Body> {
  const out = structuredClone(body);
  const fetched = opts.fetched ?? {};
  const rec = "status" in out ? await entryRecord(out, fetched) : await loadRecord(out, fetched);
  out.delivery_hash = await deliveryHash(rec);
  if (opts.signWith) out.delivery_signature = await signEnvelope(opts.signWith, out.delivery_hash as string);
  return out;
}

// A stub reply: a Uint8Array is sent as is, a Response is returned as is, and
// any other value is sent as JSON with status 200.
type Reply = unknown;

export interface Stub {
  fetcher: typeof fetch;
  // Every request URL, in order.
  requests: string[];
  // Every request body, in order.
  bodies: string[];
}

function respond(reply: Reply): Response {
  if (reply instanceof Response) return reply;
  if (reply instanceof Uint8Array) return new Response(reply as BodyInit, { status: 200 });
  return new Response(JSON.stringify(reply), { status: 200 });
}

// stubRegistry returns a fetcher that answers each request from routes, keyed
// by URL path without the query. A route value that is a function is called
// per request; a path no route names answers 404.
export function stubRegistry(routes: Record<string, Reply | (() => Reply)>): Stub {
  const stub: Stub = {
    requests: [],
    bodies: [],
    fetcher: async (input, init) => {
      const url = String(input);
      stub.requests.push(url);
      stub.bodies.push(String(init?.body ?? ""));
      const route = routes[new URL(url).pathname];
      if (route === undefined) return new Response(JSON.stringify({ code: "registry.not_found" }), { status: 404 });
      return respond(typeof route === "function" ? (route as () => Reply)() : route);
    },
  };
  return stub;
}

// objectStore returns a fetcher serving bytes by URL and recording requests.
export function objectStore(objects: Record<string, Uint8Array | string>): Stub {
  return stubRegistry(
    Object.fromEntries(
      Object.entries(objects).map(([url, body]) => [
        new URL(url).pathname,
        typeof body === "string" ? new TextEncoder().encode(body) : body,
      ]),
    ),
  );
}

// The variables that select the §4.7.9 policy and key set.
export const VERIFY_VARIABLES = ["PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGN_KEY_PATH", "PODIUM_VERIFY_SIGNATURES"];

// Isolation holds the current test's HOME and working directory.
export interface Isolation {
  home: string;
  cwd: string;
}

// isolateVerification registers a suite-wide beforeEach that unsets the
// signing variables, points HOME at an empty temporary directory, and reports
// a working directory outside any workspace, so a developer's
// ~/.podium/standalone key file, a sync.yaml the workspace walk reaches, or an
// exported PODIUM_SIGNATURE_VERIFY_KEY never selects the policy in a test. The
// working directory matters because the walk from the SDK checkout can reach
// a home directory whose .podium/ makes it a workspace.
export function isolateVerification(): Isolation {
  const iso: Isolation = { home: "", cwd: "" };
  beforeEach(async () => {
    iso.home = await mkdtemp(join(tmpdir(), "podium-home-"));
    iso.cwd = await mkdtemp(join(tmpdir(), "podium-cwd-"));
    for (const name of VERIFY_VARIABLES) vi.stubEnv(name, undefined);
    vi.stubEnv("HOME", iso.home);
    vi.spyOn(process, "cwd").mockReturnValue(iso.cwd);
  });
  afterEach(async () => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
    await rm(iso.home, { recursive: true, force: true });
    await rm(iso.cwd, { recursive: true, force: true });
  });
  return iso;
}

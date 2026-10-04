// The SDK delivery check: the Go-generated §4.7.10 and §4.7.9 vectors run
// through the decoders, and the client verifies each registry-served load and
// batch entry through a stub registry and object store.

import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { describe, expect, it, vi } from "vitest";

import {
  CODE_MISMATCH,
  checkDelivery,
  checkLinked,
  decodeBase64,
  deliveryHash,
  keyIdOf,
  manifestBodyOf,
  parseBatchResponse,
  parseKeyFile,
  parseLoadResponse,
  parseVerifyKeyList,
  placeManifestDocument,
  plainJson,
  RECORD_FIELDS,
  verifyEnvelope,
  type DeliveryRecord,
  type Served,
  type Verification,
} from "./delivery.js";
import { BatchResult, Client, LoadedArtifact, RegistryError } from "./index.js";
import {
  b64,
  isolateVerification,
  keyFileOf,
  objectStore,
  publicKeyOf,
  served,
  stubRegistry,
  unb64,
  unrelatedKey,
  VECTOR_KEY,
  VECTOR_PUBLIC_KEY,
  VECTORS,
} from "./test_support.js";

const iso = isolateVerification();

const MISMATCH = "materialize.content_hash_mismatch";
const UNAVAILABLE = "config.signature_provider_unavailable";
const OK = "ok";

const NEVER: Verification = { policy: "never", keys: [] };
const ALWAYS: Verification = { policy: "always", keys: [unb64(VECTOR_PUBLIC_KEY)] };
const enc = (s: string): Uint8Array => new TextEncoder().encode(s);

interface Case {
  name: string;
  outcome: string;
  [k: string]: unknown;
}

function cases(section: string): Case[] {
  return VECTORS[section] as Case[];
}

function vector(section: string, name: string): Case {
  const c = cases(section).find((x) => x.name === name);
  if (!c) throw new Error(`no ${section} vector named ${name}`);
  return c;
}

function fetchedOf(c: Case): Record<string, Uint8Array> {
  return Object.fromEntries(
    Object.entries((c.fetched ?? {}) as Record<string, string>).map(([url, v]) => [url, unb64(v)]),
  );
}

async function expectCode(p: Promise<unknown> | (() => unknown), code: string): Promise<RegistryError> {
  const err = await (typeof p === "function" ? Promise.resolve().then(p) : p).then(
    () => undefined,
    (e: unknown) => e,
  );
  expect(err).toBeInstanceOf(RegistryError);
  expect((err as RegistryError).code).toBe(code);
  return err as RegistryError;
}

// verifyResponse runs steps 1 to 7 on a load_artifact body and returns the
// served record, fetching linked bodies from fetched.
async function verifyResponse(body: Uint8Array, fetched: Record<string, Uint8Array>): Promise<Served> {
  const get = (url: string): Uint8Array => {
    if (!(url in fetched)) throw new RegistryError(CODE_MISMATCH, `object store serves no ${url}`);
    return fetched[url];
  };
  const parsed = await parseLoadResponse(body);
  if (parsed.manifestLink) {
    const doc = get(parsed.manifestLink.presigned_url);
    await checkLinked(doc, parsed.manifestLink.content_hash, "manifest document");
    placeManifestDocument(parsed.served, doc, manifestBodyOf(doc));
  }
  for (const path of Object.keys(parsed.largeResources).sort()) {
    const link = parsed.largeResources[path];
    await checkLinked(get(link.url), link.content_hash ?? "", path);
  }
  await checkDelivery(parsed.served, NEVER);
  return parsed.served;
}

describe("delivery vectors", () => {
  for (const c of cases("responses")) {
    describe(`response: ${c.name}`, () => {
      // Spec: §4.7.10
      it("reaches the declared outcome", async () => {
        const body = unb64(c.body_base64 as string);
        if (c.outcome !== OK) {
          await expectCode(verifyResponse(body, fetchedOf(c)), c.outcome);
          return;
        }
        const s = await verifyResponse(body, fetchedOf(c));
        expect(s.hash).toBe(c.served_hash);
        // Every vector signature is made with the vector seed, so an ok record
        // also passes the always policy under the vector public key.
        await checkDelivery(s, ALWAYS);
      });
    });
  }

  for (const c of cases("batch")) {
    describe(`batch: ${c.name}`, () => {
      // Spec: §4.7.10
      it("reaches the declared outcome", async () => {
        const body = unb64(c.body_base64 as string);
        if (c.outcome !== OK) {
          await expectCode(parseBatchResponse(body), c.outcome);
          return;
        }
        const entries = await parseBatchResponse(body);
        const want = c.entries as Case[];
        expect(entries).toHaveLength(want.length);
        for (const [i, entry] of entries.entries()) {
          if (want[i].outcome !== OK) {
            if (entry.error) expect(entry.error.code).toBe(want[i].outcome);
            else await expectCode(checkDelivery(entry.served as Served, NEVER), want[i].outcome);
            continue;
          }
          expect(entry.error).toBeUndefined();
          await checkDelivery(entry.served as Served, NEVER);
          expect(entry.served?.hash).toBe(want[i].served_hash);
        }
      });
    });
  }

  for (const c of cases("base64")) {
    describe(`base64: ${c.name}`, () => {
      // Spec: §4.7.10
      it("reaches the declared outcome", async () => {
        if (c.outcome !== OK) {
          await expectCode(() => decodeBase64(c.input as string), c.outcome);
          return;
        }
        expect(decodeBase64(c.input as string)).toEqual(unb64((c.decoded as string) ?? ""));
      });
    });
  }

  for (const c of cases("envelope")) {
    describe(`envelope: ${c.name}`, () => {
      // Spec: §7.6.3
      // Spec: §4.7.9
      it("reaches the declared outcome", async () => {
        const keys = (c.keys as string[]).map(unb64);
        const call = verifyEnvelope(c.envelope as string, c.signed_hash as string, keys);
        if (c.outcome !== OK) {
          await expectCode(call, c.outcome);
          return;
        }
        expect(await call).toBe(c.key_id);
      });
    });
  }

  for (const c of cases("manifest_body")) {
    describe(`manifest body: ${c.name}`, () => {
      // Spec: §4.7.10
      it("reaches the declared outcome", () => {
        expect(manifestBodyOf(unb64(c.document_base64 as string))).toEqual(unb64(c.body_base64 as string));
      });
    });
  }

  for (const c of cases("verify_key_list")) {
    describe(`verify key list: ${c.name}`, () => {
      // Spec: §7.6.3
      // Spec: §4.7.9
      it("reaches the declared outcome", async () => {
        if (c.outcome !== OK) {
          await expectCode(() => parseVerifyKeyList(c.input as string), c.outcome);
          return;
        }
        expect(parseVerifyKeyList(c.input as string).map((k) => b64(k))).toEqual(c.keys);
      });
    });
  }

  for (const c of cases("key_file")) {
    describe(`key file: ${c.name}`, () => {
      // Spec: §7.6.3
      // Spec: §4.7.9
      it("reaches the declared outcome", async () => {
        if (c.outcome !== OK) {
          await expectCode(() => parseKeyFile(c.input as string), c.outcome);
          return;
        }
        expect(parseKeyFile(c.input as string).map((k) => b64(k))).toEqual(c.keys);
      });
    });
  }

  // Spec: §4.7.10 — JavaScript's default sort orders UTF-16 code units, which
  // places U+FF5E after U+1F600; the record orders UTF-8 bytes, the reverse.
  it("orders paths by UTF-8 bytes", async () => {
    const rec = Object.fromEntries(RECORD_FIELDS.map((n) => [n, ""])) as unknown as DeliveryRecord;
    const a = { ...rec, resources: new Map([["～.md", "x"], ["😀.md", "y"]]) };
    const b = { ...rec, resources: new Map([["😀.md", "y"], ["～.md", "x"]]) };
    expect(await deliveryHash(a)).toBe(await deliveryHash(b));
    expect(["～.md", "😀.md"].sort()).toEqual(["😀.md", "～.md"]);
  });

  // Spec: §4.7.10
  it("refuses a batch body that is not an array of objects", async () => {
    await expectCode(parseBatchResponse(enc("{}")), MISMATCH);
    await expectCode(parseBatchResponse(enc("[1]")), MISMATCH);
    const [entry] = await parseBatchResponse(enc('[{"status":"ok","resources":{}}]'));
    expect(entry.error?.code).toBe(MISMATCH);
  });

  // Spec: §4.7.10
  it("refuses a malformed resource map and link", async () => {
    await expectCode(parseLoadResponse(enc('{"resources":{"a":5}}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"resources":[]}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"large_resources":{"a":"x"}}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"resources_base64":5}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"manifest_body_url":{"presigned_url":5}}')), MISMATCH);
    const [entry] = await parseBatchResponse(enc('[{"status":"ok","resources":[5]}]'));
    expect(entry.error?.code).toBe(MISMATCH);
    const nulls = await parseLoadResponse(enc('{"resources":{"a":null},"large_resources":{"b":null}}'));
    expect(nulls.served.record.resources.size).toBe(0);
  });

  // Spec: §4.7.10
  it("checkDelivery requires a hash and keys", async () => {
    const record = Object.fromEntries(RECORD_FIELDS.map((n) => [n, ""])) as unknown as DeliveryRecord;
    record.id = "acme/a";
    record.resources = new Map();
    const s: Served = { record, hash: await deliveryHash(record), signature: "{}" };
    await expectCode(checkDelivery(s, { policy: "always", keys: [new Uint8Array(32)] }), "materialize.signature_invalid");
    await expectCode(verifyEnvelope("{}", s.hash, []), UNAVAILABLE);
    const envelope = JSON.stringify({ signature: "A".repeat(86) + "==" });
    await expectCode(verifyEnvelope(envelope, "nohash", [new Uint8Array(32)]), "materialize.signature_invalid");
    await expectCode(checkDelivery({ ...s, hash: "" }, NEVER), MISMATCH);
  });

  // Spec: §4.7.10 — a high surrogate escape followed by an escape outside
  // DC00 to DFFF is unpaired, and a short \u escape fails the parse.
  it("refuses a broken surrogate pair and a short escape", async () => {
    await expectCode(parseLoadResponse(enc('{"frontmatter":"\\ud800\\u0041"}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"frontmatter":"\\ud800\\n"}')), MISMATCH);
    await expectCode(parseLoadResponse(enc('{"frontmatter":"\\u12"}')), MISMATCH);
    const ok = await parseLoadResponse(enc('{"frontmatter":"\\ud83d\\ude00 \\\\ \\"x\\""}'));
    expect(ok.served.record.frontmatter).toBe('😀 \\ "x"');
  });

  // Spec: §7.6.3
  // Spec: §4.7.9
  it("refuses a key of the wrong size and a repeated private line", async () => {
    await expectCode(() => parseVerifyKeyList(b64(new Uint8Array(16))), UNAVAILABLE);
    const file = `private: ${b64(new Uint8Array(64))}\nprivate: ${b64(new Uint8Array(64))}\npublic: ${VECTOR_PUBLIC_KEY}\n`;
    await expectCode(() => parseKeyFile(file), UNAVAILABLE);
  });

  // Spec: §7.6.3
  // Spec: §4.7.9 — a key Web Crypto cannot import never verifies a signature.
  it("refuses a signature under a key that does not import", async () => {
    const c0 = vector("envelope", "valid");
    await expectCode(
      verifyEnvelope(c0.envelope as string, c0.signed_hash as string, [new Uint8Array(31)]),
      "materialize.signature_invalid",
    );
  });

  // Spec: §7.6.3
  // Spec: §4.7.9
  it("derives the key_id from the public key", async () => {
    expect(await keyIdOf(unb64(VECTOR_PUBLIC_KEY))).toBe(vector("envelope", "valid").key_id);
  });

  // Spec: §4.7.10
  it("plainJson copies a Map tree to plain objects", () => {
    const tree = new Map<string, unknown>([["__proto__", new Map([["a", [new Map([["b", 1]])]]])]]);
    const plain = plainJson(tree) as Record<string, unknown>;
    expect(Object.hasOwn(plain, "__proto__")).toBe(true);
    expect(Object.getPrototypeOf(plain)).toBe(Object.prototype);
    expect(JSON.stringify(plain)).toBe('{"__proto__":{"a":[{"b":1}]}}');
  });
});

// ---------------------------------------------------------------------------
// Client-level behavior.
// ---------------------------------------------------------------------------

const REGISTRY = "http://reg";

function rule(name = "acme/rule", extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: name,
    type: "rule",
    version: "1.0.0",
    content_hash: "sha256:" + "1".repeat(64),
    sensitivity: "internal",
    artifact_revision: "2025-06-01T12:34:56.789012Z",
    frontmatter: `---\nname: ${name}\n---\nBody.\n`,
    manifest_body: "Body.\n",
    ...extra,
  };
}

function entry(name: string, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return { ...rule(name, extra), status: "ok" };
}

function loadStub(reply: unknown): ReturnType<typeof stubRegistry> {
  return stubRegistry({ "/v1/load_artifact": reply });
}

function batchStub(reply: unknown): ReturnType<typeof stubRegistry> {
  return stubRegistry({ "/v1/artifacts:batchLoad": reply });
}

function client(stub: { fetcher: typeof fetch }, opts: Record<string, unknown> = {}): Client {
  return new Client({ registry: REGISTRY, fetcher: stub.fetcher, ...opts });
}

function codes(results: BatchResult[]): string[] {
  return results.map((r) => (r.status === "ok" ? "ok" : (r.error?.code ?? "")));
}

async function withTempDir<T>(fn: (dir: string) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), "podium-dlv-"));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

async function writeAt(path: string, text: string): Promise<void> {
  await mkdir(join(path, ".."), { recursive: true });
  await writeFile(path, text);
}

const defaultKeyFile = (): string => join(iso.home, ".podium", "standalone", "registry-signing.key");

describe("policy and key resolution", () => {
  // Spec: §7.6.3
  it("defaults to never without a key", async () => {
    const stub = loadStub(await served(rule()));
    const c = client(stub);
    expect(c.verifySignatures).toBeUndefined();
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("never");
  });

  // Spec: §7.6.3
  it("selects always when PODIUM_SIGNATURE_VERIFY_KEY is set", async () => {
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", VECTOR_PUBLIC_KEY);
    const c = client(loadStub(await served(rule(), { signWith: VECTOR_KEY })));
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("always");
  });

  // Spec: §7.6.3
  it("selects always when a key file sits at the default path", async () => {
    await writeAt(defaultKeyFile(), keyFileOf(VECTOR_KEY));
    const c = client(loadStub(await served(rule(), { signWith: VECTOR_KEY })));
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("always");
  });

  // Spec: §7.6.3
  it("reads the key file at PODIUM_SIGN_KEY_PATH", async () => {
    const path = join(iso.home, "k.key");
    await writeAt(path, keyFileOf(VECTOR_KEY));
    vi.stubEnv("PODIUM_SIGN_KEY_PATH", path);
    const c = client(loadStub(await served(rule(), { signWith: VECTOR_KEY })));
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("always");
  });

  // Spec: §7.6.3
  it("parses no key under an explicit never", async () => {
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64");
    const c = client(loadStub(await served(rule())), { verifySignatures: "never" });
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("never");
  });

  // Spec: §7.6.3
  it("throws on an invalid verifySignatures in the constructor", () => {
    expect(() => new Client({ registry: REGISTRY, verifySignatures: "sometimes" as unknown as "never" })).toThrow(
      /verifySignatures/,
    );
  });

  // Spec: §7.6.3
  it("lets verifyKeys override the variable", async () => {
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", publicKeyOf(unrelatedKey()));
    const c = client(loadStub(await served(rule(), { signWith: VECTOR_KEY })), { verifyKeys: VECTOR_PUBLIC_KEY });
    const art = await c.loadArtifact("acme/rule");
    expect(art.id).toBe("acme/rule");
  });

  // Spec: §7.6.3
  it("counts empty variables as unset", async () => {
    for (const name of ["PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGN_KEY_PATH", "PODIUM_VERIFY_SIGNATURES"]) {
      vi.stubEnv(name, "");
    }
    const c = client(loadStub(await served(rule())));
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("never");
  });

  // Spec: §7.6.3
  it("refuses PODIUM_VERIFY_SIGNATURES=always without a key", async () => {
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "always");
    const stub = loadStub(await served(rule()));
    await expectCode(client(stub).loadArtifact("acme/rule"), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3
  it("parses no key under PODIUM_VERIFY_SIGNATURES=never", async () => {
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "never");
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64");
    const c = client(loadStub(await served(rule())));
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("never");
  });

  // Spec: §7.6.3
  it("lets an explicit never win over the policy variable", async () => {
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "always");
    const c = client(loadStub(await served(rule())), { verifySignatures: "never" });
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("never");
  });

  // Spec: §7.6.3
  it("lets the policy variable win over sync.yaml", async () => {
    await writeAt(join(iso.cwd, ".podium", "sync.yaml"), "defaults:\n  verify_signatures: never\n");
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "always");
    const c = client(loadStub(await served(rule(), { signWith: VECTOR_KEY })), { verifyKeys: VECTOR_PUBLIC_KEY });
    await c.loadArtifact("acme/rule");
    expect(c.verifySignatures).toBe("always");
  });

  // Spec: §7.6.3
  it("rejects an invalid PODIUM_VERIFY_SIGNATURES naming the variable", async () => {
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "medium-and-above");
    const stub = loadStub(await served(rule()));
    const err = await client(stub).loadArtifact("acme/rule").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect(err).not.toBeInstanceOf(RegistryError);
    expect((err as Error).message).toContain("PODIUM_VERIFY_SIGNATURES");
    expect(stub.requests).toEqual([]);
  });

  const unusable: Array<[string, () => Promise<Record<string, unknown>>]> = [
    [
      "a PODIUM_SIGN_KEY_PATH naming no file",
      async () => {
        vi.stubEnv("PODIUM_SIGN_KEY_PATH", join(iso.home, "absent.key"));
        return {};
      },
    ],
    [
      "a key file with no public line",
      async () => {
        const path = join(iso.home, "k.key");
        await writeAt(path, `verify: ${VECTOR_PUBLIC_KEY}\n`);
        vi.stubEnv("PODIUM_SIGN_KEY_PATH", path);
        return {};
      },
    ],
    [
      "an unparseable file at the default path",
      async () => {
        await writeAt(defaultKeyFile(), "public: not-base64\n");
        return {};
      },
    ],
    ["verifyKeys that does not decode", async () => ({ verifyKeys: "not-base64" })],
    ["verifyKeys with an empty entry", async () => ({ verifyKeys: `${VECTOR_PUBLIC_KEY},,${VECTOR_PUBLIC_KEY}` })],
  ];
  for (const [name, setup] of unusable) {
    // Spec: §7.6.3
    // Spec: §4.7.9
    it(`refuses ${name} before any request`, async () => {
      const opts = await setup();
      const stub = loadStub(await served(rule()));
      const err = await expectCode(client(stub, opts).loadArtifact("acme/rule"), UNAVAILABLE);
      expect(err.message).toContain("PODIUM_VERIFY_SIGNATURES=never");
      expect(stub.requests).toEqual([]);
    });
  }
});

describe("single loads", () => {
  // Spec: §4.7.10
  it("verifies a signed load", async () => {
    const body = await served(rule(), { signWith: VECTOR_KEY });
    const art = await client(loadStub(body), { verifyKeys: VECTOR_PUBLIC_KEY }).loadArtifact("acme/rule");
    expect(art.delivery_signature).toBe(body.delivery_signature);
  });

  // Spec: §4.7.10
  it("refuses a load signed by an unrelated key", async () => {
    const stub = loadStub(await served(rule(), { signWith: unrelatedKey() }));
    await expectCode(client(stub, { verifyKeys: VECTOR_PUBLIC_KEY }).loadArtifact("acme/rule"), "materialize.signature_invalid");
  });

  // Spec: §4.7.10
  it("refuses an unsigned load under always", async () => {
    const stub = loadStub(await served(rule()));
    await expectCode(client(stub, { verifyKeys: VECTOR_PUBLIC_KEY }).loadArtifact("acme/rule"), "materialize.signature_missing");
  });

  // Spec: §4.7.10
  it("refuses a missing or mismatched hash under never", async () => {
    const missing = await served(rule());
    delete missing.delivery_hash;
    await expectCode(client(loadStub(missing), { verifySignatures: "never" }).loadArtifact("acme/rule"), MISMATCH);
    const tampered = await served(rule());
    tampered.frontmatter = `${tampered.frontmatter as string}tampered\n`;
    await expectCode(client(loadStub(tampered), { verifySignatures: "never" }).loadArtifact("acme/rule"), MISMATCH);
  });

  // Spec: §4.7.10 — the record frames an absent id as the empty string, so the
  // client returns that attested value rather than the id it requested.
  it("returns the framed empty id of an id-less load", async () => {
    const body = rule();
    delete body.id;
    const art = await client(loadStub(await served(body))).loadArtifact("acme/rule");
    expect(art.id).toBe("");
  });

  async function manifestUrlResponse(doc: Uint8Array, link: Record<string, unknown> = {}) {
    const url = "http://store/objects/doc.md";
    const body = rule("acme/rule", {
      frontmatter: "",
      manifest_body: "",
      manifest_body_url: { presigned_url: url, ...link },
    });
    return served(body, { fetched: { [url]: doc } });
  }

  // Spec: §4.7.10
  it("refuses a manifest document that fails its link hash", async () => {
    const doc = enc("---\nname: big\n---\nBig body.\n");
    const store = objectStore({ "http://store/objects/doc.md": enc("---\nname: big\n---\nBig body.\ntampered") });
    const c = client(loadStub(await manifestUrlResponse(doc)));
    await expectCode(c.loadArtifact("acme/rule", undefined, { fetcher: store.fetcher }), MISMATCH);
  });

  // Spec: §4.7.10
  it("covers a manifest link with an empty hash by the delivery hash", async () => {
    const doc = enc("---\nname: big\n---\nBig body.\n");
    const c = client(loadStub(await manifestUrlResponse(doc, { content_hash: "" })));
    const ok = objectStore({ "http://store/objects/doc.md": doc });
    const art = await c.loadArtifact("acme/rule", undefined, { fetcher: ok.fetcher });
    expect([art.frontmatter, art.manifest_body]).toEqual(["---\nname: big\n---\nBig body.\n", "Big body.\n"]);
    const bad = objectStore({ "http://store/objects/doc.md": enc("---\nname: big\n---\nBog body.\n") });
    const err = await expectCode(c.loadArtifact("acme/rule", undefined, { fetcher: bad.fetcher }), MISMATCH);
    expect(err.message).toContain("recomputed delivery hash");
  });

  // Spec: §4.7.10
  it("refuses a large link with an empty hash at load", async () => {
    const url = "http://store/big.bin";
    const body = await served(rule("acme/rule", { large_resources: { "big.bin": { presigned_url: url } } }), {
      fetched: { [url]: enc("BIG") },
    });
    (body.large_resources as Record<string, Record<string, unknown>>)["big.bin"].content_hash = "";
    const err = await expectCode(client(loadStub(body)).loadArtifact("acme/rule"), MISMATCH);
    expect(err.message).toContain("recomputed delivery hash");
  });

  for (const policy of ["always", "never"] as const) {
    // Spec: §4.7.10
    it(`refuses a path served inline and by link under ${policy}`, async () => {
      const url = "http://store/objects/a.md";
      const body = await served(rule("acme/rule", { large_resources: { "a.md": { presigned_url: url } } }), {
        fetched: { [url]: enc("A") },
        signWith: VECTOR_KEY,
      });
      body.resources = { "a.md": "forged" };
      const store = objectStore({ [url]: "A" });
      const c = client(loadStub(body), { verifySignatures: policy, verifyKeys: VECTOR_PUBLIC_KEY });
      await expectCode(c.loadArtifact("acme/rule", undefined, { fetcher: store.fetcher }), MISMATCH);
      expect(store.requests).toEqual([]);
    });
  }

  // Spec: §4.7.10 — the TEST-1 non-UTF-8 manifest document is checked and
  // framed as raw bytes, and the returned text is its non-fatal decoding.
  it("verifies a non-UTF-8 manifest document and returns replaced text", async () => {
    const c0 = vector("responses", "manifest_body_url document that is not UTF-8");
    const fetched = fetchedOf(c0);
    const store: typeof fetch = async (url) => new Response(fetched[String(url)] as BodyInit);
    const art = await client(loadStub(unb64(c0.body_base64 as string))).loadArtifact("acme/big-rule", undefined, {
      fetcher: store,
    });
    const [doc] = Object.values(fetched);
    expect(art.frontmatter).toBe(new TextDecoder().decode(doc));
    expect(art.frontmatter).toContain("�");
    expect(art.delivery_hash).toBe(c0.served_hash);
  });

  // Spec: §4.7.10 — a link's unknown url member is never fetched; the body
  // comes from the exact-name presigned_url.
  it("never fetches an unknown url member", async () => {
    const doc = enc("---\nname: big\n---\nBig body.\n");
    const docURL = "http://store/objects/doc.md";
    const bigURL = "http://store/objects/big.bin";
    const body = rule("acme/rule", {
      frontmatter: "",
      manifest_body: "",
      manifest_body_url: { presigned_url: docURL, url: "http://decoy/doc" },
      large_resources: { "big.bin": { presigned_url: bigURL, url: "http://decoy/big" } },
    });
    const store = objectStore({ [docURL]: doc, [bigURL]: "BIGDATA" });
    const decoy = objectStore({ "http://decoy/doc": "decoy", "http://decoy/big": "decoy" });
    const routed: typeof fetch = async (url, init) =>
      (String(url).startsWith("http://decoy") ? decoy : store).fetcher(url, init);
    const c = client(loadStub(await served(body, { fetched: { [docURL]: doc, [bigURL]: enc("BIGDATA") } })));
    const art = await c.loadArtifact("acme/rule", undefined, { fetcher: routed });
    await withTempDir(async (dir) => {
      await art.materialize(dir, { fetcher: routed });
      expect(await readFile(join(dir, "acme", "rule", "big.bin"), "utf8")).toBe("BIGDATA");
    });
    expect(decoy.requests).toEqual([]);
  });

  // Spec: §4.7.10 — the vector link carrying an extra url member, ported.
  it("fetches the vector link from presigned_url", async () => {
    const c0 = vector("responses", "large link with an unknown url member");
    const fetched = fetchedOf(c0);
    const decoy = objectStore({ "https://objects.acme.com/elsewhere": "decoy" });
    const routed: typeof fetch = async (url, init) =>
      String(url) in fetched ? new Response(fetched[String(url)] as BodyInit) : decoy.fetcher(url, init);
    const art = await client(loadStub(unb64(c0.body_base64 as string))).loadArtifact("acme/link-url", "1.0.0");
    await withTempDir(async (dir) => {
      await art.materialize(dir, { fetcher: routed });
      expect(await readdir(join(dir, "acme", "link-url"))).toContain("big.bin");
    });
    expect(decoy.requests).toEqual([]);
  });

  // Spec: §7.6.3 — materialize fetches and checks every large resource before
  // the first write, so a later mismatch leaves no file of the artifact.
  it("materialize writes nothing when a later resource fails", async () => {
    const fetched = { "http://store/a.bin": enc("A"), "http://store/b.bin": enc("B") };
    const links = { "a.bin": { presigned_url: "http://store/a.bin" }, "b.bin": { presigned_url: "http://store/b.bin" } };
    const art = await client(loadStub(await served(rule("acme/rule", { large_resources: links }), { fetched }))).loadArtifact(
      "acme/rule",
    );
    const tampered = objectStore({ "http://store/a.bin": "A", "http://store/b.bin": "tampered" });
    await withTempDir(async (dir) => {
      await expectCode(art.materialize(dir, { fetcher: tampered.fetcher }), MISMATCH);
      expect(await readdir(dir)).toEqual([]);
    });
  });
});

describe("batch loads", () => {
  // Spec: §7.6.3
  it("names an entry that serves a path twice as an error", async () => {
    const twice = await served(
      entry("acme/b", { resources: [{ path: "a.md", inline: "A" }, { path: "a.md", presigned_url: "http://store/a.md" }] }),
      { fetched: { "http://store/a.md": enc("A") } },
    );
    const stub = batchStub([await served(entry("acme/a")), twice, await served(entry("acme/c"))]);
    expect(codes(await client(stub).loadArtifacts(["acme/a", "acme/b", "acme/c"]))).toEqual(["ok", MISMATCH, "ok"]);
  });

  // Spec: §7.6.3
  it("returns a tampered entry as an error and the others ok", async () => {
    const tampered = await served(entry("acme/b"));
    tampered.manifest_body = "changed";
    const out = await client(batchStub([await served(entry("acme/a")), tampered])).loadArtifacts(["acme/a", "acme/b"]);
    expect(codes(out)).toEqual(["ok", MISMATCH]);
    expect(out[1].id).toBe("acme/b");
    expect(out[1].error).toBeInstanceOf(RegistryError);
  });

  // Spec: §4.7.10
  it("returns an entry whose inline body fails its hash as an error", async () => {
    const e = await served(entry("acme/b", { resources: [{ path: "a.md", inline: "A" }] }));
    (e.resources as Record<string, unknown>[])[0].inline = "forged";
    const out = await client(batchStub([await served(entry("acme/a")), e])).loadArtifacts(["acme/a", "acme/b"]);
    expect(codes(out)).toEqual(["ok", MISMATCH]);
  });

  // Spec: §4.7.10 — the registry frames the body's true digest; the
  // reference serves "".
  it("returns an inline reference with an empty hash as an error", async () => {
    const e = await served(entry("acme/b", { resources: [{ path: "a.md", inline: "A" }] }));
    (e.resources as Record<string, unknown>[])[0].content_hash = "";
    const stub = batchStub([await served(entry("acme/a")), e, await served(entry("acme/c"))]);
    expect(codes(await client(stub).loadArtifacts(["acme/a", "acme/b", "acme/c"]))).toEqual(["ok", MISMATCH, "ok"]);
  });

  // Spec: §7.6.3 — the TEST-4 reference-normalization case, ported.
  it("keeps only the members step 2 reads on each reference", async () => {
    const link = { path: "big.bin", presigned_url: "http://store/big", inline: "mismatched", extra: "x" };
    const inline = { path: "a.md", inline: "A", extra: "x" };
    const e = await served(entry("acme/a", { resources: [link, inline] }), {
      fetched: { "http://store/big": enc("BIG") },
    });
    const [result] = await client(batchStub([e])).loadArtifacts(["acme/a"]);
    expect(result.status).toBe("ok");
    const [linkRef, inlineRef] = result.resources ?? [];
    expect(Object.keys(linkRef).sort()).toEqual(["content_hash", "path", "presigned_url"]);
    expect("inline" in linkRef).toBe(false);
    expect(Object.keys(inlineRef).sort()).toEqual(["content_hash", "inline", "inline_base64", "path"]);
    expect(inlineRef).toMatchObject({ path: "a.md", inline: "A", inline_base64: false });
  });

  // Spec: §7.6.3
  it("checks a presigned reference in BatchResult.materialize", async () => {
    const e = await served(entry("acme/a", { resources: [{ path: "big.bin", presigned_url: "http://store/big" }] }), {
      fetched: { "http://store/big": enc("BIG") },
    });
    const [result] = await client(batchStub([e])).loadArtifacts(["acme/a"]);
    await withTempDir(async (dir) => {
      await expectCode(result.materialize(join(dir, "bad"), { fetcher: objectStore({ "http://store/big": "forged" }).fetcher }), MISMATCH);
      expect(await readdir(dir)).toEqual([]);
      await result.materialize(join(dir, "ok"), { fetcher: objectStore({ "http://store/big": "BIG" }).fetcher });
      expect(await readFile(join(dir, "ok", "acme", "a", "big.bin"), "utf8")).toBe("BIG");
    });
  });

  // Spec: §7.6.3
  // Spec: §4.7.9 — the TEST-4 batch signature-policy cases, ported.
  it("applies the signature policy per entry", async () => {
    const stub = batchStub([
      await served(entry("acme/a"), { signWith: VECTOR_KEY }),
      await served(entry("acme/b"), { signWith: unrelatedKey() }),
      await served(entry("acme/c")),
    ]);
    const out = await client(stub, { verifyKeys: VECTOR_PUBLIC_KEY }).loadArtifacts(["acme/a", "acme/b", "acme/c"]);
    expect(codes(out)).toEqual(["ok", "materialize.signature_invalid", "materialize.signature_missing"]);
    const unsigned = batchStub([await served(entry("acme/a")), await served(entry("acme/b"))]);
    expect(codes(await client(unsigned, { verifySignatures: "never" }).loadArtifacts(["acme/a", "acme/b"]))).toEqual([
      "ok",
      "ok",
    ]);
  });

  // Spec: §7.6.3
  it("rejects a batch body that is not UTF-8 and returns no entries", async () => {
    const stub = batchStub(new Uint8Array([0x5b, 0xff, 0x5d]));
    await expectCode(client(stub).loadArtifacts(["acme/a"]), MISMATCH);
  });

  // Spec: §7.6.3 — loadArtifacts caps a chunk at 50 ids, and a refused second
  // chunk rejects the call without the first chunk's entries.
  it("rejects when a later chunk is not UTF-8", async () => {
    const ids = Array.from({ length: 51 }, (_, i) => `acme/a${i}`);
    const first = await Promise.all(ids.slice(0, 50).map((id) => served(entry(id))));
    let call = 0;
    const stub = batchStub(() => (call++ === 0 ? first : new Uint8Array([0xff])));
    await expectCode(client(stub).loadArtifacts(ids), MISMATCH);
    expect(stub.requests).toHaveLength(2);
  });

  // Spec: §7.6.3 — the TEST-4 per-entry step-2 case, ported.
  it("refuses a mistyped entry and a path-less reference per entry", async () => {
    const mistyped = { ...(await served(entry("acme/b"))), sensitivity: 5 };
    const pathless = await served(entry("acme/c", { resources: [{ path: "a.md", inline: "A" }] }));
    delete (pathless.resources as Record<string, unknown>[])[0].path;
    const stub = batchStub([await served(entry("acme/a")), mistyped, pathless]);
    const out = await client(stub).loadArtifacts(["acme/a", "acme/b", "acme/c"]);
    expect(codes(out)).toEqual(["ok", MISMATCH, MISMATCH]);
    expect(out[1].error).toBeInstanceOf(RegistryError);
    expect(out[2].error).toBeInstanceOf(RegistryError);
  });

  // Spec: §7.6.3
  // Spec: §7.6.2
  it("surfaces an HTTP error envelope on the batch path", async () => {
    const stub = batchStub(new Response(JSON.stringify({ code: "auth.forbidden", message: "no" }), { status: 403 }));
    await expectCode(client(stub).loadArtifacts(["acme/a"]), "auth.forbidden");
  });

  // Spec: §7.6.3
  // Spec: §7.6.2 — an error entry keeps the registry's envelope.
  it("keeps an error entry's envelope", async () => {
    const stub = batchStub([{ id: "acme/x", status: "error", error: { code: "registry.not_found", message: "gone" } }]);
    const [result] = await client(stub).loadArtifacts(["acme/x"]);
    expect(result.status).toBe("error");
    expect(result.error).toMatchObject({ code: "registry.not_found", message: "gone" });
  });
});

describe("TypeScript client surface", () => {
  function badKeyClient(stub: { fetcher: typeof fetch }): Client {
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64");
    vi.stubEnv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", undefined);
    vi.stubEnv("PODIUM_OAUTH_TOKEN_URL", undefined);
    return client(stub);
  }

  function anyRoute(): ReturnType<typeof stubRegistry> {
    const reply = () => ({ total_matched: 0, results: [] });
    return stubRegistry({
      "/v1/load_artifact": reply,
      "/v1/search_artifacts": reply,
      "/v1/artifacts:batchLoad": () => [],
      "/v1/events": () => new Uint8Array(),
      "/.well-known/oauth-authorization-server": reply,
    });
  }

  // Spec: §7.6.3
  it("constructs with no I/O under a malformed key", () => {
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64");
    const stub = anyRoute();
    expect(() => client(stub)).not.toThrow();
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3
  it("rejects fromEnv under a malformed key", async () => {
    vi.stubEnv("PODIUM_REGISTRY", REGISTRY);
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", "not-base64");
    await expectCode(Client.fromEnv(), UNAVAILABLE);
  });

  // Spec: §7.6.3
  it("resolves fromEnv before construction", async () => {
    vi.stubEnv("PODIUM_REGISTRY", REGISTRY);
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", VECTOR_PUBLIC_KEY);
    const c = await Client.fromEnv();
    expect(c.verifySignatures).toBe("always");
  });

  // Spec: §7.6.3
  it("refuses the first loadArtifact before any request", async () => {
    const stub = anyRoute();
    await expectCode(badKeyClient(stub).loadArtifact("acme/rule"), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3
  it("refuses the first searchArtifacts before any request", async () => {
    const stub = anyRoute();
    await expectCode(badKeyClient(stub).searchArtifacts("q"), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3 — the batch POST does not pass through get.
  it("refuses the first loadArtifacts before any request", async () => {
    const stub = anyRoute();
    await expectCode(badKeyClient(stub).loadArtifacts(["acme/rule"]), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3 — the event stream does not pass through get.
  it("refuses the first subscribe next() before any request", async () => {
    const stub = anyRoute();
    const iter = badKeyClient(stub).subscribe(["artifact.published"])[Symbol.asyncIterator]();
    await expectCode(iter.next(), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3 — the discoverIdp request does not pass through get.
  it("refuses startLogin before the discovery request", async () => {
    const stub = anyRoute();
    await expectCode(badKeyClient(stub).startLogin(), UNAVAILABLE);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3
  it("reuses the memoized resolution", async () => {
    const stub = anyRoute();
    const c = badKeyClient(stub);
    const first = await c.loadArtifact("acme/rule", "1.0.0").catch((e: unknown) => e);
    vi.stubEnv("PODIUM_SIGNATURE_VERIFY_KEY", undefined);
    const second = await c.searchArtifacts("q").catch((e: unknown) => e);
    expect(second).toBe(first);
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3
  it("rejects an invalid policy variable on the first load before any request", async () => {
    vi.stubEnv("PODIUM_VERIFY_SIGNATURES", "medium-and-above");
    const stub = anyRoute();
    const err = await client(stub).loadArtifact("acme/rule").catch((e: unknown) => e);
    expect((err as Error).message).toContain("PODIUM_VERIFY_SIGNATURES");
    expect(stub.requests).toEqual([]);
  });

  // Spec: §7.6.3 — the TEST-1 __proto__ response keeps the path as an own
  // property and materializes the file.
  it("keeps a __proto__ resource path", async () => {
    const c0 = vector("responses", "resource named __proto__");
    const art = await client(loadStub(unb64(c0.body_base64 as string))).loadArtifact("acme/proto");
    expect(Object.hasOwn(art.resources ?? {}, "__proto__")).toBe(true);
    expect(art.delivery_hash).toBe(c0.served_hash);
    await withTempDir(async (dir) => {
      await art.materialize(dir);
      expect(await readFile(join(dir, "acme", "proto", "__proto__"), "utf8")).toBe("prototype\n");
    });
  });

  // Spec: §7.6.3
  it("materializes a batch inline reference at path __proto__", async () => {
    const e = await served(entry("acme/a", { resources: [{ path: "__proto__", inline: "P" }] }));
    const [result] = await client(batchStub([e])).loadArtifacts(["acme/a"]);
    await withTempDir(async (dir) => {
      await result.materialize(dir);
      expect(await readFile(join(dir, "acme", "a", "__proto__"), "utf8")).toBe("P");
    });
  });

  // Spec: §7.6.3
  it("returns large_resources as plain objects", async () => {
    const url = "http://store/big.bin";
    const body = await served(
      rule("acme/rule", { large_resources: { "big.bin": { presigned_url: url, size: 5242880, content_type: 7 } } }),
      { fetched: { [url]: enc("BIG") } },
    );
    const art = await client(loadStub(body)).loadArtifact("acme/rule");
    const large = art.large_resources ?? {};
    expect(large).not.toBeInstanceOf(Map);
    expect(large["big.bin"]).not.toBeInstanceOf(Map);
    expect(large["big.bin"].size).toBe(5242880);
    expect(large["big.bin"].url).toBe(url);
    expect(large["big.bin"].content_type).toBeUndefined();
  });

  // Spec: §7.6.3 — the §4.7.4 lifecycle members survive verification.
  it("returns the lifecycle members of a load and a batch entry", async () => {
    const lifecycle = { deprecated: true, replaced_by: "acme/next", deprecation_warning: "use acme/next" };
    const art = await client(loadStub(await served(rule("acme/rule", lifecycle)))).loadArtifact("acme/rule");
    expect(art).toMatchObject(lifecycle);
    const [result] = await client(batchStub([await served(entry("acme/a", lifecycle))])).loadArtifacts(["acme/a"]);
    expect(result).toMatchObject(lifecycle);
    const mistyped = await client(loadStub(await served(rule("acme/rule", { deprecated: "yes" })))).loadArtifact(
      "acme/rule",
    );
    expect(mistyped.deprecated).toBeUndefined();
  });

  // Spec: §4.7.10 — a caller-built LoadedArtifact keeps its public url field.
  it("materializes a caller-built large resource from url", async () => {
    const art = new LoadedArtifact({
      id: "a/b",
      type: "context",
      frontmatter: "---\ntype: context\n---\n",
      large_resources: { "big.bin": { url: "https://store/presigned" } },
    });
    await withTempDir(async (dir) => {
      await art.materialize(dir, { fetcher: objectStore({ "https://store/presigned": "BIGDATA" }).fetcher });
      expect(await readFile(join(dir, "a", "b", "big.bin"), "utf8")).toBe("BIGDATA");
    });
  });

  // Spec: §7.6.3 — a caller-built link with no URL is refused before any
  // fetch or write.
  it("refuses a caller-built large resource with no URL", async () => {
    const art = new LoadedArtifact({
      id: "a/b",
      type: "context",
      frontmatter: "---\ntype: context\n---\n",
      large_resources: { "big.bin": { url: "" } },
    });
    const store = objectStore({});
    await withTempDir(async (dir) => {
      await expect(art.materialize(dir, { fetcher: store.fetcher })).rejects.toThrow(/no presigned URL/);
      expect(await readdir(dir)).toEqual([]);
    });
    expect(store.requests).toEqual([]);
  });

  // Spec: §7.6.3 — CODE-8 "Runtime boundary": with no node:fs/promises and no
  // process global, a pinned loadArtifact and loadArtifacts still verify under
  // the never default.
  it("runs a pinned load and a batch load without Node", async () => {
    const one = vector("responses", "spec record without SKILL.md");
    const batch = vector("batch", "entry without sensitivity");
    const stub = stubRegistry({
      "/v1/load_artifact": unb64(one.body_base64 as string),
      "/v1/artifacts:batchLoad": unb64(batch.body_base64 as string),
    });
    const overlayPath = await mkdtemp(join(tmpdir(), "podium-ov-"));
    vi.doMock("node:fs/promises", () => {
      throw new Error("no filesystem");
    });
    let art: LoadedArtifact | undefined;
    let results: BatchResult[] = [];
    let policy: string | undefined;
    try {
      vi.stubGlobal("process", undefined);
      const c = new Client({ registry: REGISTRY, fetcher: stub.fetcher, overlayPath });
      art = await c.loadArtifact("team/rule", "1.0.0");
      results = await c.loadArtifacts(["acme/batch-first"]);
      policy = c.verifySignatures;
    } finally {
      vi.unstubAllGlobals();
      vi.doUnmock("node:fs/promises");
      await rm(overlayPath, { recursive: true, force: true });
    }
    expect(policy).toBe("never");
    expect(art?.delivery_hash).toBe(one.served_hash);
    expect(results[0].status).toBe("ok");
    expect(results[0].delivery_hash).toBe((batch.entries as Case[])[0].served_hash);
    expect(stub.requests).toHaveLength(2);
  });

  // Spec: §7.6.3 — delivery.ts adds no Node touchpoint.
  it("keeps delivery.ts free of Node touchpoints", async () => {
    const src = await readFile(new URL("./delivery.ts", import.meta.url), "utf-8");
    expect(src).not.toMatch(/\bBuffer\b/);
    expect(src).not.toMatch(/\bprocess\b\s*\??\./);
    expect(src).not.toMatch(/["']node:/);
  });
});

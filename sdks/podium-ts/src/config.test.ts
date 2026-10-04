// The §4.7.9 policy and key-set resolution across the §7.5.2 sync.yaml scopes.

import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { describe, expect, it, vi } from "vitest";

import { resolveVerification } from "./config.js";
import { RegistryError } from "./errors.js";
import { isolateVerification, keyFileOf, VECTOR_KEY, VECTOR_PUBLIC_KEY } from "./test_support.js";

const iso = isolateVerification();

async function writeAt(path: string, text: string): Promise<void> {
  await mkdir(join(path, ".."), { recursive: true });
  await writeFile(path, text);
}

function syncYaml(dir: string, file: string, policy: string): Promise<void> {
  return writeAt(join(dir, ".podium", file), `defaults:\n  verify_signatures: ${policy}\n`);
}

describe("resolveVerification", () => {
  // Spec: §7.6.3
  // Spec: §7.5.2 — sync.local.yaml outranks the workspace sync.yaml, which
  // outranks the user-global file, and a trailing comment is ignored.
  it("reads defaults.verify_signatures with scope precedence", async () => {
    const warn = vi.spyOn(process, "emitWarning").mockImplementation(() => undefined);
    await syncYaml(iso.home, "sync.yaml", "never");
    await syncYaml(iso.cwd, "sync.yaml", "always");
    await syncYaml(iso.cwd, "sync.local.yaml", "never   # off while drafting");
    const v = await resolveVerification({}, {}, iso.cwd, iso.home);
    expect(v.policy).toBe("never");
    expect(String(warn.mock.calls[0][0])).toContain(join(iso.cwd, ".podium", "sync.local.yaml"));

    await writeAt(join(iso.cwd, ".podium", "sync.local.yaml"), "defaults:\n  registry: http://reg\n");
    const err = await resolveVerification({}, {}, iso.cwd, iso.home).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RegistryError);
    expect((err as RegistryError).code).toBe("config.signature_provider_unavailable");
  });

  // Spec: §7.6.3 — a never from sync.yaml emits the stale-never warning.
  it("warns through process.emitWarning on a sync.yaml never", async () => {
    const warn = vi.spyOn(process, "emitWarning").mockImplementation(() => undefined);
    await syncYaml(iso.home, "sync.yaml", "never");
    await resolveVerification({}, {}, iso.cwd, iso.home);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(String(warn.mock.calls[0][0])).toMatch(/^signature verification is off because defaults\.verify_signatures is never in /);
  });

  // Spec: §7.6.3 — the environment's never prints no warning.
  it("prints no warning when the variable supplies never", async () => {
    const warn = vi.spyOn(process, "emitWarning").mockImplementation(() => undefined);
    await syncYaml(iso.home, "sync.yaml", "never");
    const v = await resolveVerification({}, { PODIUM_VERIFY_SIGNATURES: "never" }, iso.cwd, iso.home);
    expect(v.policy).toBe("never");
    expect(warn).not.toHaveBeenCalled();
  });

  // Spec: §7.6.3 — an invalid sync.yaml value is refused naming its file.
  it("rejects an invalid sync.yaml value naming the file", async () => {
    await syncYaml(iso.cwd, "sync.yaml", "sometimes");
    const err = await resolveVerification({}, {}, iso.cwd, iso.home).catch((e: unknown) => e);
    expect(err).not.toBeInstanceOf(RegistryError);
    expect((err as Error).message).toContain(join(iso.cwd, ".podium", "sync.yaml"));
  });

  // Spec: §7.6.3
  it("resolves the key file named by PODIUM_SIGN_KEY_PATH", async () => {
    const path = join(iso.home, "registry.key");
    await writeAt(path, keyFileOf(VECTOR_KEY));
    const v = await resolveVerification({}, { PODIUM_SIGN_KEY_PATH: path }, iso.cwd, iso.home);
    expect(v.policy).toBe("always");
    expect(v.keys.map((k) => Buffer.from(k).toString("base64"))).toEqual([VECTOR_PUBLIC_KEY]);
  });

  // Spec: §7.6.3
  it("prefers verifyKeys, then the variable, then the key file", async () => {
    const v = await resolveVerification(
      { verifyKeys: VECTOR_PUBLIC_KEY },
      { PODIUM_SIGNATURE_VERIFY_KEY: "not-base64", PODIUM_SIGN_KEY_PATH: "/absent" },
      iso.cwd,
      iso.home,
    );
    expect(v.keys).toHaveLength(1);
    const env = await resolveVerification({}, { PODIUM_SIGNATURE_VERIFY_KEY: VECTOR_PUBLIC_KEY }, iso.cwd, iso.home);
    expect(env.keys).toHaveLength(1);
  });

  // Spec: §7.6.3 — CODE-8 "Runtime boundary": a failed node:fs import reads as
  // a runtime with no filesystem, so no sync.yaml scope is read and a key file
  // cannot be loaded.
  it("treats a failed node:fs import as no filesystem", async () => {
    await syncYaml(iso.cwd, "sync.yaml", "always");
    vi.doMock("node:fs/promises", () => {
      throw new Error("no filesystem");
    });
    try {
      const v = await resolveVerification({}, {}, iso.cwd, iso.home);
      expect(v.policy).toBe("never");
      const err = await resolveVerification({}, { PODIUM_SIGN_KEY_PATH: "/k" }, iso.cwd, undefined).catch(
        (e: unknown) => e,
      );
      expect((err as RegistryError).code).toBe("config.signature_provider_unavailable");
      expect((err as Error).message).toContain("no filesystem");
    } finally {
      vi.doUnmock("node:fs/promises");
    }
  });
});

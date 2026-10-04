# @lennylabs/podium-sdk

TypeScript client for the Podium registry.

```sh
npm install @lennylabs/podium-sdk
```

```ts
import { Client } from "@lennylabs/podium-sdk";

const client = await Client.fromEnv();
const results = await client.searchArtifacts("variance", { type: "skill" });
const artifact = await client.loadArtifact(results.results![0].id);
console.log(artifact.manifest_body);
```

The client runs the §4.7.10 delivery check on every registry-served
`loadArtifact` response and every `ok` `loadArtifacts` item (§7.6.3). It
recomputes `delivery_hash` under every policy and verifies `delivery_signature`
with the registry-managed verifier when the policy is `always`. The
`verifySignatures` option (`"never"` or `"always"`) sets the policy ahead of
`PODIUM_VERIFY_SIGNATURES` and `defaults.verify_signatures`, and the
`verifyKeys` option takes a comma-separated list of base64 Ed25519 public keys
in place of `PODIUM_SIGNATURE_VERIFY_KEY`. The default is `always` when a
verification key is configured, through `verifyKeys`,
`PODIUM_SIGNATURE_VERIFY_KEY`, `PODIUM_SIGN_KEY_PATH`, or a key file at
`~/.podium/standalone/registry-signing.key`, and `never` otherwise. The client
resolves the policy and keys in `Client.fromEnv()`, or before the first registry
request of a directly constructed client, and verifies through the Web Crypto
API with no added dependency.

Run the tests with:

```sh
cd sdks/podium-ts
npm install
npm test
```

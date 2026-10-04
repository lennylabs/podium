# podium-py

Thin HTTP client for the Podium registry.

Distributed on PyPI as `podium-sdk`; the import name is `podium`:

```sh
pip install 'podium-sdk[verify]'
```

```python
from podium import Client

client = Client.from_env()
results = client.search_artifacts("variance", type="skill")
artifact = client.load_artifact(results.results[0].id)
print(artifact.manifest_body)
```

The client covers the meta-tool surface (`search_artifacts`,
`load_artifact`, `load_artifacts`, `search_domains`, `load_domain`,
`dependents_of`, `preview_scope`, and `subscribe`). `Client.from_env()`
resolves the registry from `PODIUM_REGISTRY` and the `sync.yaml` scopes
(§7.5.2). `search_artifacts` and `load_artifact` merge the workspace overlay
client-side (§6.4). `client.login()` runs the `oauth-device-code` flow and
attaches the access token as the `Authorization: Bearer` credential on every
request (§6.3). `client.start_login()` returns a single-use pending handle
carrying the verification URL and the user code without printing or polling,
and `client.finish_login(handle)` polls until the flow completes and installs
the token on the client (§6.3).

The client runs the §4.7.10 delivery check on every registry-served
`load_artifact` response and every `ok` `load_artifacts` item (§7.6.3). It
recomputes `delivery_hash` under every policy and verifies `delivery_signature`
with the registry-managed verifier when the policy is `always`. The
`verify_signatures` argument (`"never"` or `"always"`) sets the policy ahead of
`PODIUM_VERIFY_SIGNATURES` and `defaults.verify_signatures`, and the
`verify_keys` argument takes a comma-separated list of base64 Ed25519 public
keys in place of `PODIUM_SIGNATURE_VERIFY_KEY`. The default is `always` when a
verification key is configured, through `verify_keys`,
`PODIUM_SIGNATURE_VERIFY_KEY`, `PODIUM_SIGN_KEY_PATH`, or a key file at
`~/.podium/standalone/registry-signing.key`, and `never` otherwise. Signature
verification needs the `podium-sdk[verify]` extra, and a client that resolves
`always` without it raises `config.signature_provider_unavailable` at
construction.

## Test

```sh
cd sdks/podium-py
pip install -e '.[verify]'
pytest
```

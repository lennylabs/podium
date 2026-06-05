# podium-py

Thin HTTP client for the Podium registry.

Distributed on PyPI as `podium-sdk`; the import name is `podium`:

```sh
pip install podium-sdk
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
request (§7.7).

## Test

```sh
cd sdks/podium-py
pip install -e .
pytest
```

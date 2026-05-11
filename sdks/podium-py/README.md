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

The client covers the meta-tool surface. OAuth device code, streaming
subscriptions, and dependency walks remain on the roadmap.

## Test

```sh
cd sdks/podium-py
pip install -e .
pytest
```

# @podium/sdk

TypeScript client for the Podium registry (spec §7.6).

```ts
import { Client } from "@podium/sdk";

const client = Client.fromEnv();
const results = await client.searchArtifacts("variance", { type: "skill" });
const artifact = await client.loadArtifact(results.results![0].id);
console.log(artifact.manifest_body);
```

Tests skip when the repo's `.phase` file reads below `14`. Run them with:

```sh
cd sdks/podium-ts
npm install
npm test
```

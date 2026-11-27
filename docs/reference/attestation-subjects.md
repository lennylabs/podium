---
title: Attestation subjects predicate
nav_title: Attestation subjects
nav_order: 7
permalink: /attestation/subjects/v1.html
description: The in-toto predicate type https://lennylabs.github.io/podium/attestation/subjects/v1, which `podium attest --format statement` writes with an empty predicate.
---

# Attestation subjects predicate

This page defines the in-toto predicate type `https://lennylabs.github.io/podium/attestation/subjects/v1`. The URI of the type is the address of this page.

## Meaning

A statement of this type states that its signer attests the statement's subjects: each subject names an artifact or a layer and carries its package digest or layer digest. The statement states nothing else about the subjects. It records no build, no review, and no scan result.

## Predicate

The predicate is the empty JSON object. A producer writes `{}`, and a consumer of this type reads no member of the predicate.

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {"name": ".podium/layer/skills", "digest": {"sha256": "c07d...3b"}},
    {"name": "finance/close-books@1.4.0", "digest": {"sha256": "9f2c...e1"}}
  ],
  "predicateType": "https://lennylabs.github.io/podium/attestation/subjects/v1",
  "predicate": {}
}
```

## Producers and consumers

`podium attest --format statement` writes statements of this type by default. A tool that signs a prepared statement, such as `cosign attest-blob --statement`, signs them. [`podium attest`](cli#podium-attest) in the CLI reference describes the subject names, the digests, and the files the command writes.

Podium reads `predicateType` and never reads `predicate`, so a statement of this type and a statement of another type over the same subjects attest the same digests. A layer trust requirement can name a predicate type, and a requirement that names this type accepts the statements `podium attest` writes by default. `podium attest --predicate-type` sets another type when a statement must carry the type of another signing recipe. The predicate stays empty, so a type whose schema requires members, such as SLSA provenance, gives a statement that tools which check the predicate refuse.

## Versioning

The `v1` in the URI names this definition. A change to the meaning or to the predicate takes a new URI.

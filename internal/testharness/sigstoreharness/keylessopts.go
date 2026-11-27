package sigstoreharness

// The EnvOpt adjustments below target the bundle conditions that the
// §4.7.9 keyless bundle shares with §4.7.11 and the keyless-only ones.

// WithKeylessSCT embeds an SCT the harness CT log key signs in the leaf,
// which a trusted root built with WithCTLogKey verifies.
func WithKeylessSCT() EnvOpt { return func(c *envConfig) { c.sct = sctTrusted } }

// WithKeylessForeignSCT embeds an SCT signed by a key no trusted root
// lists.
func WithKeylessForeignSCT() EnvOpt { return func(c *envConfig) { c.sct = sctForeign } }

// WithP384Leaf issues the leaf over an ECDSA P-384 key and signs the
// digest with it.
func WithP384Leaf() EnvOpt { return func(c *envConfig) { c.p384Leaf = true } }

// WithForeignLogID writes the log ID of a key no trusted root lists into
// the entry, while the checkpoint stays signed by the harness log key.
func WithForeignLogID() EnvOpt { return func(c *envConfig) { c.foreignLogID = true } }

// WithKeylessV1Body writes a Rekor v1 hashedrekord 0.0.1 body that binds
// the bundle's digest, signature, and leaf in place of the Rekor v2 body.
// The kindVersion label stays hashedrekord 0.0.2 unless
// WithKeylessKindVersionLabel sets it.
func WithKeylessV1Body() EnvOpt { return func(c *envConfig) { c.v1Body = true } }

// WithKeylessV1Checkpoint proves the entry under a Rekor v1 checkpoint,
// whose origin ends in a tree ID, signed by the P-256 log key.
func WithKeylessV1Checkpoint() EnvOpt { return func(c *envConfig) { c.v1Checkpoint = true } }

// WithKeylessKindVersionLabel sets the entry's kindVersion label, which is
// unsigned bundle metadata, without changing the body.
func WithKeylessKindVersionLabel(kind, version string) EnvOpt {
	return func(c *envConfig) { c.label = &kindVersionJSON{Kind: kind, Version: version} }
}

// WithKeylessMediaType sets the bundle's media type.
func WithKeylessMediaType(mt string) EnvOpt { return func(c *envConfig) { c.mediaType = mt } }

// WithMessageDigestAlgorithm writes alg, a protobuf HashAlgorithm name such
// as SHA2_384, as the message digest's algorithm. The digest bytes stay
// the SHA-256 digest of the content hash.
func WithMessageDigestAlgorithm(alg string) EnvOpt {
	return func(c *envConfig) { c.digestAlg = alg }
}

// WithTimestampOptions applies opts to the default token, so a keyless
// bundle carries a token that TimestampToken would build with them.
func WithTimestampOptions(opts ...TSOpt) EnvOpt {
	return tsMod(func(_ *Harness, ts *tsConfig) error {
		for _, o := range opts {
			o(ts)
		}
		return nil
	})
}

// WithExtraTimestamp adds, after the default token, one RFC 3161 token
// over the signature from each named authority.
func WithExtraTimestamp(authorities ...TSA) EnvOpt {
	return func(c *envConfig) { c.extraTS = append(c.extraTS, authorities...) }
}

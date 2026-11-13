package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// openAuditStream opens the registry's §8.3 audit stream over the
// PODIUM_AUDIT_LOG_PATH destination list, with the §13.12 transport and
// queue settings read from the environment when it runs. Every error wraps
// audit.ErrConfig, and run refuses it with config.audit_sink_unavailable
// whatever the anchoring setting (§13.12).
//
// The stream's senders run on context.Background rather than the serve
// context: the shutdown signal cancels the serve context before the HTTP
// drain starts, and the senders keep delivering the records drained requests
// chain until Stream.Shutdown closes them.
//
// Spec: §8.3, §13.8, §13.12
func openAuditStream(cfg *Config, obs audit.Observer) (*audit.Stream, error) {
	queueMax, err := audit.ParseQueueMaxBytes(os.Getenv("PODIUM_AUDIT_QUEUE_MAX_BYTES"))
	if err != nil {
		return nil, err
	}
	return audit.Open(context.Background(), cfg.auditLogPath, audit.OpenOptions{
		Service:       "podium-registry",
		TracingStdout: os.Getenv("PODIUM_TRACING") == "stdout",
		HeadersFile:   os.Getenv("PODIUM_AUDIT_HEADERS_FILE"),
		CAFile:        os.Getenv("PODIUM_AUDIT_TLS_CA_FILE"),
		CertFile:      os.Getenv("PODIUM_AUDIT_TLS_CERT_FILE"),
		KeyFile:       os.Getenv("PODIUM_AUDIT_TLS_KEY_FILE"),
		QueueMaxBytes: queueMax,
		Observer:      obs,
	})
}

// auditSinkUnavailable builds the config.audit_sink_unavailable refusal of
// an audit configuration openAuditStream cannot use. The audit.Open text
// that follows the code names the variable at fault.
//
// Spec: §6.10, §13.12
func auditSinkUnavailable(err error) error {
	return fmt.Errorf("config.audit_sink_unavailable: %w", err)
}

// loadAnchorSigner loads and checks the §8.6 audit anchor key ahead of the
// §13.4 first-start rewrite and the bootstrap ingest, so a start it refuses
// changes no stored row. It returns the key and whether anchoring runs.
//
// With the interval at 0 nothing is read. run refuses an unusable audit
// configuration before this call, so the stream it receives opened. A
// stream with no file destination leaves no chain to anchor and only warns.
// With registry signing on, an anchor key that the registry key file also
// carries refuses, because the anchor signature carries no purpose label.
// Spec: §8.6, §13.12.
func loadAnchorSigner(cfg *Config, stream *audit.Stream, registryKey sign.RegistryManagedKey, signingOn bool) (sign.RegistryManagedKey, bool, error) {
	if cfg.auditAnchorInterval <= 0 {
		return sign.RegistryManagedKey{}, false, nil
	}
	if stream.File() == nil {
		log.Printf("warning: audit anchor disabled (no sink)")
		return sign.RegistryManagedKey{}, false, nil
	}
	key, path, err := loadOrGenerateAuditSigner(cfg.auditSigningKeyPath)
	if err != nil {
		return sign.RegistryManagedKey{}, false, anchorKeyUnavailable(path, err)
	}
	if signingOn {
		// registrySignerFor already resolved this path to load the registry
		// key, so the resolution cannot fail here; the raw value is a fallback
		// for the message alone.
		registryPath, rerr := registrySigningKeyPath(os.Getenv("PODIUM_SIGN_KEY_PATH"))
		if rerr != nil {
			registryPath = os.Getenv("PODIUM_SIGN_KEY_PATH")
		}
		if err := refuseSharedAnchorKey(key, path, registryKey, registryPath); err != nil {
			return sign.RegistryManagedKey{}, false, err
		}
	}
	return key, true, nil
}

// anchorKeyUnavailable builds the config.audit_anchor_key_unavailable refusal.
// An empty path means the default path could not be resolved. The cause is
// never sign.ErrRegistryManagedUnavailable, so errors.Is never classifies an
// anchor failure as a registry-key failure. Spec: §8.6, §13.12.
func anchorKeyUnavailable(path string, err error) error {
	if path == "" {
		return fmt.Errorf("config.audit_anchor_key_unavailable: PODIUM_AUDIT_SIGNING_KEY_PATH is unset and its default ~/.podium/standalone/audit.key cannot be resolved (§8.6, §13.12): %w", err)
	}
	return fmt.Errorf("config.audit_anchor_key_unavailable: PODIUM_AUDIT_SIGNING_KEY_PATH resolves to %s, which cannot be read, parsed, or generated as the audit anchor key (§8.6, §13.12): %w", path, err)
}

// refuseSharedAnchorKey refuses an anchor key whose public half equals the
// registry signing key or any of its verify: keys. A verifier that trusts the
// registry key set would otherwise accept an anchor signature over a chain
// head as an artifact signature over a content hash of the same bytes. The
// message names the key by key_id and carries no key material.
// Spec: §8.6, §13.12.
func refuseSharedAnchorKey(anchor sign.RegistryManagedKey, anchorPath string, registryKey sign.RegistryManagedKey, registryPath string) error {
	role := sharedKeyRole(anchor.PublicKey, registryKey)
	if role == "" {
		return nil
	}
	return fmt.Errorf("config.audit_anchor_key_shared: the audit anchor key at %s (PODIUM_AUDIT_SIGNING_KEY_PATH) has key_id %s, which the registry signing key file at %s (PODIUM_SIGN_KEY_PATH) carries as its %s key; the anchor signature carries no purpose label, so generate a separate keypair for PODIUM_AUDIT_SIGNING_KEY_PATH (§8.6, §13.12)", anchorPath, sign.KeyIDFor(anchor.PublicKey), registryPath, role)
}

// sharedKeyRole names the role in which registryKey carries pub: "signing"
// for its public key, "verify:" for one of its verification-only keys, and ""
// when it carries pub in neither.
func sharedKeyRole(pub ed25519.PublicKey, registryKey sign.RegistryManagedKey) string {
	if pub.Equal(registryKey.PublicKey) {
		return "signing"
	}
	for _, k := range registryKey.Trusted {
		if pub.Equal(k) {
			return "verify:"
		}
	}
	return ""
}

// startAnchorScheduler starts the §8.6 local chain-head anchoring scheduler
// with the anchor key loadAnchorSigner loaded and checked. The scheduler
// runs in its own goroutine, never blocks startup, and records a failed
// periodic attempt as audit.anchor_failed.
func startAnchorScheduler(ctx context.Context, cfg *Config, stream *audit.Stream, signer sign.Provider) {
	sched := &audit.Scheduler{
		Sink:     stream,
		Signer:   signer,
		Interval: time.Duration(cfg.auditAnchorInterval) * time.Second,
		OnFailure: func(err error) {
			log.Printf("audit anchor failure: %v", err)
		},
	}
	go func() {
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("audit anchor scheduler stopped: %v", err)
		}
	}()
	log.Printf("audit anchor scheduler running (interval=%ds)", cfg.auditAnchorInterval)
}

// newReAnchor returns the §8.6 immediate re-anchor hook. A retention pass
// that drops events and an erasure that redacts them both rewrite the hash
// chain, which moves its head and leaves the last anchor naming a superseded
// head. The hook anchors the new head with the caller's context so verifiers
// do not wait for the next scheduler tick. Only the periodic scheduler
// records audit.anchor_failed; a failed re-anchor logs, and the next tick
// anchors the head.
//
// Spec: §8.6
func newReAnchor(stream *audit.Stream, signer sign.Provider) func(context.Context) {
	return func(ctx context.Context) {
		if _, err := audit.Anchor(ctx, stream, signer); err != nil {
			log.Printf("audit re-anchor after chain rewrite failed: %v", err)
		}
	}
}

// startVerifyScheduler bootstraps the §8.6 audit-integrity
// verification scheduler. It re-verifies the hash chain on a cadence
// and, on a detected gap, records an audit.gap_detected event and logs
// an alert ("Detection of gaps is automated and alerted"). The
// scheduler runs in its own goroutine and never blocks startup.
//
// A stream with no file destination, or a nil stream, disables
// verification, because there is no local chain to walk. The verification
// pass needs no signer, so it runs independently of whether anchoring is
// enabled.
func startVerifyScheduler(ctx context.Context, cfg *Config, stream *audit.Stream) {
	if stream.File() == nil {
		log.Printf("warning: audit verify disabled (no sink)")
		return
	}
	sched := &audit.VerifyScheduler{
		Sink:     stream,
		Interval: time.Duration(cfg.auditVerifyInterval) * time.Second,
		OnGap: func(err error) {
			log.Printf("audit integrity ALERT: hash-chain gap detected: %v", err)
		},
	}
	go func() {
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("audit verify scheduler stopped: %v", err)
		}
	}()
	log.Printf("audit verify scheduler running (interval=%ds)", cfg.auditVerifyInterval)
}

// loadOrGenerateAuditSigner reads the §8.6 anchor keypair from path,
// generating and writing one when the file is absent, and returns the key
// with the path it resolved (~/.podium/standalone/audit.key when path is
// empty). The returned path is empty only when the default cannot be
// resolved. The file uses the registry key-file format, and any verify: line
// is ignored because the anchor key is not rotated through a verification key
// set.
func loadOrGenerateAuditSigner(path string) (sign.RegistryManagedKey, string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return sign.RegistryManagedKey{}, "", err
		}
		path = filepath.Join(home, ".podium", "standalone", "audit.key")
	}
	kf, err := readOrCreateKeyFile(path)
	if err != nil {
		return sign.RegistryManagedKey{}, path, err
	}
	return sign.RegistryManagedKey{PrivateKey: kf.Private, PublicKey: kf.Public}, path, nil
}

// readOrCreateKeyFile reads the signing key file at path, or generates a
// keypair and writes it there when the file is absent. It generates only on
// fs.ErrNotExist, so an unreadable or malformed file is an error and is never
// overwritten. Both callers sign, so a file with no private: line is refused.
func readOrCreateKeyFile(path string) (sign.KeyFile, error) {
	kf, err := sign.ReadKeyFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return generateKeyFile(path)
	}
	if err != nil {
		return sign.KeyFile{}, err
	}
	if kf.Private == nil {
		return sign.KeyFile{}, fmt.Errorf("sign: key file %s carries no \"private:\" line", path)
	}
	return kf, nil
}

// generateKeyFile writes a fresh keypair to path and returns it.
func generateKeyFile(path string) (sign.KeyFile, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return sign.KeyFile{}, err
	}
	kf := sign.KeyFile{Private: priv, Public: pub}
	if err := sign.WriteKeyFile(path, kf); err != nil {
		return sign.KeyFile{}, err
	}
	return kf, nil
}

package serverboot

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// openAuditSink builds the registry's §8.3 audit sink from
// PODIUM_AUDIT_LOG_PATH. A filesystem path (or the empty default,
// ~/.podium/audit.log) yields a hash-chained file sink. An http(s) value
// yields an EndpointSink that forwards catalogue events to an external
// SIEM / log aggregator, mirroring the local sink's PODIUM_AUDIT_SINK
// redirect so "both the registry and local sinks can be redirected to
// external SIEM / log aggregation independently" (§8.3). spec: §8.3, §9.1.
//
// It returns the emit sink (the audit.Sink every event flows through) plus
// the *FileSink, which is non-nil only for the file case. The §8.6
// anchor/verify and §8.4 retention schedulers, and the §8.5 erasure pass,
// walk and rewrite the on-disk chain, so they run only against the file
// sink; with an endpoint, the receiving aggregator owns durability,
// integrity, and erasure of the shipped stream. Both returns are nil (with
// a logged warning) when the sink can't be constructed; callers treat a
// nil emit sink as "no audit sink available" and continue.
func openAuditSink(cfg *Config) (audit.Sink, *audit.FileSink) {
	if isAuditEndpoint(cfg.auditLogPath) {
		sink, err := audit.NewEndpointSink(cfg.auditLogPath)
		if err != nil {
			log.Printf("warning: audit sink disabled (endpoint): %v", err)
			return nil, nil
		}
		return sink, nil
	}
	logPath, err := resolveAuditPath(cfg.auditLogPath)
	if err != nil {
		log.Printf("warning: audit sink disabled (path): %v", err)
		return nil, nil
	}
	sink, err := audit.NewFileSink(logPath)
	if err != nil {
		log.Printf("warning: audit sink disabled (open): %v", err)
		return nil, nil
	}
	return sink, sink
}

// isAuditEndpoint reports whether a PODIUM_AUDIT_LOG_PATH value selects the
// external-endpoint sink rather than a local file path. The MCP local sink
// applies the same http(s) test to PODIUM_AUDIT_SINK. spec: §8.3.
func isAuditEndpoint(v string) bool {
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

// startAnchorScheduler bootstraps the §8.6 audit-anchoring
// scheduler. The signer is a §4.7.9 RegistryManagedKey backed by
// an Ed25519 keypair persisted at cfg.auditSigningKeyPath
// (defaults to ~/.podium/standalone/audit.key). The scheduler
// runs in its own goroutine and never blocks startup.
//
// It returns the signer so the caller can re-anchor on demand (e.g.
// immediately after a retention truncation); nil is returned
// when anchoring is disabled.
func startAnchorScheduler(cfg *Config, sink *audit.FileSink) sign.Provider {
	if sink == nil {
		log.Printf("warning: audit anchor disabled (no sink)")
		return nil
	}
	signer, err := loadOrGenerateAuditSigner(cfg.auditSigningKeyPath)
	if err != nil {
		log.Printf("warning: audit anchor disabled (signer): %v", err)
		return nil
	}
	sched := &audit.Scheduler{
		Sink:     sink,
		Signer:   signer,
		Interval: time.Duration(cfg.auditAnchorInterval) * time.Second,
		OnFailure: func(err error) {
			log.Printf("audit anchor failure: %v", err)
		},
	}
	go func() {
		if err := sched.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("audit anchor scheduler stopped: %v", err)
		}
	}()
	log.Printf("audit anchor scheduler running (interval=%ds)", cfg.auditAnchorInterval)
	return signer
}

// startVerifyScheduler bootstraps the §8.6 audit-integrity
// verification scheduler. It re-verifies the hash chain on a cadence
// and, on a detected gap, records an audit.gap_detected event and logs
// an alert ("Detection of gaps is automated and alerted"). The
// scheduler runs in its own goroutine and never blocks startup.
//
// Nil sink disables verification. The verification pass needs no signer,
// so it runs independently of whether anchoring is enabled.
func startVerifyScheduler(cfg *Config, sink *audit.FileSink) {
	if sink == nil {
		log.Printf("warning: audit verify disabled (no sink)")
		return
	}
	sched := &audit.VerifyScheduler{
		Sink:     sink,
		Interval: time.Duration(cfg.auditVerifyInterval) * time.Second,
		OnGap: func(err error) {
			log.Printf("audit integrity ALERT: hash-chain gap detected: %v", err)
		},
	}
	go func() {
		if err := sched.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("audit verify scheduler stopped: %v", err)
		}
	}()
	log.Printf("audit verify scheduler running (interval=%ds)", cfg.auditVerifyInterval)
}

// resolveAuditPath returns the audit log path with the home
// directory expanded. Empty defaults to ~/.podium/audit.log.
func resolveAuditPath(p string) (string, error) {
	if p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".podium", "audit.log"), nil
}

// loadOrGenerateAuditSigner reads the keypair from path; missing
// file is filled in by generating a new keypair and writing it
// back. The on-disk format is two base64 lines (private + public).
func loadOrGenerateAuditSigner(path string) (sign.Provider, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".podium", "standalone", "audit.key")
	}
	priv, pub, err := readOrCreateEd25519(path)
	if err != nil {
		return nil, err
	}
	keyID := keyIDFor(pub)
	return sign.RegistryManagedKey{
		PrivateKey: priv,
		PublicKey:  pub,
		KeyID:      keyID,
	}, nil
}

func readOrCreateEd25519(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return parseEd25519PEM(data)
	}
	if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	body := []byte(
		"private: " + base64.StdEncoding.EncodeToString(priv) + "\n" +
			"public: " + base64.StdEncoding.EncodeToString(pub) + "\n",
	)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", path, err)
	}
	return priv, pub, nil
}

// parseEd25519PEM parses the simple two-line format produced by
// readOrCreateEd25519.
func parseEd25519PEM(data []byte) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	var priv ed25519.PrivateKey
	var pub ed25519.PublicKey
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "private:"):
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "private:")))
			if err != nil {
				return nil, nil, fmt.Errorf("decode private: %w", err)
			}
			priv = ed25519.PrivateKey(b)
		case strings.HasPrefix(line, "public:"):
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "public:")))
			if err != nil {
				return nil, nil, fmt.Errorf("decode public: %w", err)
			}
			pub = ed25519.PublicKey(b)
		}
	}
	if len(priv) == 0 || len(pub) == 0 {
		return nil, nil, errors.New("audit signing key file: missing private or public block")
	}
	return priv, pub, nil
}

// keyIDFor returns a short fingerprint of the public key for the
// envelope's `key_id` field. Format: hex of sha256(pub)[:8].
func keyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

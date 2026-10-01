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
func startAnchorScheduler(ctx context.Context, cfg *Config, sink *audit.FileSink) sign.Provider {
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
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
func startVerifyScheduler(ctx context.Context, cfg *Config, sink *audit.FileSink) {
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
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
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

// loadOrGenerateAuditSigner reads the §8.6 anchor keypair from path,
// generating and writing one when the file is absent. The file uses the
// registry key-file format, and any verify: line is ignored because the anchor
// key is not rotated through a verification key set.
func loadOrGenerateAuditSigner(path string) (sign.Provider, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".podium", "standalone", "audit.key")
	}
	kf, err := readOrCreateKeyFile(path)
	if err != nil {
		return nil, err
	}
	return sign.RegistryManagedKey{PrivateKey: kf.Private, PublicKey: kf.Public}, nil
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

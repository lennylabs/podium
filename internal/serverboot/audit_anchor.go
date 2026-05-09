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

// startAnchorScheduler bootstraps the §8.6 audit-anchoring
// scheduler. The signer is a §4.7.9 RegistryManagedKey backed by
// an Ed25519 keypair persisted at cfg.auditSigningKeyPath
// (defaults to ~/.podium/standalone/audit.key). The scheduler
// runs in its own goroutine and never blocks startup.
func startAnchorScheduler(cfg *Config) {
	logPath, err := resolveAuditPath(cfg.auditLogPath)
	if err != nil {
		log.Printf("warning: audit anchor disabled (log path): %v", err)
		return
	}
	sink, err := audit.NewFileSink(logPath)
	if err != nil {
		log.Printf("warning: audit anchor disabled (sink): %v", err)
		return
	}
	signer, err := loadOrGenerateAuditSigner(cfg.auditSigningKeyPath)
	if err != nil {
		log.Printf("warning: audit anchor disabled (signer): %v", err)
		return
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

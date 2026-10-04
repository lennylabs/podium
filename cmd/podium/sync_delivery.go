package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lennylabs/podium/pkg/sync"
)

// newSyncDeliveryCheck builds the one memoized §4.7.10 delivery-check resolver
// a podium sync invocation passes to every pkg/sync call. startDir is where the
// defaults.verify_signatures workspace scope is discovered: the working
// directory, or the workspace of a --config file. Building it reads nothing;
// pkg/sync resolves it at the start of a server-source load, so this binary
// holds no condition on cache mode, target kind, or source kind. A stale-never
// warning is printed to stderr as WARN: <text>.
//
// Spec: §4.7.9, §7.5
func newSyncDeliveryCheck(startDir string) sync.DeliveryCheckFunc {
	home, _ := os.UserHomeDir()
	return sync.NewDeliveryCheckFunc(startDir, home, os.Getenv, func(text string) {
		fmt.Fprintf(os.Stderr, "WARN: %s\n", text)
	})
}

// workingDir returns the current working directory, empty when it cannot be
// read, in which case only the home sync.yaml scope is consulted.
func workingDir() string {
	wd, _ := os.Getwd()
	return wd
}

// reportDeliveryConfigError prints err as prefix + "error: <message>" and
// returns true when err is a delivery-check resolution failure. The message is
// the resolution failure itself, so it leads with
// config.signature_provider_unavailable or PODIUM_VERIFY_SIGNATURES must be.
func reportDeliveryConfigError(err error, prefix string) bool {
	var cfgErr *sync.DeliveryConfigError
	if !errors.As(err, &cfgErr) {
		return false
	}
	fmt.Fprintf(os.Stderr, "%serror: %v\n", prefix, cfgErr)
	return true
}

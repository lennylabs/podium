package sync

import (
	"errors"
	"fmt"
	"path/filepath"
	stdsync "sync"

	"github.com/lennylabs/podium/pkg/sign"
)

// VerifySignaturesSetting resolves defaults.verify_signatures across the
// §7.5.2 file scopes by per-key precedence: the workspace's
// .podium/sync.local.yaml, then its .podium/sync.yaml, then the home-global
// <home>/.podium/sync.yaml. The workspace is discovered by walking up from
// cwd; with none found, or with cwd empty, only the home file is read, and an
// empty home skips the home file. It returns the first non-empty value with
// the path of the file that carried it, or two empty strings when no scope
// sets it. A file that is absent, cannot be read, or does not parse
// contributes nothing, as in the podium-mcp resolution this ports, and err is
// therefore always nil today. LoadMergedConfig is not reused because it merges no verify_signatures and
// reports no per-key scope, which the stale-never warning and the
// invalid-policy error name.
//
// Spec: §4.7.9, §7.5.2
func VerifySignaturesSetting(cwd, home string) (value, path string, err error) {
	for _, p := range verifySignaturesScopes(cwd, home) {
		cfg, rerr := ReadConfigFile(p)
		if rerr != nil || cfg == nil || cfg.Defaults.VerifySignatures == "" {
			continue
		}
		return cfg.Defaults.VerifySignatures, p, nil
	}
	return "", "", nil
}

// verifySignaturesScopes lists the §7.5.2 files that can carry
// defaults.verify_signatures, highest precedence first.
func verifySignaturesScopes(cwd, home string) []string {
	var paths []string
	if cwd != "" {
		if ws, ok := DiscoverWorkspace(cwd); ok {
			paths = append(paths,
				filepath.Join(ws, ".podium", "sync.local.yaml"),
				filepath.Join(ws, ".podium", "sync.yaml"))
		}
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".podium", "sync.yaml"))
	}
	return paths
}

// ResolveVerifyPolicy resolves a consumer's §4.7.9 signature policy below its
// own explicit configuration. A non-empty envValue (the value of
// PODIUM_VERIFY_SIGNATURES, or a value a higher-precedence source already set)
// wins; an empty one counts as unset and falls back to
// defaults.verify_signatures across the §7.5.2 scopes, then to always. source
// is the path of the sync.yaml file that supplied the policy, empty otherwise.
// A never a file supplied returns warning text naming that file, because an
// earlier standalone bootstrap wrote that line into ~/.podium/sync.yaml and it
// disables verification on every registry the machine later reaches. The
// caller prints the warning; this function writes nothing.
//
// Spec: §4.7.9, §6.2, §7.5.2
func ResolveVerifyPolicy(envValue, cwd, home string) (policy sign.VerificationPolicy, source, warning string, err error) {
	if envValue != "" {
		policy = sign.VerificationPolicy(envValue)
		if !sign.ValidPolicy(policy) {
			return "", "", "", fmt.Errorf("PODIUM_VERIFY_SIGNATURES must be never | always, got %q", envValue)
		}
		return policy, "", "", nil
	}
	value, path, err := VerifySignaturesSetting(cwd, home)
	if err != nil {
		return "", "", "", err
	}
	if value == "" {
		return sign.PolicyAlways, "", "", nil
	}
	policy = sign.VerificationPolicy(value)
	if !sign.ValidPolicy(policy) {
		return "", "", "", fmt.Errorf("PODIUM_VERIFY_SIGNATURES must be never | always, got %q from defaults.verify_signatures in %s", value, path)
	}
	if policy == sign.PolicyNever {
		warning = fmt.Sprintf("signature verification is off because defaults.verify_signatures is never in %s; remove defaults.verify_signatures from that file to verify under the always default (§4.7.9)", path)
	}
	return policy, path, warning, nil
}

// ResolveDeliveryCheck resolves the §4.7.10 delivery check of server-source
// podium sync. The policy comes from PODIUM_VERIFY_SIGNATURES, then
// defaults.verify_signatures discovered from startDir and home, then always;
// the verifier comes from sign.ResolveVerifier over PODIUM_SIGNATURE_VERIFY_KEY
// and PODIUM_SIGN_KEY_PATH. getenv is read for those three variables alone:
// sync reads no PODIUM_SIGNATURE_PROVIDER, because a delivery signature is
// always registry-managed and the variable is also the podium sign and podium
// verify provider default. The returned string is the stale-never warning,
// empty when there is none.
//
// Spec: §4.7.9, §7.5
func ResolveDeliveryCheck(startDir, home string, getenv func(string) string) (*sign.DeliveryCheck, string, error) {
	policy, _, warning, err := ResolveVerifyPolicy(getenv("PODIUM_VERIFY_SIGNATURES"), startDir, home)
	if err != nil {
		return nil, "", err
	}
	verifier, err := sign.ResolveVerifier(policy, getenv("PODIUM_SIGNATURE_VERIFY_KEY"), getenv("PODIUM_SIGN_KEY_PATH"))
	if err != nil {
		return nil, warning, err
	}
	return &sign.DeliveryCheck{Policy: policy, Verifier: verifier}, warning, nil
}

// DeliveryCheckFunc returns the delivery check a server-source sync applies.
// The sync entry points receive it through their options and call it at the
// one resolution point of §7.5.
type DeliveryCheckFunc func() (*sign.DeliveryCheck, error)

// DeliveryConfigError reports that the delivery check could not be resolved:
// an invalid policy value or missing verification material. Its message is the
// underlying message unchanged, so it still leads with
// config.signature_provider_unavailable or PODIUM_VERIFY_SIGNATURES must be.
// The CLI maps it to exit status 2.
type DeliveryConfigError struct {
	// Err is the resolution failure.
	Err error
}

// Error returns the underlying message.
func (e *DeliveryConfigError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *DeliveryConfigError) Unwrap() error { return e.Err }

// NewDeliveryCheckFunc returns a DeliveryCheckFunc that resolves the check
// through ResolveDeliveryCheck on its first call and returns that same result
// on every later call, so one command invocation resolves once however many
// targets, watch cycles, or override steps it runs. Building the function
// reads nothing. A non-empty stale-never warning is passed to warn once, and a
// resolution failure is returned as a *DeliveryConfigError.
//
// Spec: §4.7.9, §7.5
func NewDeliveryCheckFunc(startDir, home string, getenv func(string) string, warn func(string)) DeliveryCheckFunc {
	var (
		once  stdsync.Once
		check *sign.DeliveryCheck
		err   error
	)
	return func() (*sign.DeliveryCheck, error) {
		once.Do(func() {
			c, warning, rerr := ResolveDeliveryCheck(startDir, home, getenv)
			if warning != "" && warn != nil {
				warn(warning)
			}
			if rerr != nil {
				err = &DeliveryConfigError{Err: rerr}
				return
			}
			check = c
		})
		return check, err
	}
}

// errNoDeliveryCheck is the fail-closed refusal of a server-source load whose
// caller supplied no DeliveryCheckFunc.
var errNoDeliveryCheck = errors.New("sync: server-source load has no delivery check configured")

// deliveryCheck resolves f, refusing a nil f so a caller that omits the
// hand-off cannot load an unverified record. f's error is returned unchanged.
// A nil check with no error is refused like a nil f, because no record can be
// verified against it.
//
// Spec: §4.7.10, §7.5
func deliveryCheck(f DeliveryCheckFunc) (*sign.DeliveryCheck, error) {
	if f == nil {
		return nil, errNoDeliveryCheck
	}
	check, err := f()
	if err != nil {
		return nil, err
	}
	if check == nil {
		return nil, errNoDeliveryCheck
	}
	return check, nil
}

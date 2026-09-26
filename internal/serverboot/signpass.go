package serverboot

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lennylabs/podium/pkg/sign"
)

// ErrSignStoredRowsUsage marks a sign-stored-rows invocation whose arguments
// do not parse: an unknown flag or a positional argument. Only argument
// parsing returns it, so a dispatcher maps it to exit status 2 and every other
// error to exit status 1.
var ErrSignStoredRowsUsage = errors.New("sign-stored-rows: usage")

// SignStoredRowsUsage is the usage text a dispatcher prints on a usage error.
const SignStoredRowsUsage = `usage: sign-stored-rows [--include-unsigned] [--dry-run]

Re-sign stored rows under the registry signing key (§13.4). It reads the
configuration a registry start reads and binds no listen address.

  --include-unsigned  also sign every stored row that carries no signature
  --dry-run           report every write and make none
`

// signStoredRowsOptions are the parsed command-line flags.
type signStoredRowsOptions struct {
	includeUnsigned bool
	dryRun          bool
}

// RunSignStoredRows is the sign-stored-rows command that podium-server and
// podium admin both expose. It applies the §13.4 rewrite's classification and
// writes on operator demand: with the completion record absent it performs the
// rewrite in place of the first start, and with the record present it re-signs
// every row a verification-only key verifies. It writes the summary lines to
// stdout, binds no listener, and never generates a key.
//
// Spec: §13.4, §4.7.9, §13.12.
func RunSignStoredRows(ctx context.Context, args []string) error {
	return runSignStoredRows(ctx, args, os.Stdout)
}

// SignStoredRowsExitCode writes err to stderr and returns the command's exit
// status: 0 on success, 2 with the usage text on a usage error, and 1 on any
// other error. Both dispatchers call it, so the binaries cannot map the same
// failure to different statuses.
func SignStoredRowsExitCode(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, err)
	if errors.Is(err, ErrSignStoredRowsUsage) {
		_, _ = fmt.Fprint(stderr, SignStoredRowsUsage)
		return 2
	}
	return 1
}

// runSignStoredRows is RunSignStoredRows with the summary writer injected.
func runSignStoredRows(ctx context.Context, args []string, stdout io.Writer) error {
	opts, err := parseSignStoredRowsArgs(args)
	if err != nil {
		return err
	}
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	d, closeStore, err := openSignPassDeps(cfg, opts)
	if err != nil {
		return err
	}
	defer closeStore()
	d.Summary = stdout
	if opts.dryRun {
		return dryRunSignStoredRows(ctx, d, stdout)
	}
	return signStoredRows(ctx, d)
}

// parseSignStoredRowsArgs parses the flags and refuses a positional argument.
// The flag package's own message is discarded because the dispatcher prints
// the usage text once, from the returned error.
func parseSignStoredRowsArgs(args []string) (signStoredRowsOptions, error) {
	var opts signStoredRowsOptions
	fs := flag.NewFlagSet("sign-stored-rows", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.includeUnsigned, "include-unsigned", false, "also sign every stored row that carries no signature")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "report every write and make none")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("%w: %w", ErrSignStoredRowsUsage, err)
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("%w: unexpected argument %q", ErrSignStoredRowsUsage, fs.Arg(0))
	}
	return opts, nil
}

// openSignPassDeps refuses a command that could not sign and otherwise opens
// the collaborators a registry start opens for the rewrite. The refusals run
// before the store opens, so a refused command writes nothing, and the loader
// runs with generate unset, so an absent key file is refused rather than
// generated. The returned func releases the store.
//
// Spec: §13.4, §13.12.
func openSignPassDeps(cfg *Config, opts signStoredRowsOptions) (rehashDeps, func(), error) {
	if !registrySigningEnabled(cfg.signMode) {
		return rehashDeps{}, nil, fmt.Errorf("config.signature_provider_unavailable: sign-stored-rows: signing is off (PODIUM_SIGN=none): %w", sign.ErrRegistryManagedUnavailable)
	}
	if err := refuseUnpersistedSigningKey(cfg); err != nil {
		return rehashDeps{}, nil, err
	}
	keyEnv := os.Getenv("PODIUM_SIGN_KEY_PATH")
	key, err := loadRegistrySigner(keyEnv, false)
	if err != nil {
		return rehashDeps{}, nil, err
	}
	mintUnsigned, err := mintUnsignedOnFirstRun(cfg, keyEnv)
	if err != nil {
		return rehashDeps{}, nil, err
	}
	scrubber, err := cfg.piiRedaction.BuildScrubber()
	if err != nil {
		return rehashDeps{}, nil, fmt.Errorf("pii redaction config: %w", err)
	}
	st, err := openStore(cfg)
	if err != nil {
		return rehashDeps{}, nil, fmt.Errorf("open store: %w", err)
	}
	closeStore := func() {
		if closer, ok := st.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	sink, _ := openAuditSink(cfg)
	return rehashDeps{
		Store:   st,
		Objects: openObjectStoreOrNil(cfg),
		// The deadline the boot applies: a zero value would expire every
		// object-held read at once and hold the completion record back.
		ReadTimeout:    cfg.migrationObjectReadTimeout,
		Signer:         key,
		MintUnsigned:   mintUnsigned,
		AttestUnsigned: opts.includeUnsigned,
		Sink:           sink,
		Scrubber:       scrubber,
	}, closeStore, nil
}

// signStoredRows runs the rewrite over every stored row whether or not the
// completion record is present, and fails when a row held the record back or
// a write failed, so a success status is what licenses removing a
// verification-only key. Every write is a compare-and-swap on the stored hash
// and signature, so a record-present run may overlap serving registries.
//
// Spec: §13.4, §4.7.9.
func signStoredRows(ctx context.Context, d rehashDeps) error {
	counts, held, err := rehashStoredHashes(ctx, d, false)
	if err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	if held || counts.errors > 0 {
		return fmt.Errorf("sign-stored-rows: %d row(s) failed to sign or write, and %d body_unavailable row(s) could not be read; the log names each row, and a later run retries them", counts.errors, counts.bodyUnavailable)
	}
	return nil
}

// dryRunSignStoredRows plans the run under the effective unsigned-row policy
// and prints every planned row with its class, the hash a write would store,
// whether the write would sign, and the row's current signature state. It then
// prints the unsigned-left and per-verify-key totals a run would report. The
// signature state is what lets the operator list the unsigned rows that
// --include-unsigned attests before attesting them. It writes nothing, the
// completion record included. Spec: §13.4, §4.7.9.
func dryRunSignStoredRows(ctx context.Context, d rehashDeps, out io.Writer) error {
	d, _, err := rehashPolicy(ctx, d)
	if err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	plan, err := planRehash(ctx, d)
	if err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	counts := rehashCounts{stillSigned: map[string]int{}}
	var writes, signs int
	for _, row := range plan {
		write := row.class == classUnmigrated
		if write {
			writes++
		}
		if write && row.sign {
			signs++
		}
		counts.countSigningState(row, write)
		target := row.newHash
		if target == "" {
			target = "-"
		}
		_, _ = fmt.Fprintf(out, "dry-run: %s class=%s target=%s write=%t sign=%t signed_by=%s\n", row.key(), row.class, target, write, write && row.sign, signatureState(row))
	}
	_, _ = fmt.Fprintf(out, "dry-run: %d row(s) planned, %d would be written, %d would be signed\n", len(plan), writes, signs)
	d.Summary = out
	logSigningSummary(d, counts, "dry-run")
	return nil
}

// signatureState names a planned row's stored envelope for the dry-run line:
// the key_id of the trusted key that verifies it, "unsigned" for a row with no
// envelope, or "unverified" for an envelope no trusted key verifies.
func signatureState(row rehashRow) string {
	switch {
	case row.rec.Signature == "":
		return "unsigned"
	case row.signedBy == "":
		return "unverified"
	}
	return row.signedBy
}

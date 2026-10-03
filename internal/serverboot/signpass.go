package serverboot

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/lennylabs/podium/pkg/sign"
)

// ErrSignStoredRowsUsage marks a sign-stored-rows invocation whose arguments
// do not parse or break a flag rule: an unknown flag, a positional argument,
// --plan-digest with --dry-run, a malformed digest, or --include-unsigned
// without --dry-run or --plan-digest. Only argument parsing returns it. A
// dispatcher maps it to exit status 2, ErrSignStoredRowsPlanChanged to exit
// status 3, and every other error to exit status 1.
var ErrSignStoredRowsUsage = errors.New("sign-stored-rows: usage")

// ErrSignStoredRowsPlanChanged marks a run whose own plan digest differs from
// the digest given with --plan-digest. The §13.4 plan-digest comparison
// returns it before any write, and dispatchers map it to exit status 3. Its
// text carries no command prefix, because signStoredRows wraps every error
// with one.
var ErrSignStoredRowsPlanChanged = errors.New("plan changed since the reviewed dry run")

// SignStoredRowsUsage is the usage text a dispatcher prints on a usage error.
const SignStoredRowsUsage = `usage: sign-stored-rows [--include-unsigned] [--dry-run | --plan-digest=<digest>]

Rewrite stored rows under the §13.4 rules, signing under the registry signing key when signing is on.
It reads the configuration a registry start reads and binds no listen address.

  --include-unsigned  also sign every stored row that carries no signature; refused with signing off
  --dry-run           report every write and make none, and print the plan digest
  --plan-digest       fail with exit status 3, writing nothing, unless this run's plan has the digest
                      a reviewed --dry-run printed; required with --include-unsigned
`

// signStoredRowsOptions are the parsed command-line flags.
type signStoredRowsOptions struct {
	includeUnsigned bool
	dryRun          bool
	// planDigest is the reviewed dry run's plan digest, or empty.
	planDigest string
}

// RunSignStoredRows is the sign-stored-rows command that podium-server and
// podium admin both expose. It applies the §13.4 rewrite's classification and
// writes on operator demand: with the completion record absent it performs the
// rewrite in place of the first start, and with the record present it re-signs
// every row a verification-only key verifies. Given --plan-digest, it refuses
// before any write when its own plan differs from the reviewed dry run's. With
// signing off it performs the rewrite without signing, which is the only
// rewrite path for a signing-off store outside the co-located SQLite store once
// that store holds a manifest row. It writes the summary lines to stdout, binds
// no listener, and never generates a key.
//
// Spec: §13.4, §4.7.9, §13.12.
func RunSignStoredRows(ctx context.Context, args []string) error {
	return runSignStoredRows(ctx, args, os.Stdout)
}

// SignStoredRowsExitCode writes err to stderr and returns the command's exit
// status: 0 on success, 0 with the usage text on a help request, 2 with the
// usage text on a usage error, 3 on a plan that changed since the reviewed dry
// run, and 1 on any other error. Status 3 lets a scripted caller tell a changed
// plan from a failed write. Both dispatchers call it, so the binaries cannot
// map the same failure to different statuses. A help request exits 0 as every
// other podium subcommand's does.
func SignStoredRowsExitCode(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprint(stderr, SignStoredRowsUsage)
		return 0
	}
	_, _ = fmt.Fprintln(stderr, err)
	if errors.Is(err, ErrSignStoredRowsUsage) {
		_, _ = fmt.Fprint(stderr, SignStoredRowsUsage)
		return 2
	}
	if errors.Is(err, ErrSignStoredRowsPlanChanged) {
		return 3
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
// the usage text once, from the returned error. A help request returns
// flag.ErrHelp unwrapped, because it is not a usage error and must not carry
// ErrSignStoredRowsUsage. It also refuses --plan-digest with --dry-run, a
// digest that is not sha256: followed by 64 lowercase hex digits, and
// --include-unsigned without --dry-run or --plan-digest, so each refusal runs
// before the configuration is read and the store opens. Spec: §13.4.
func parseSignStoredRowsArgs(args []string) (signStoredRowsOptions, error) {
	var opts signStoredRowsOptions
	fs := flag.NewFlagSet("sign-stored-rows", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.includeUnsigned, "include-unsigned", false, "also sign every stored row that carries no signature")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "report every write and make none")
	fs.StringVar(&opts.planDigest, "plan-digest", "", "the plan digest a reviewed --dry-run printed")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, err
		}
		return opts, fmt.Errorf("%w: %w", ErrSignStoredRowsUsage, err)
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("%w: unexpected argument %q", ErrSignStoredRowsUsage, fs.Arg(0))
	}
	return opts, checkSignStoredRowsFlags(opts)
}

// checkSignStoredRowsFlags applies the §13.4 flag rules to parsed options.
// --include-unsigned outside --dry-run needs a digest, so the attestation
// covers the rows the reviewed dry run listed. Spec: §13.4.
func checkSignStoredRowsFlags(opts signStoredRowsOptions) error {
	switch {
	case opts.planDigest != "" && opts.dryRun:
		return fmt.Errorf("%w: --plan-digest cannot be combined with --dry-run", ErrSignStoredRowsUsage)
	case opts.planDigest != "" && !planDigestPattern.MatchString(opts.planDigest):
		return fmt.Errorf("%w: --plan-digest must be sha256: followed by 64 lowercase hex digits", ErrSignStoredRowsUsage)
	case opts.includeUnsigned && !opts.dryRun && opts.planDigest == "":
		return fmt.Errorf("%w: --include-unsigned requires --plan-digest from a reviewed --dry-run", ErrSignStoredRowsUsage)
	}
	return nil
}

// openSignPassDeps refuses a command that could not run as invoked and
// otherwise opens the collaborators a registry start opens for the rewrite.
// With signing off the command runs with no signer, and --include-unsigned,
// which attests unsigned rows by signing them, is the only invocation refused
// for it. The refusals run before the store opens, so a refused command writes
// nothing, and the loader runs with generate unset, so an absent key file is
// refused rather than generated. The returned func releases the store.
//
// Spec: §13.4, §13.12.
func openSignPassDeps(cfg *Config, opts signStoredRowsOptions) (rehashDeps, func(), error) {
	signingOn := registrySigningEnabled(cfg.signMode)
	if !signingOn && opts.includeUnsigned {
		return rehashDeps{}, nil, fmt.Errorf("config.signature_provider_unavailable: sign-stored-rows: --include-unsigned attests unsigned rows by signing them, and signing is off (PODIUM_SIGN=none): %w", sign.ErrRegistryManagedUnavailable)
	}
	var key sign.RegistryManagedKey
	var mintUnsigned bool
	if signingOn {
		var err error
		if key, mintUnsigned, err = loadSignPassSigner(cfg); err != nil {
			return rehashDeps{}, nil, err
		}
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
	// sign-stored-rows never anchors, so an unopenable file sink is logged
	// and the pass runs without one.
	sink, _, sinkErr := openAuditSink(cfg)
	if sinkErr != nil {
		log.Printf("warning: audit sink disabled: %v", sinkErr)
	}
	d := rehashDeps{
		Store:   st,
		Objects: openObjectStoreOrNil(cfg),
		// The deadline the boot applies: a zero value would expire every
		// object-held read at once and hold the completion record back.
		ReadTimeout:    cfg.migrationObjectReadTimeout,
		AttestUnsigned: opts.includeUnsigned,
		ReviewedPlan:   opts.planDigest,
		Sink:           sink,
		Scrubber:       scrubber,
	}
	// Assign the signer only when signing is on: a zero RegistryManagedKey
	// stored in the interface field is never nil, and the rewrite would then
	// send unsigned rows to Sign.
	if signingOn {
		d.Signer = key
		d.MintUnsigned = mintUnsigned
	}
	return d, closeStore, nil
}

// loadSignPassSigner loads the registry signing key without generating one and
// resolves the first-run unsigned-row policy for the configured store.
func loadSignPassSigner(cfg *Config) (sign.RegistryManagedKey, bool, error) {
	if err := refuseUnpersistedSigningKey(cfg); err != nil {
		return sign.RegistryManagedKey{}, false, err
	}
	keyEnv := os.Getenv("PODIUM_SIGN_KEY_PATH")
	key, err := loadRegistrySigner(keyEnv, false)
	if err != nil {
		return sign.RegistryManagedKey{}, false, err
	}
	mintUnsigned, err := mintUnsignedOnFirstRun(cfg, keyEnv)
	if err != nil {
		return sign.RegistryManagedKey{}, false, err
	}
	return key, mintUnsigned, nil
}

// signStoredRows runs the rewrite over every stored row whether or not the
// completion record is present, and fails when a row held the record back, a
// write failed, or the completion record could not be written, so a success
// status is what licenses removing a verification-only key and what tells the
// operator that the rewrite standing in for the first start is recorded. Every write is a compare-and-swap on the stored hash
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
	if counts.recordErr != nil {
		return fmt.Errorf("sign-stored-rows: %w; a later run records it", counts.recordErr)
	}
	return nil
}

// dryRunSignStoredRows plans the run under the effective unsigned-row policy
// and applies the stranded-signature refusal the run applies, so it never
// prints a digest for a plan the run refuses. It prints every planned row in
// canonical order with its class, the hash a write would store, whether the
// write would sign, the row's current signature state, and its stored content
// hash, then the plan header, the unsigned-left and per-verify-key totals a
// run would report, and last the plan digest that --plan-digest binds a run
// to. The signature state is what lets the operator list the unsigned rows
// that --include-unsigned attests before attesting them. It writes nothing,
// the completion record included. Spec: §13.4, §4.7.9.
func dryRunSignStoredRows(ctx context.Context, d rehashDeps, out io.Writer) error {
	d, applied, err := rehashPolicy(ctx, d)
	if err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	plan, err := planRehash(ctx, d)
	if err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	if err := refuseStrandedSignature(plan, d.Signer); err != nil {
		return fmt.Errorf("sign-stored-rows: %w", err)
	}
	h := newPlanHeader(applied, d)
	rows := canonicalPlan(plan)
	counts := rehashCounts{stillSigned: map[string]int{}}
	var writes, signs int
	for _, row := range rows {
		write := row.class == classUnmigrated
		if write {
			writes++
		}
		if row.writesSigned() {
			signs++
		}
		counts.countSigningState(row, write)
	}
	writePlanRows(out, "dry-run", h, rows)
	writePlanHeader(out, "dry-run", h)
	_, _ = fmt.Fprintf(out, "dry-run: %d row(s) planned, %d would be written, %d would be signed\n", len(rows), writes, signs)
	d.Summary = out
	logSigningSummary(d, counts, "dry-run")
	digest, covered := planDigest(h, rows)
	_, _ = fmt.Fprintf(out, "dry-run: plan digest %s over %d row(s)\n", digest, covered)
	return nil
}

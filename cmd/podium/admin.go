package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
)

// adminCmd is the entry point for the admin family of subcommands.
// Phase 16 ships:
//
//	podium admin erase <user-id> [--audit-path PATH] [--salt SALT]
//	podium admin retention [--audit-path PATH] --policy TYPE=DURATION...
//
// All operations run against the local FileSink at ~/.podium/audit.log
// unless --audit-path is supplied; the registry-wide retention path
// runs server-side as part of standalone bootstrap.
func adminCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium admin <subcommand>")
		return 2
	}
	switch args[0] {
	case "erase":
		return adminEraseCmd(args[1:])
	case "retention":
		return adminRetentionCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", args[0])
		return 2
	}
}

func adminEraseCmd(args []string) int {
	fs := flag.NewFlagSet("admin erase", flag.ContinueOnError)
	auditPath := fs.String("audit-path", "", "audit log path (default ~/.podium/audit.log)")
	salt := fs.String("salt", "", "salt for the GDPR erasure tombstone (per tenant)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin erase <user-id>")
		return 2
	}
	userID := fs.Arg(0)
	sink, err := audit.NewFileSink(*auditPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open audit log: %v\n", err)
		return 1
	}
	transformed, err := audit.EraseUser(context.Background(), sink, userID, *salt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "erase failed: %v\n", err)
		return 1
	}
	fmt.Printf("erased %s in %d audit events; tombstone written\n", userID, transformed)
	return 0
}

func adminRetentionCmd(args []string) int {
	fs := flag.NewFlagSet("admin retention", flag.ContinueOnError)
	auditPath := fs.String("audit-path", "", "audit log path (default ~/.podium/audit.log)")
	policyFlag := stringSliceFlag{}
	fs.Var(&policyFlag, "policy", "TYPE=DURATION (repeatable, e.g. artifacts.searched=720h)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(policyFlag) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one --policy is required")
		return 2
	}
	policies, err := parsePolicies(policyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse --policy: %v\n", err)
		return 2
	}
	sink, err := audit.NewFileSink(*auditPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open audit log: %v\n", err)
		return 1
	}
	dropped, err := audit.Enforce(context.Background(), sink, time.Now().UTC(), policies)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enforce failed: %v\n", err)
		return 1
	}
	fmt.Printf("retention enforced: %d audit events dropped\n", dropped)
	return 0
}

func parsePolicies(flags []string) ([]audit.Policy, error) {
	out := make([]audit.Policy, 0, len(flags))
	for _, raw := range flags {
		// Accept either "TYPE=DURATION" or "TYPE=DAYS:N" / "TYPE=HOURS:N"
		// for ergonomics; default is Go's time.ParseDuration.
		i := strings.Index(raw, "=")
		if i < 0 {
			return nil, fmt.Errorf("expected TYPE=DURATION, got %q", raw)
		}
		t := raw[:i]
		val := raw[i+1:]
		dur, err := time.ParseDuration(val)
		if err != nil {
			// Fall back to "N days" form: "30d" → 30*24h.
			if strings.HasSuffix(val, "d") {
				n, perr := strconv.Atoi(strings.TrimSuffix(val, "d"))
				if perr == nil {
					dur = time.Duration(n) * 24 * time.Hour
					err = nil
				}
			}
			if err != nil {
				return nil, fmt.Errorf("policy %q: %w", raw, err)
			}
		}
		out = append(out, audit.Policy{Type: audit.EventType(t), MaxAge: dur})
	}
	return out, nil
}

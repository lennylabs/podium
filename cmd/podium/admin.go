package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
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
	if len(args) == 0 || isHelpArg(args[0]) {
		printGroupHelp("admin", "Administer the registry: grants, audit, runtime keys, migration.", [][2]string{
			{"grant", "Grant tenant admin role to a user."},
			{"revoke", "Revoke tenant admin role from a user."},
			{"show-effective", "Print the per-layer visibility for a user identity."},
			{"erase", "GDPR right-to-erasure: purge a user's layers and redact their audit identity."},
			{"retention", "Apply audit retention policies to the local audit log."},
			{"reembed", "Re-run vector embeddings against the configured registry."},
			{"runtime", "Manage trusted runtime signing keys."},
			{"migrate-to-standard", "Pump standalone state into a standard deployment."},
		})
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "erase":
		return adminEraseCmd(args[1:])
	case "retention":
		return adminRetentionCmd(args[1:])
	case "reembed":
		return adminReembedCmd(args[1:])
	case "grant":
		return adminGrantCmd(args[1:])
	case "revoke":
		return adminRevokeCmd(args[1:])
	case "show-effective":
		return adminShowEffectiveCmd(args[1:])
	case "runtime":
		return adminRuntimeCmd(args[1:])
	case "migrate-to-standard":
		return adminMigrateToStandard(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", args[0])
		return 2
	}
}

// adminGrantCmd adds an admin grant for the named user.
//
//	podium admin grant <user-id> [--registry URL]
func adminGrantCmd(args []string) int {
	fs := flag.NewFlagSet("admin grant", flag.ContinueOnError)
	setUsage(fs, "Grant tenant admin role to a user.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin grant <user-id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	body := map[string]any{"user_id": fs.Arg(0)}
	out, status := doJSON(*registry+"/v1/admin/grants", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "grant failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// adminRevokeCmd removes an admin grant for the named user.
//
//	podium admin revoke <user-id> [--registry URL]
func adminRevokeCmd(args []string) int {
	fs := flag.NewFlagSet("admin revoke", flag.ContinueOnError)
	setUsage(fs, "Revoke tenant admin role from a user.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin revoke <user-id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(
		*registry+"/v1/admin/grants?user_id="+url.QueryEscape(fs.Arg(0)),
		"DELETE", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "revoke failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Fprintln(os.Stderr, "revoked")
	return 0
}

// adminShowEffectiveCmd queries the per-layer visibility for one
// user identity.
//
//	podium admin show-effective <user-id> [--group g1] [--group g2] [--registry URL]
func adminShowEffectiveCmd(args []string) int {
	fs := flag.NewFlagSet("admin show-effective", flag.ContinueOnError)
	setUsage(fs, "Print the per-layer visibility for a user identity.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	groups := stringSliceFlag{}
	fs.Var(&groups, "group", "OIDC group claim (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin show-effective <user-id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	q := url.Values{}
	q.Set("user_id", fs.Arg(0))
	for _, g := range groups {
		q.Add("group", g)
	}
	out, status := doJSON(*registry+"/v1/admin/show-effective?"+q.Encode(), "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "show-effective failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func adminReembedCmd(args []string) int {
	fs := flag.NewFlagSet("admin reembed", flag.ContinueOnError)
	setUsage(fs, "Re-run vector embeddings against the configured registry.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	artifact := fs.String("artifact", "", "specific artifact ID (optional)")
	version := fs.String("version", "", "specific version (required with --artifact)")
	onlyMissing := fs.Bool("only-missing", false, "skip artifacts that already have a vector")
	since := fs.String("since", "", "re-embed only artifacts ingested at or after this RFC3339 timestamp")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			fmt.Fprintf(os.Stderr, "error: --since must be an RFC3339 timestamp: %v\n", err)
			return 2
		}
	}
	q := url.Values{}
	if *artifact != "" {
		if *version == "" {
			fmt.Fprintln(os.Stderr, "error: --version is required with --artifact")
			return 2
		}
		q.Set("artifact", *artifact)
		q.Set("version", *version)
	} else {
		// spec: §4.7 — `--all` is the no-flag default; `--since` and
		// `--only-missing` scope a tenant-wide pass and compose.
		if *onlyMissing {
			q.Set("only_missing", "true")
		}
		if *since != "" {
			q.Set("since", *since)
		}
	}
	endpoint := *registry + "/v1/admin/reembed"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	out, status := doJSON(endpoint, "POST", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "reembed failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// adminEraseCmd runs the §8.5 GDPR right-to-erasure. By default it calls the
// registry-side endpoint, which unregisters and purges the user's owned
// layers and redacts the registry audit stream (the authenticated session
// identifies the invoking admin). The --local / --audit-path form instead
// redacts the MCP local audit sink directly; that path records the invoking
// admin from --operator.
func adminEraseCmd(args []string) int {
	fs := flag.NewFlagSet("admin erase", flag.ContinueOnError)
	setUsage(fs, "GDPR right-to-erasure: purge a user's layers and redact their audit identity.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	auditPath := fs.String("audit-path", "", "local MCP audit log path (default ~/.podium/audit.log); selects the local-log form")
	local := fs.Bool("local", false, "redact the local MCP audit log instead of the registry")
	salt := fs.String("salt", "", "salt for the GDPR erasure tombstone (per tenant, required)")
	operator := fs.String("operator", "", "invoking admin identity recorded on user.erased (required for the local-log form)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium admin erase <user-id>")
		return 2
	}
	userID := fs.Arg(0)
	// spec §8.5: an empty salt yields a guessable tombstone.
	if *salt == "" {
		fmt.Fprintln(os.Stderr, "error: --salt is required (an empty salt yields a guessable tombstone)")
		return 2
	}
	// Local MCP-sink form: redact the local audit log directly.
	if *local || *auditPath != "" {
		// spec §8.5: record the invoking admin for accountability.
		if *operator == "" {
			fmt.Fprintln(os.Stderr, "error: --operator is required for the local-log erase")
			return 2
		}
		sink, err := audit.NewFileSink(*auditPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open audit log: %v\n", err)
			return 1
		}
		transformed, err := audit.EraseUser(context.Background(), sink, userID, *salt, *operator)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erase failed: %v\n", err)
			return 1
		}
		fmt.Printf("erased %s in %d audit events; tombstone written\n", userID, transformed)
		return 0
	}
	// Registry-side form: the endpoint purges owned layers and redacts the
	// registry audit stream; the authenticated session names the admin.
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	body := map[string]any{"user_id": userID, "salt": *salt}
	out, status := doJSON(*registry+"/v1/admin/erase", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "erase failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func adminRetentionCmd(args []string) int {
	fs := flag.NewFlagSet("admin retention", flag.ContinueOnError)
	setUsage(fs, "Apply audit retention policies to the local audit log.")
	auditPath := fs.String("audit-path", "", "audit log path (default ~/.podium/audit.log)")
	policyFlag := stringSliceFlag{}
	fs.Var(&policyFlag, "policy", "TYPE=DURATION (repeatable, e.g. artifacts.searched=720h)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	dropped, err := audit.Enforce(context.Background(), sink, time.Now().UTC(), policies, audit.DefaultQueryRetention())
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

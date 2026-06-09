package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// adminTenantCmd dispatches `podium admin tenant <create|list|update|deactivate>`
// for §7.3.3 runtime tenant provisioning. The caller authenticates as any
// client does; the registry checks the operator grant (§4.7.1 Operator role).
func adminTenantCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("admin tenant", "Manage tenants (operator role).", [][2]string{
			{"create", "Create a tenant."},
			{"list", "List tenants."},
			{"update", "Update a tenant's quota, scope-preview gate, or active state."},
			{"deactivate", "Deactivate a tenant (soft)."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "create":
		return adminTenantCreate(args[1:])
	case "list":
		return adminTenantList(args[1:])
	case "update":
		return adminTenantUpdate(args[1:])
	case "deactivate":
		return adminTenantDeactivate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown tenant subcommand: %s\n", args[0])
		return 2
	}
}

// leadingPositional splits off a leading non-flag argument (a positional
// <name> or <id>) so flags may follow it, returning the positional and the
// remaining args to parse as flags. It returns "" when the first arg is a flag.
func leadingPositional(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// setFlags returns the set of flag names the caller passed, so an update sends
// only the supplied fields (the §7.3.3 partial PATCH).
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// parseBoolFlag parses a true|false flag value.
func parseBoolFlag(name, s string) (bool, int) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, 0
	case "false":
		return false, 0
	default:
		fmt.Fprintf(os.Stderr, "error: --%s must be true or false, got %q\n", name, s)
		return false, 2
	}
}

// tenantQuotaFlags registers the quota flags shared by create and update and
// returns a function that builds the quota body from only the flags that were
// set, so an omitted quota field is left out of the request.
func tenantQuotaFlags(fs *flag.FlagSet) func(set map[string]bool) map[string]any {
	storageBytes := fs.Int64("storage-bytes", 0, "storage quota in bytes (0 disables the limit)")
	searchQPS := fs.Int("search-qps", 0, "search QPS quota (0 disables the limit)")
	materializeRate := fs.Int("materialize-rate", 0, "materialize rate quota (0 disables the limit)")
	auditVolume := fs.Int64("audit-volume-per-day", 0, "audit volume per day quota (0 disables the limit)")
	maxUserLayers := fs.Int("max-user-layers", 0, "per-identity user-defined-layer cap (0 selects the default)")
	return func(set map[string]bool) map[string]any {
		quota := map[string]any{}
		if set["storage-bytes"] {
			quota["storage_bytes"] = *storageBytes
		}
		if set["search-qps"] {
			quota["search_qps"] = *searchQPS
		}
		if set["materialize-rate"] {
			quota["materialize_rate"] = *materializeRate
		}
		if set["audit-volume-per-day"] {
			quota["audit_volume_per_day"] = *auditVolume
		}
		if set["max-user-layers"] {
			quota["max_user_layers"] = *maxUserLayers
		}
		return quota
	}
}

// podium admin tenant create <name> [--storage-bytes N] [--search-qps N]
//
//	[--materialize-rate N] [--audit-volume-per-day N] [--max-user-layers N]
//	[--expose-scope-preview true|false] [--registry URL]
func adminTenantCreate(args []string) int {
	fs := flag.NewFlagSet("admin tenant create", flag.ContinueOnError)
	setUsage(fs, "Create a tenant.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	buildQuota := tenantQuotaFlags(fs)
	exposeScopePreview := fs.String("expose-scope-preview", "", "scope-preview gate (true|false)")
	fs.SetOutput(os.Stderr)
	name, rest := leadingPositional(args)
	if err := fs.Parse(rest); err != nil {
		return parseExit(err)
	}
	if *registry == "" || name == "" {
		fmt.Fprintln(os.Stderr, "usage: podium admin tenant create <name> [flags] --registry URL")
		return 2
	}
	set := setFlags(fs)
	body := map[string]any{"name": name}
	if quota := buildQuota(set); len(quota) > 0 {
		body["quota"] = quota
	}
	if set["expose-scope-preview"] {
		b, rc := parseBoolFlag("expose-scope-preview", *exposeScopePreview)
		if rc != 0 {
			return rc
		}
		body["expose_scope_preview"] = b
	}
	out, status := doJSON(*registry+"/v1/admin/tenants", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "create failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// podium admin tenant list [--json] [--registry URL]
func adminTenantList(args []string) int {
	fs := flag.NewFlagSet("admin tenant list", flag.ContinueOnError)
	setUsage(fs, "List tenants.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	asJSON := fs.Bool("json", false, "emit the raw JSON array")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/admin/tenants", "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "list failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	if *asJSON {
		fmt.Println(string(out))
		return 0
	}
	var resp struct {
		Tenants []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "decode response: %v\n", err)
		return 1
	}
	fmt.Printf("%-40s  %-24s  %s\n", "ID", "NAME", "STATE")
	for _, tn := range resp.Tenants {
		state := "active"
		if !tn.Active {
			state = "inactive"
		}
		fmt.Printf("%-40s  %-24s  %s\n", tn.ID, tn.Name, state)
	}
	return 0
}

// podium admin tenant update <id> [--storage-bytes N] [...]
//
//	[--expose-scope-preview true|false] [--active true|false] [--registry URL]
func adminTenantUpdate(args []string) int {
	fs := flag.NewFlagSet("admin tenant update", flag.ContinueOnError)
	setUsage(fs, "Update a tenant's quota, scope-preview gate, or active state.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	buildQuota := tenantQuotaFlags(fs)
	exposeScopePreview := fs.String("expose-scope-preview", "", "scope-preview gate (true|false)")
	active := fs.String("active", "", "active state (true|false); false deactivates, true reactivates")
	fs.SetOutput(os.Stderr)
	id, rest := leadingPositional(args)
	if err := fs.Parse(rest); err != nil {
		return parseExit(err)
	}
	if *registry == "" || id == "" {
		fmt.Fprintln(os.Stderr, "usage: podium admin tenant update <id> [flags] --registry URL")
		return 2
	}
	set := setFlags(fs)
	body := map[string]any{}
	if quota := buildQuota(set); len(quota) > 0 {
		body["quota"] = quota
	}
	if set["expose-scope-preview"] {
		b, rc := parseBoolFlag("expose-scope-preview", *exposeScopePreview)
		if rc != 0 {
			return rc
		}
		body["expose_scope_preview"] = b
	}
	if set["active"] {
		b, rc := parseBoolFlag("active", *active)
		if rc != 0 {
			return rc
		}
		body["active"] = b
	}
	if len(body) == 0 {
		fmt.Fprintln(os.Stderr, "error: nothing to update; pass at least one quota, --expose-scope-preview, or --active flag")
		return 2
	}
	out, status := doJSON(*registry+"/v1/admin/tenants/"+url.PathEscape(id), "PATCH", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "update failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// podium admin tenant deactivate <id> [--registry URL]
func adminTenantDeactivate(args []string) int {
	fs := flag.NewFlagSet("admin tenant deactivate", flag.ContinueOnError)
	setUsage(fs, "Deactivate a tenant (soft).")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	id, rest := leadingPositional(args)
	if err := fs.Parse(rest); err != nil {
		return parseExit(err)
	}
	if *registry == "" || id == "" {
		fmt.Fprintln(os.Stderr, "usage: podium admin tenant deactivate <id> --registry URL")
		return 2
	}
	out, status := doJSON(*registry+"/v1/admin/tenants/"+url.PathEscape(id), "DELETE", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "deactivate failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Fprintf(os.Stderr, "tenant %s deactivated\n", id)
	return 0
}

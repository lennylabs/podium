package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"time"

	"github.com/lennylabs/podium/pkg/sync"
)

// sleepFor blocks for d. Wrapped so tests can override without
// relying on time.Sleep.
var sleepFor = func(d time.Duration) { time.Sleep(d) }

// layerCmd dispatches `podium layer ...` subcommands per spec §7.3.1.
//
//	podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>]
//	podium layer register --id <id> --local <path>
//	podium layer list
//	podium layer reorder <id> [<id> ...]
//	podium layer unregister <id>
//	podium layer reingest <id>
func layerCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("layer", "Manage layers registered with the registry.", [][2]string{
			{"register", "Register a layer with the registry."},
			{"list", "List registered layers."},
			{"reorder", "Re-sequence the layer list."},
			{"unregister", "Remove a layer."},
			{"restore", "Recover a layer unregistered within the recovery window."},
			{"reingest", "Trigger a fresh ingest for a layer."},
			{"update", "Patch a registered layer's mutable fields."},
			{"watch", "Poll a layer's source on an interval."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "register":
		return layerRegister(args[1:])
	case "list":
		return layerList(args[1:])
	case "reorder":
		return layerReorder(args[1:])
	case "unregister":
		return layerUnregister(args[1:])
	case "restore":
		return layerRestore(args[1:])
	case "reingest":
		return layerReingest(args[1:])
	case "update":
		return layerUpdate(args[1:])
	case "watch":
		return layerWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown layer subcommand: %s\n", args[0])
		return 2
	}
}

// layerUpdate sends a partial-patch PUT to /v1/layers/update?id=ID.
// Only flags the operator passes are applied; everything else
// keeps its prior value.
func layerUpdate(args []string) int {
	fs := flag.NewFlagSet("layer update", flag.ContinueOnError)
	setUsage(fs, "Patch a registered layer's mutable fields.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	id := fs.String("id", "", "layer id (required)")
	ref := fs.String("ref", "", "git ref")
	root := fs.String("root", "", "git subpath")
	local := fs.String("local", "", "filesystem path")
	owner := fs.String("owner", "", "OIDC sub of the user-defined layer's owner")
	public := fs.Bool("public", false, "set visibility to public")
	organization := fs.Bool("organization", false, "set visibility to organization-wide")
	forcePush := fs.String("force-push-policy", "", "git force-push handling: tolerant or strict")
	rotateSecret := fs.Bool("rotate-webhook-secret", false, "regenerate the git layer's HMAC webhook secret and print the new value")
	var groups, users stringSliceFlag
	fs.Var(&groups, "group", "OIDC group with visibility (repeatable)")
	fs.Var(&users, "user", "OIDC sub with visibility (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" || *id == "" {
		fmt.Fprintln(os.Stderr, "error: --registry and --id are required")
		return 2
	}
	body := map[string]any{}
	if *ref != "" {
		body["ref"] = *ref
	}
	if *root != "" {
		body["root"] = *root
	}
	if *local != "" {
		body["local_path"] = *local
	}
	if *forcePush != "" {
		body["force_push_policy"] = *forcePush
	}
	if *rotateSecret {
		body["rotate_webhook_secret"] = true
	}
	if *owner != "" {
		body["owner"] = *owner
	}
	if *public {
		body["public"] = true
	}
	if *organization {
		body["organization"] = true
	}
	if len(groups) > 0 {
		body["groups"] = []string(groups)
	}
	if len(users) > 0 {
		body["users"] = []string(users)
	}
	if len(body) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one mutable field must be provided")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers/update?id="+*id, "PUT", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "update failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// layerWatch periodically POSTs to /v1/layers/reingest?id=ID until
// the user interrupts. Useful as a manual replacement for the
// per-layer webhook callback when the source isn't reachable from
// the registry.
func layerWatch(args []string) int {
	fs := flag.NewFlagSet("layer watch", flag.ContinueOnError)
	setUsage(fs, "Poll a layer's source on an interval.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	id := fs.String("id", "", "layer id (required)")
	// spec §7.3.1 / §14.10: `podium layer watch <id> [--interval <duration>]`.
	// The interval is a Go-style duration (e.g. 30s, 1h) so the §14.10 example
	// `--interval 1h` parses; a bare integer is rejected by flag parsing.
	interval := fs.Duration("interval", time.Minute, "duration between reingest pokes (e.g. 30s, 1h)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	*registry = resolveLayerRegistry(*registry)
	if *registry == "" || *id == "" {
		fmt.Fprintln(os.Stderr, "error: --registry and --id are required")
		return 2
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "error: --interval must be positive")
		return 2
	}
	url := *registry + "/v1/layers/reingest?id=" + *id
	for {
		out, status := doJSON(url, "POST", nil)
		if status >= 400 {
			fmt.Fprintf(os.Stderr, "reingest failed: HTTP %d\n%s\n", status, out)
		} else {
			fmt.Printf("[reingest %s] %s\n", *id, out)
		}
		sleepFor(*interval)
	}
}

func layerRegister(args []string) int {
	fs := flag.NewFlagSet("layer register", flag.ContinueOnError)
	setUsage(fs, "Register a layer with the registry.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	id := fs.String("id", "", "layer id (required)")
	repo := fs.String("repo", "", "git repo URL (for git source)")
	ref := fs.String("ref", "", "git ref (for git source)")
	root := fs.String("root", "", "subpath under the repo")
	local := fs.String("local", "", "filesystem path (for local source)")
	userDefined := fs.Bool("user-defined", false, "register a personal layer")
	owner := fs.String("owner", "", "OIDC sub of the user-defined layer's owner")
	public := fs.Bool("public", false, "visibility: public")
	organization := fs.Bool("organization", false, "visibility: organization-wide")
	forcePush := fs.String("force-push-policy", "", "git force-push handling: tolerant (default) or strict")
	var groups, users stringSliceFlag
	fs.Var(&groups, "group", "OIDC group with visibility (repeatable)")
	fs.Var(&users, "user", "OIDC sub with visibility (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	*registry = resolveLayerRegistry(*registry)
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required (set --registry, PODIUM_REGISTRY, or defaults.registry in ~/.podium/sync.yaml)")
		return 2
	}
	if *id == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		return 2
	}
	body := map[string]any{"id": *id}
	if *repo != "" {
		body["source_type"] = "git"
		body["repo"] = *repo
		body["ref"] = *ref
		if *root != "" {
			body["root"] = *root
		}
	} else if *local != "" {
		body["source_type"] = "local"
		body["local_path"] = *local
	} else {
		fmt.Fprintln(os.Stderr, "error: --repo (with --ref) or --local is required")
		return 2
	}
	if *userDefined {
		body["user_defined"] = true
		body["owner"] = *owner
	}
	if *forcePush != "" {
		body["force_push_policy"] = *forcePush
	}
	if *public {
		body["public"] = true
	}
	if *organization {
		body["organization"] = true
	}
	if len(groups) > 0 {
		body["groups"] = []string(groups)
	}
	if len(users) > 0 {
		body["users"] = []string(users)
	}

	out, status := doJSON(*registry+"/v1/layers", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "register failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	// spec §14.10 step 3: "The CLI prints the webhook URL it would expect."
	// Surface the server's absolute webhook URL on its own labeled line so a
	// developer can paste it into a Git host's webhook configuration without
	// digging it out of the raw JSON.
	var reg struct {
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.Unmarshal(out, &reg); err == nil && reg.WebhookURL != "" {
		fmt.Printf("webhook URL: %s\n", reg.WebhookURL)
	}
	return 0
}

func layerList(args []string) int {
	fs := flag.NewFlagSet("layer list", flag.ContinueOnError)
	setUsage(fs, "List registered layers.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	deleted := fs.Bool("deleted", false, "list soft-deleted layers recoverable within the §8.4 window")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	url := *registry + "/v1/layers"
	if *deleted {
		url += "?deleted=true"
	}
	out, status := doJSON(url, "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "list failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func layerReorder(args []string) int {
	fs := flag.NewFlagSet("layer reorder", flag.ContinueOnError)
	setUsage(fs, "Re-sequence the layer list.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: podium layer reorder <id> [<id> ...]")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	body := map[string]any{"order": fs.Args()}
	out, status := doJSON(*registry+"/v1/layers/reorder", "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "reorder failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func layerUnregister(args []string) int {
	fs := flag.NewFlagSet("layer unregister", flag.ContinueOnError)
	setUsage(fs, "Remove a layer.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer unregister <id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers?id="+fs.Arg(0), "DELETE", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "unregister failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// layerRestore recovers a layer (and its artifacts) that was
// unregistered within the §8.4 30-day recovery window.
func layerRestore(args []string) int {
	fs := flag.NewFlagSet("layer restore", flag.ContinueOnError)
	setUsage(fs, "Recover a layer unregistered within the recovery window.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer restore <id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers/restore?id="+fs.Arg(0), "POST", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "restore failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func layerReingest(args []string) int {
	fs := flag.NewFlagSet("layer reingest", flag.ContinueOnError)
	setUsage(fs, "Trigger a fresh ingest for a layer.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	// spec §7.3.1 / §4.7.2: break-glass overrides an active freeze window on
	// the manual reingest path. It requires a justification and (per the
	// dual-signoff rule) two distinct approvers, supplied by the repeatable
	// --approver flag plus the authenticated caller.
	breakGlass := fs.Bool("break-glass", false, "override an active freeze window (requires --justification)")
	justification := fs.String("justification", "", "reason for the break-glass override")
	var approvers stringSliceFlag
	fs.Var(&approvers, "approver", "break-glass approver identity (repeatable, for dual-signoff)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer reingest [--break-glass --justification <text>] <id>")
		return 2
	}
	*registry = resolveLayerRegistry(*registry)
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required (set --registry, PODIUM_REGISTRY, or defaults.registry in ~/.podium/sync.yaml)")
		return 2
	}
	if *justification != "" && !*breakGlass {
		fmt.Fprintln(os.Stderr, "error: --justification is only valid with --break-glass")
		return 2
	}
	var body any
	if *breakGlass {
		if *justification == "" {
			fmt.Fprintln(os.Stderr, "error: --break-glass requires --justification")
			return 2
		}
		req := map[string]any{"break_glass": true, "justification": *justification}
		if len(approvers) > 0 {
			req["approvers"] = []string(approvers)
		}
		body = req
	}
	out, status := doJSON(*registry+"/v1/layers/reingest?id="+fs.Arg(0), "POST", body)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "reingest failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	// §0 quickstart: print one `artifact: <id>@<version>   layer: <layer>`
	// line per ingested artifact. Fall back to the raw response body when the
	// registry returned no artifact list (for example a queue-only
	// acknowledgement from a server with no ingest runner wired).
	var parsed struct {
		Layer     string `json:"layer"`
		Artifacts []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"artifacts"`
		Advisories []struct {
			ArtifactID string `json:"artifact_id"`
			Code       string `json:"code"`
			Severity   string `json:"severity"`
			Message    string `json:"message"`
		} `json:"advisories"`
		Conflicts []struct {
			ArtifactID string `json:"artifact_id"`
			Version    string `json:"version"`
			Code       string `json:"code"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(out, &parsed); err == nil && len(parsed.Artifacts) > 0 {
		layer := parsed.Layer
		if layer == "" {
			layer = fs.Arg(0)
		}
		for _, a := range parsed.Artifacts {
			fmt.Printf("artifact: %s@%s   layer: %s\n", a.ID, a.Version, layer)
		}
		// §4.6 / §3.3: print any non-blocking advisories (e.g. a cross-layer
		// license change) so the publisher sees them on a runtime reingest.
		for _, a := range parsed.Advisories {
			fmt.Printf("advisory: %s [%s] %s (%s)\n", a.ArtifactID, a.Severity, a.Message, a.Code)
		}
		// §7.3.1: a same-version content conflict is rejected as
		// ingest.immutable_violation even when sibling artifacts ingested. Print
		// each so the author sees which artifact must have its version bumped
		// (F-7.3.2). A snapshot with only conflicts surfaces as a 409 above.
		for _, c := range parsed.Conflicts {
			fmt.Fprintf(os.Stderr, "conflict: %s@%s rejected (%s); bump the version\n", c.ArtifactID, c.Version, c.Code)
		}
		return 0
	}
	fmt.Println(string(out))
	return 0
}

// resolveLayerRegistry resolves the registry URL for the standalone `podium
// layer` subcommands. flagVal is the --registry flag value (already defaulted
// to PODIUM_REGISTRY). When both are empty it falls back to defaults.registry
// in the merged ~/.podium/sync.yaml, the same precedence `podium sync` uses
// (resolveOverrideRegistry). This lets the §14.10 register / reingest / watch
// commands run against the standalone server that `podium serve --standalone`
// bootstrapped into ~/.podium/sync.yaml, with no explicit --registry.
//
// spec: §14.10 — the standalone bootstrap writes defaults.registry into
// ~/.podium/sync.yaml; the layer commands resolve it.
func resolveLayerRegistry(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	ws, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return mergedRegistry(ws, home)
}

// mergedRegistry returns defaults.registry from the merged sync.yaml resolved
// under workspace + home, or "" when none is configured. It is split out from
// resolveLayerRegistry so a unit test can supply explicit directories without
// mutating the process working directory or HOME.
func mergedRegistry(workspace, home string) string {
	if merged, _, _ := sync.LoadMergedConfig(workspace, home); merged != nil {
		return merged.Defaults.Registry
	}
	return ""
}

// requestBase strips the path and query from a full request URL, recovering
// the registry base (scheme://host[:port]) used as the keychain key by
// `podium login`. On a parse failure it returns the input unchanged.
func requestBase(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

// doJSON makes an HTTP request with optional JSON body and returns
// the response bytes + status code.
func doJSON(url, method string, body any) ([]byte, int) {
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		return []byte(err.Error()), 500
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// spec: §7.6 / §7.6.1 — layer, admin, and quota commands reach
	// authenticated registry endpoints with the caller's identity. Attach the
	// resolved credential (injected session token, then the keychain access
	// token keyed by the registry URL) so the same omission flagged for the
	// read commands does not recur here (F-14.15.1).
	if tok := readCLIToken(requestBase(url)); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return []byte(err.Error()), 500
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode
}

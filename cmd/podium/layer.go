package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// sleepSeconds blocks for n seconds. Wrapped so tests can override
// without relying on time.Sleep.
var sleepSeconds = func(n int) { time.Sleep(time.Duration(n) * time.Second) }

// layerCmd dispatches `podium layer ...` subcommands per spec §7.3.1.
//
//	podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>]
//	podium layer register --id <id> --local <path>
//	podium layer list
//	podium layer reorder <id> [<id> ...]
//	podium layer unregister <id>
//	podium layer reingest <id>
func layerCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer <register|list|reorder|unregister|reingest> [flags]")
		return 2
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
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	id := fs.String("id", "", "layer id (required)")
	ref := fs.String("ref", "", "git ref")
	root := fs.String("root", "", "git subpath")
	local := fs.String("local", "", "filesystem path")
	owner := fs.String("owner", "", "OIDC sub of the user-defined layer's owner")
	public := fs.Bool("public", false, "set visibility to public")
	organization := fs.Bool("organization", false, "set visibility to organization-wide")
	var groups, users stringSliceFlag
	fs.Var(&groups, "group", "OIDC group with visibility (repeatable)")
	fs.Var(&users, "user", "OIDC sub with visibility (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
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
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	id := fs.String("id", "", "layer id (required)")
	intervalSec := fs.Int("interval", 60, "seconds between reingest pokes")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" || *id == "" {
		fmt.Fprintln(os.Stderr, "error: --registry and --id are required")
		return 2
	}
	if *intervalSec <= 0 {
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
		sleepSeconds(*intervalSec)
	}
}

func layerRegister(args []string) int {
	fs := flag.NewFlagSet("layer register", flag.ContinueOnError)
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
	var groups, users stringSliceFlag
	fs.Var(&groups, "group", "OIDC group with visibility (repeatable)")
	fs.Var(&users, "user", "OIDC sub with visibility (repeatable)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required (or set PODIUM_REGISTRY)")
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
	return 0
}

func layerList(args []string) int {
	fs := flag.NewFlagSet("layer list", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers", "GET", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "list failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

func layerReorder(args []string) int {
	fs := flag.NewFlagSet("layer reorder", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
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
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
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

func layerReingest(args []string) int {
	fs := flag.NewFlagSet("layer reingest", flag.ContinueOnError)
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: podium layer reingest <id>")
		return 2
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	out, status := doJSON(*registry+"/v1/layers/reingest?id="+fs.Arg(0), "POST", nil)
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "reingest failed: HTTP %d\n%s\n", status, out)
		return 1
	}
	fmt.Println(string(out))
	return 0
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return []byte(err.Error()), 500
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode
}

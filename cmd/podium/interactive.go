package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/pkg/sync"
)

// interactiveStdin and interactiveIsTerminal are indirections so tests can
// drive the §7.5.5 / §7.5.7 interactive modes without a real terminal. The
// command loops read from interactiveStdin (a pipe under test, a tty in
// production); interactiveIsTerminal only governs whether a prompt is printed
// and whether an empty, non-terminal invocation prints actionable guidance
// rather than silently doing nothing. The loops always terminate on stdin EOF,
// so they never block.
var (
	interactiveStdin      io.Reader = os.Stdin
	interactiveIsTerminal           = func() bool {
		fi, err := os.Stdin.Stat()
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeCharDevice != 0
	}
)

// overrideTUIResult is the outcome of the §7.5.5 override checklist: the IDs to
// materialize and to drop relative to the current state, whether the user
// applied (saved) the diff, and how many commands the loop recognized.
type overrideTUIResult struct {
	Add      []string
	Remove   []string
	Apply    bool
	Commands int
}

// runOverrideTUI renders the caller's effective view as an expandable tree
// (domains as nodes, artifacts as leaf checkboxes) and reads toggle commands
// until the user saves or quits. Each leaf's checkbox starts at its current
// materialization state; on save the result reports the added (newly checked)
// and removed (newly unchecked) IDs. spec: §7.5.5.
func runOverrideTUI(in io.Reader, out io.Writer, view []sync.EffectiveArtifact, tty bool) overrideTUIResult {
	selected := make([]bool, len(view))
	for i, a := range view {
		selected[i] = a.Materialized
	}
	collapsed := map[string]bool{}

	renderOverrideTree(out, view, selected, collapsed)
	r := bufio.NewReader(in)
	res := overrideTUIResult{}
	for {
		if tty {
			fmt.Fprint(out, "> ")
		}
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			res.Commands++
			cmd, arg := splitCommand(line)
			switch cmd {
			case "save", "apply", "w":
				res.Apply = true
				res.Add, res.Remove = overrideDiff(view, selected)
				return res
			case "quit", "cancel", "q":
				return res
			case "list", "print", "?", "help":
				renderOverrideTree(out, view, selected, collapsed)
			case "all":
				for i := range selected {
					selected[i] = true
				}
				fmt.Fprintln(out, "selected all")
			case "none":
				for i := range selected {
					selected[i] = false
				}
				fmt.Fprintln(out, "deselected all")
			case "expand":
				collapsed[strings.TrimSuffix(arg, "/")] = false
				renderOverrideTree(out, view, selected, collapsed)
			case "collapse":
				collapsed[strings.TrimSuffix(arg, "/")] = true
				renderOverrideTree(out, view, selected, collapsed)
			default:
				if n, perr := strconv.Atoi(cmd); perr == nil && n >= 1 && n <= len(view) {
					selected[n-1] = !selected[n-1]
					state := "off"
					if selected[n-1] {
						state = "on"
					}
					fmt.Fprintf(out, "toggled %d: %s [%s]\n", n, view[n-1].ID, state)
				} else {
					fmt.Fprintf(out, "unrecognized command %q (try: <number>, all, none, expand <domain>, collapse <domain>, list, save, quit)\n", line)
				}
			}
		}
		if err != nil {
			return res
		}
	}
}

// overrideDiff computes the §7.5.5 add/remove sets from the final checkbox
// state: an artifact checked but not currently materialized is added, and one
// unchecked but currently materialized is removed.
func overrideDiff(view []sync.EffectiveArtifact, selected []bool) (add, remove []string) {
	for i, a := range view {
		switch {
		case selected[i] && !a.Materialized:
			add = append(add, a.ID)
		case !selected[i] && a.Materialized:
			remove = append(remove, a.ID)
		}
	}
	return add, remove
}

// renderOverrideTree prints the effective view grouped by top-level domain.
// Collapsed domains render a one-line summary; expanded domains list each
// artifact leaf with its checkbox, type, materialization state, and layer.
func renderOverrideTree(out io.Writer, view []sync.EffectiveArtifact, selected []bool, collapsed map[string]bool) {
	fmt.Fprintln(out, "podium sync override — interactive checklist")
	fmt.Fprintln(out, "Toggle an item by its number. Commands: <n>, all, none, expand <domain>, collapse <domain>, list, save, quit.")
	if len(view) == 0 {
		fmt.Fprintln(out, "(no artifacts visible)")
		return
	}
	// Group leaf indices by domain, preserving the sorted leaf order.
	domains := []string{}
	byDomain := map[string][]int{}
	for i, a := range view {
		d := domainOf(a.ID)
		if _, ok := byDomain[d]; !ok {
			domains = append(domains, d)
		}
		byDomain[d] = append(byDomain[d], i)
	}
	sort.Strings(domains)
	for _, d := range domains {
		idxs := byDomain[d]
		if collapsed[d] {
			fmt.Fprintf(out, "%s/  (collapsed, %d items)\n", d, len(idxs))
			continue
		}
		fmt.Fprintf(out, "%s/\n", d)
		for _, i := range idxs {
			a := view[i]
			box := " "
			if selected[i] {
				box = "x"
			}
			state := ""
			if a.Materialized {
				state = "materialized"
			}
			typ := a.Type
			if typ == "" {
				typ = "-"
			}
			fmt.Fprintf(out, "  [%s] %3d  %-40s %-9s %-14s (%s)\n", box, i+1, a.ID, typ, state, a.Layer)
		}
	}
}

// domainOf returns the top-level domain segment of a canonical artifact ID
// (the part before the first slash), or "(root)" for a slash-less ID.
func domainOf(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return "(root)"
}

// profileEditTUIResult is the outcome of the §7.5.7 profile editor: the final
// include and exclude pattern lists, whether the user saved, and how many
// commands the loop recognized.
type profileEditTUIResult struct {
	Include  []string
	Exclude  []string
	Apply    bool
	Commands int
}

// runProfileEditTUI renders a profile's include/exclude pattern lists and reads
// edit commands (add-include, remove-include, add-exclude, remove-exclude)
// until the user saves or quits. spec: §7.5.7.
func runProfileEditTUI(in io.Reader, out io.Writer, name string, cur sync.Profile, tty bool) profileEditTUIResult {
	include := append([]string(nil), cur.Include...)
	exclude := append([]string(nil), cur.Exclude...)
	renderProfile(out, name, include, exclude)
	r := bufio.NewReader(in)
	res := profileEditTUIResult{}
	removeAt := func(list []string, arg string) ([]string, bool) {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > len(list) {
			return list, false
		}
		return append(list[:n-1:n-1], list[n:]...), true
	}
	for {
		if tty {
			fmt.Fprint(out, "> ")
		}
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			res.Commands++
			cmd, arg := splitCommand(line)
			switch cmd {
			case "save", "apply", "w":
				res.Apply = true
				res.Include, res.Exclude = include, exclude
				return res
			case "quit", "cancel", "q":
				return res
			case "list", "print", "?", "help":
				renderProfile(out, name, include, exclude)
			case "add-include", "ai":
				if arg != "" {
					include = appendUnique(include, arg)
					fmt.Fprintf(out, "include += %s\n", arg)
				}
			case "remove-include", "ri":
				if next, ok := removeAt(include, arg); ok {
					include = next
					fmt.Fprintf(out, "include -= #%s\n", arg)
				} else {
					fmt.Fprintf(out, "no include entry #%s\n", arg)
				}
			case "add-exclude", "ae":
				if arg != "" {
					exclude = appendUnique(exclude, arg)
					fmt.Fprintf(out, "exclude += %s\n", arg)
				}
			case "remove-exclude", "re":
				if next, ok := removeAt(exclude, arg); ok {
					exclude = next
					fmt.Fprintf(out, "exclude -= #%s\n", arg)
				} else {
					fmt.Fprintf(out, "no exclude entry #%s\n", arg)
				}
			default:
				fmt.Fprintf(out, "unrecognized command %q (try: add-include <pat>, remove-include <n>, add-exclude <pat>, remove-exclude <n>, list, save, quit)\n", line)
			}
		}
		if err != nil {
			return res
		}
	}
}

// renderProfile prints a profile's include and exclude lists with 1-based
// indices for the remove commands.
func renderProfile(out io.Writer, name string, include, exclude []string) {
	fmt.Fprintf(out, "podium profile edit %q\n", name)
	fmt.Fprintln(out, "Commands: add-include <pattern>, remove-include <n>, add-exclude <pattern>, remove-exclude <n>, list, save, quit.")
	printPatternList(out, "include", include)
	printPatternList(out, "exclude", exclude)
}

func printPatternList(out io.Writer, label string, items []string) {
	fmt.Fprintf(out, "%s:\n", label)
	if len(items) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for i, p := range items {
		fmt.Fprintf(out, "  %3d  %s\n", i+1, p)
	}
}

// appendUnique appends s to list when it is not already present.
func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// runSyncOverrideInteractive implements the §7.5.5 `podium sync override` TUI
// mode: it resolves the caller's effective view, runs the checklist over it,
// and applies the resulting add/remove toggles through sync.Override (the same
// path the --add/--remove flags drive). A non-terminal invocation with no
// scripted input prints actionable guidance instead of blocking.
func runSyncOverrideInteractive(target, registryFlag, harnessFlag string, dryRun bool) int {
	registryPath := resolveOverrideRegistry(registryFlag)
	view, err := sync.ResolveEffectiveView(sync.Options{
		RegistryPath: registryPath,
		Target:       target,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "override: resolve view: %v\n", err)
		return 1
	}
	tty := interactiveIsTerminal()
	res := runOverrideTUI(interactiveStdin, os.Stdout, view, tty)
	if res.Commands == 0 && !tty {
		fmt.Fprintln(os.Stderr, "interactive override needs a terminal or piped commands; use --add <id>, --remove <id>, or --reset for non-interactive changes")
		return 0
	}
	if !res.Apply {
		fmt.Fprintln(os.Stderr, "override: cancelled; no changes written")
		return 0
	}
	if len(res.Add) == 0 && len(res.Remove) == 0 {
		fmt.Println("(no change)")
		return 0
	}
	ovr, err := sync.Override(sync.OverrideOptions{
		Target:       target,
		Add:          res.Add,
		Remove:       res.Remove,
		DryRun:       dryRun,
		RegistryPath: registryPath,
		AdapterID:    resolveOverrideHarness(harnessFlag, target),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "override failed: %v\n", err)
		return 1
	}
	for _, w := range ovr.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if dryRun {
		fmt.Println("(dry-run; nothing written)")
	}
	fmt.Printf("toggles.add:    %s\n", formatToggles(ovr.Lock.Toggles.Add))
	fmt.Printf("toggles.remove: %s\n", formatToggles(ovr.Lock.Toggles.Remove))
	return 0
}

// runProfileEditInteractive implements the §7.5.7 `podium profile edit` TUI
// mode: it loads the named profile's include/exclude lists, runs the editor,
// and writes the resulting deltas through sync.ProfileEdit (the same path the
// --add-include/--remove-include flags drive). A non-terminal invocation with
// no scripted input prints actionable guidance instead of blocking.
func runProfileEditInteractive(name, target string, dryRun bool) int {
	var cur sync.Profile
	if cfg, err := sync.ReadConfig(target); err == nil && cfg != nil {
		cur = cfg.Profiles[name]
	}
	tty := interactiveIsTerminal()
	res := runProfileEditTUI(interactiveStdin, os.Stdout, name, cur, tty)
	if res.Commands == 0 && !tty {
		fmt.Fprintln(os.Stderr, "interactive profile editing needs a terminal or piped commands; use --add-include/--remove-include/--add-exclude/--remove-exclude for non-interactive changes")
		return 0
	}
	if !res.Apply {
		fmt.Fprintln(os.Stderr, "profile edit: cancelled; no changes written")
		return 0
	}
	addInc, removeInc := diffStrings(cur.Include, res.Include)
	addExc, removeExc := diffStrings(cur.Exclude, res.Exclude)
	if len(addInc)+len(removeInc)+len(addExc)+len(removeExc) == 0 {
		fmt.Printf("profile: %s\n", name)
		fmt.Println("(no change)")
		return 0
	}
	editRes, err := sync.ProfileEdit(sync.ProfileEditOptions{
		Target:        target,
		Profile:       name,
		AddInclude:    addInc,
		RemoveInclude: removeInc,
		AddExclude:    addExc,
		RemoveExclude: removeExc,
		DryRun:        dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile edit failed: %v\n", err)
		return 1
	}
	fmt.Printf("profile: %s\n", name)
	fmt.Printf("  include: %s\n", formatList(editRes.Profile.Include))
	fmt.Printf("  exclude: %s\n", formatList(editRes.Profile.Exclude))
	if dryRun {
		fmt.Println("(dry-run; nothing written)")
	}
	return 0
}

// splitCommand splits a command line into its first token (the command) and the
// remainder (the argument), both trimmed.
func splitCommand(line string) (cmd, arg string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", ""
	}
	cmd = fields[0]
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	return cmd, arg
}

// diffStrings returns the entries in next that are absent from prev (added) and
// the entries in prev that are absent from next (removed), preserving order.
func diffStrings(prev, next []string) (added, removed []string) {
	prevSet := make(map[string]bool, len(prev))
	for _, s := range prev {
		prevSet[s] = true
	}
	nextSet := make(map[string]bool, len(next))
	for _, s := range next {
		nextSet[s] = true
	}
	for _, s := range next {
		if !prevSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range prev {
		if !nextSet[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

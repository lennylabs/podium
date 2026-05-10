package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

// Spec: n/a — internal CLI help-output wiring.
func TestSetUsage_PrintsDescriptionAndFlags(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("widget make", flag.ContinueOnError)
	setUsage(fs, "Construct a widget.")
	fs.String("color", "blue", "widget color")
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.Usage()
	got := buf.String()
	for _, want := range []string{
		"podium widget make - Construct a widget.",
		"Flags:",
		"-color string",
		"widget color",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("usage output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// Spec: n/a — internal CLI help-output wiring.
func TestFprintGroupHelp_AlignsSubcommandColumn(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	fprintGroupHelp(&buf, "layer", "Manage layers.", [][2]string{
		{"register", "Register a layer."},
		{"reorder", "Re-sequence layers."},
	})
	got := buf.String()
	if !strings.Contains(got, "podium layer - Manage layers.") {
		t.Errorf("missing header: %s", got)
	}
	if !strings.Contains(got, "Subcommands:") {
		t.Errorf("missing Subcommands heading: %s", got)
	}
	// The longest subcommand name is "register" (8 chars); both rows
	// pad the first column to that width.
	if !strings.Contains(got, "  register  Register a layer.") {
		t.Errorf("register row alignment off: %q", got)
	}
	if !strings.Contains(got, "  reorder   Re-sequence layers.") {
		t.Errorf("reorder row alignment off: %q", got)
	}
}

// Spec: n/a — internal CLI help-output wiring.
func TestIsHelpArg(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"help", "-h", "--help"} {
		if !isHelpArg(s) {
			t.Errorf("isHelpArg(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "h", "-help", "register", "--version"} {
		if isHelpArg(s) {
			t.Errorf("isHelpArg(%q) = true, want false", s)
		}
	}
}

// Spec: n/a — dispatcher groups respond to --help with their subcommand
// list and exit 0 (no missing-args treatment).
func TestAdminCmd_HelpExitsCleanly(t *testing.T) {
	t.Parallel()
	// adminCmd writes to os.Stdout for the help branch; we only
	// assert the exit code here. Stdout-content tests live in the
	// fprintGroupHelp test above.
	if code := adminCmd([]string{"--help"}); code != 0 {
		t.Errorf("adminCmd(--help) exit = %d, want 0", code)
	}
	if code := adminCmd([]string{"help"}); code != 0 {
		t.Errorf("adminCmd(help) exit = %d, want 0", code)
	}
	if code := adminCmd([]string{"-h"}); code != 0 {
		t.Errorf("adminCmd(-h) exit = %d, want 0", code)
	}
	if code := adminCmd([]string{}); code != 2 {
		t.Errorf("adminCmd(no args) exit = %d, want 2", code)
	}
}

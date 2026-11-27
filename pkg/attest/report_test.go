package attest

import (
	"strings"
	"testing"
)

// Spec: §4.7.11 (Reported strings) — a string longer than 256 bytes is cut
// at a character boundary and marked with its length and digest prefix, so
// two strings that share their first bytes read differently, and every
// control, format, line, and paragraph separator character, and every
// invalid byte, reads as U+FFFD in a cut string and in an uncut one.
func TestReportString(t *testing.T) {
	t.Parallel()
	if got := ReportString("alice@acme.com"); got != "alice@acme.com" {
		t.Errorf("short string = %q", got)
	}
	if got := ReportString("a\u202eb\nc\u2028d\xffe\u200bf"); got != "a\ufffdb\ufffdc\ufffdd\ufffde\ufffdf" {
		t.Errorf("controls = %q", got)
	}
	long := strings.Repeat("x", 255) + "é" + "tail"
	a, b := ReportString(long), ReportString(long+"!")
	if !strings.HasPrefix(a, strings.Repeat("x", 255)+"...(261 bytes, sha256:") || strings.Contains(a, "é") || a == b || len(a) != 255+len("...(261 bytes, sha256:)")+16 {
		t.Errorf("cut = %q, other = %q", a, b)
	}
	if got := ReportStrings([]string{"a", "b\n", "c"}, 2); len(got) != 2 || got[1] != "b\ufffd" {
		t.Errorf("list = %q", got)
	}
}

// Spec: §13.12 — the bundle limits take their defaults when unset and refuse
// a value that is not a decimal integer of at least 1, naming the setting.
func TestReadLimits(t *testing.T) {
	t.Parallel()
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if l, err := ReadLimits(get); err != nil || l.MaxBytes != DefaultMaxBundleBytes || l.MaxBundles != DefaultMaxBundles {
		t.Fatalf("defaults %+v %v", l, err)
	}
	env[MaxBundlesSetting] = "0"
	if _, err := ReadLimits(get); err == nil || !strings.Contains(err.Error(), MaxBundlesSetting) {
		t.Fatalf("zero bundles: %v", err)
	}
	env[MaxBundlesSetting], env[MaxBytesSetting] = "3", "x"
	if _, err := ReadLimits(get); err == nil || !strings.Contains(err.Error(), MaxBytesSetting) {
		t.Fatalf("bytes: %v", err)
	}
}

package main

import "testing"

// browserSuppressed honors PODIUM_NO_BROWSER with the usual truthy values, so
// a headless/CI environment or the test suite can suppress the login
// verification-URL auto-open without passing --no-browser. spec: §7.7.
func TestBrowserSuppressed_TruthyValues(t *testing.T) {
	suppress := []string{"1", "true", "TRUE", "Yes", "on", "  on  "}
	for _, v := range suppress {
		t.Run("suppress/"+v, func(t *testing.T) {
			t.Setenv("PODIUM_NO_BROWSER", v)
			if !browserSuppressed() {
				t.Errorf("PODIUM_NO_BROWSER=%q: want suppressed", v)
			}
		})
	}
	allow := []string{"", "0", "false", "no", "off", "nope"}
	for _, v := range allow {
		t.Run("allow/"+v, func(t *testing.T) {
			t.Setenv("PODIUM_NO_BROWSER", v)
			if browserSuppressed() {
				t.Errorf("PODIUM_NO_BROWSER=%q: want not suppressed", v)
			}
		})
	}
}

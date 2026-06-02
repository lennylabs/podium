package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// emitDeviceCodePending writes a §6.10 auth.device_code_pending envelope
// carrying the verification URL and user code, the structured replacement for
// the human prompt under `podium login --json`. F-6.3.3.
func TestEmitDeviceCodePending(t *testing.T) {
	var buf bytes.Buffer
	emitDeviceCodePending(&buf, &identity.DeviceAuth{
		VerificationURL:         "https://idp.example.com/device",
		UserCode:                "ABCD-EFGH",
		VerificationURLComplete: "https://idp.example.com/device?user_code=ABCD-EFGH",
	})
	var env struct {
		Code            string            `json:"code"`
		Message         string            `json:"message"`
		Details         map[string]string `json:"details"`
		Retryable       bool              `json:"retryable"`
		SuggestedAction string            `json:"suggested_action"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("emit produced non-JSON output %q: %v", buf.String(), err)
	}
	if env.Code != "auth.device_code_pending" {
		t.Errorf("code = %q, want auth.device_code_pending", env.Code)
	}
	if env.Details["verification_uri"] != "https://idp.example.com/device" {
		t.Errorf("details.verification_uri = %q", env.Details["verification_uri"])
	}
	if env.Details["user_code"] != "ABCD-EFGH" {
		t.Errorf("details.user_code = %q", env.Details["user_code"])
	}
	if env.Details["verification_uri_complete"] != "https://idp.example.com/device?user_code=ABCD-EFGH" {
		t.Errorf("details.verification_uri_complete = %q", env.Details["verification_uri_complete"])
	}
	if !env.Retryable {
		t.Errorf("retryable = false, want true")
	}
	if env.SuggestedAction == "" {
		t.Errorf("suggested_action empty, want a remediation hint")
	}

	// The complete URL is omitted when the IdP does not return one.
	buf.Reset()
	emitDeviceCodePending(&buf, &identity.DeviceAuth{
		VerificationURL: "https://idp.example.com/device",
		UserCode:        "WXYZ-1234",
	})
	var env2 struct {
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := env2.Details["verification_uri_complete"]; ok {
		t.Errorf("verification_uri_complete should be omitted when empty, got %q", env2.Details["verification_uri_complete"])
	}
}

// browserCommand selects the per-OS launcher §6.3 names: open (macOS),
// xdg-open (Linux), and start (Windows). The Windows path invokes the start
// cmd builtin as `cmd /c start "" <url>` rather than rundll32, with the empty
// title placeholder so a URL with & or spaces is not read as the window title.
// F-6.3.4.
func TestBrowserCommand_PerOS(t *testing.T) {
	const url = "https://idp.example.com/device?code=ABCD-EFGH"
	cases := []struct {
		goos string
		want []string
	}{
		{"darwin", []string{"open", url}},
		{"linux", []string{"xdg-open", url}},
		{"windows", []string{"cmd", "/c", "start", "", url}},
		{"freebsd", []string{"xdg-open", url}},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			cmd := browserCommand(tc.goos, url)
			if cmd == nil {
				t.Fatalf("browserCommand(%q) = nil", tc.goos)
			}
			if !reflect.DeepEqual(cmd.Args, tc.want) {
				t.Errorf("browserCommand(%q).Args = %v, want %v", tc.goos, cmd.Args, tc.want)
			}
		})
	}
	if cmd := browserCommand("darwin", ""); cmd != nil {
		t.Errorf("browserCommand with empty url = %v, want nil", cmd.Args)
	}
}

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

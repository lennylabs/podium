// Package harness_integration drives the real agent-harness binaries (Claude
// Code, Codex, Gemini, OpenCode, …) against a project materialized by the real
// `podium sync`, to confirm a harness actually accepts and discovers Podium's
// output. It realizes the §6.7 conformance line's opt-in end-to-end check.
//
// The tests live behind the harness_integration build tag, so normal builds and
// `go test ./...` never run them. Run them explicitly on a machine with the
// harness CLIs installed:
//
//	go test -tags harness_integration ./test/harness_integration/ -v
//
// See README.md for the tiers (config-accept vs. per-type agent behavior), the
// gates, and the per-harness coverage. This file exists with no build tag so the
// package is always buildable (and `go test` reports "no test files" rather than
// failing) when the tag is absent.
package harness_integration

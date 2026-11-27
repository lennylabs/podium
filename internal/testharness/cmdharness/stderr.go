package cmdharness

import "strings"

// coverageMetaFailure is the line the Go coverage runtime prints when an
// instrumented binary fails to write its meta-data file at startup.
const coverageMetaFailure = "error: coverage meta-data emit failed: "

// StripCoverageNoise removes the coverage runtime's meta-data failure line,
// and the blank line that follows it, from a subprocess's stderr.
//
// A binary built with -cover writes its meta-data file at startup through a
// temporary file named after the meta hash and time.Now().UnixNano(), with no
// process ID. On a platform whose clock reports microseconds, two parallel
// subprocesses of one binary that start in the same microsecond pick the same
// temporary name, and the second rename fails. The runtime prints the failure
// and the program runs on. The meta-data file the other process wrote is
// byte-identical, so no coverage is lost, but the line breaks a test that
// asserts what stderr begins with or that it is empty.
func StripCoverageNoise(stderr string) string {
	if !strings.Contains(stderr, coverageMetaFailure) {
		return stderr
	}
	lines := strings.SplitAfter(stderr, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], coverageMetaFailure) {
			out = append(out, lines[i])
			continue
		}
		if i+1 < len(lines) && lines[i+1] == "\n" {
			i++
		}
	}
	return strings.Join(out, "")
}

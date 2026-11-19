package audit

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
)

// TraceIDHeader is the §8.1 request header that carries a trace ID outside
// W3C trace context. The MCP server sends it on a meta-tool call that has no
// span context, and the registry reads it when the request carries no valid
// traceparent.
//
// Spec: §8.1
const TraceIDHeader = "X-Podium-Trace-Id"

// zeroTraceID is the all-zero trace ID, which W3C Trace Context reserves as
// invalid.
const zeroTraceID = "00000000000000000000000000000000"

// ValidTraceID returns s in lowercase when it is 32 hexadecimal characters
// that are not all zeros, and "" otherwise. It is the check for both the
// trace-id field of a traceparent header and an X-Podium-Trace-Id value.
//
// Spec: §8.1
func ValidTraceID(s string) string {
	if len(s) != 32 {
		return ""
	}
	s = strings.ToLower(s)
	if _, err := hex.DecodeString(s); err != nil || s == zeroTraceID {
		return ""
	}
	return s
}

// NewTraceID returns a random 32-hex trace ID, or "" when the random source
// fails.
//
// Spec: §8.1
func NewTraceID() string {
	return newTraceIDFrom(rand.Reader)
}

// newTraceIDFrom is NewTraceID with an injectable random source. A draw that
// is all zeros, which W3C Trace Context reserves as invalid, yields "".
func newTraceIDFrom(random io.Reader) string {
	var buf [16]byte
	if _, err := io.ReadFull(random, buf[:]); err != nil {
		return ""
	}
	return ValidTraceID(hex.EncodeToString(buf[:]))
}

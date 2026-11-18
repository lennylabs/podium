package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
)

// OperatorTokenHeader carries PODIUM_OPERATOR_TOKEN on a re-embed or overlap
// rebuild request to a public-mode registry (§13.10).
const OperatorTokenHeader = "X-Podium-Operator-Token"

// MinOperatorTokenLength is the shortest PODIUM_OPERATOR_TOKEN the registry
// accepts (§13.12).
const MinOperatorTokenLength = 32

// WithOperatorToken sets the PODIUM_OPERATOR_TOKEN that admits a re-embed or
// overlap rebuild request in public mode (§4.7, §7.3.1). An empty token
// leaves both endpoints refusing every request in public mode.
func WithOperatorToken(token string) Option {
	return func(s *Server) { s.operatorToken = token }
}

// CheckOperatorToken reports whether r carries token in the operator token
// header, compared in constant time, and returns an error naming the remedy
// otherwise. An empty token admits no request, so a public-mode registry
// without PODIUM_OPERATOR_TOKEN refuses every request this gates. The error
// names the variable and the header and never the expected value.
//
// Spec: §7.3.1 (Operator token in public mode), §13.10
func CheckOperatorToken(r *http.Request, token string) error {
	if token == "" {
		return errors.New("this registry runs in public mode and has no PODIUM_OPERATOR_TOKEN, so it refuses this request")
	}
	got := r.Header.Get(OperatorTokenHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return errors.New("this registry runs in public mode and requires PODIUM_OPERATOR_TOKEN in the " + OperatorTokenHeader + " header")
	}
	return nil
}

// authorizeOperatorPass decides whether a request may start a re-embed or
// overlap rebuild pass, both of which query the embedding provider and the
// search index for every artifact. In public mode the registry authenticates
// no caller and can be reachable beyond the local machine, so a pass needs
// the operator token, compared in constant time, and is refused while none is
// configured. A registry that authenticates no caller for want of an identity
// provider admits the pass, because no caller can hold the admin role there
// and its default bind is loopback. Any other registry requires the admin
// role, and an authorization it cannot evaluate denies.
//
// Spec: §4.7, §7.3.1, §13.10
func (s *Server) authorizeOperatorPass(r *http.Request) error {
	if s.publicMode {
		return CheckOperatorToken(r, s.operatorToken)
	}
	if s.unauthenticatedReembed {
		return nil
	}
	return s.requireAdmin(r)
}

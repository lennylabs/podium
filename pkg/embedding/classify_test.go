package embedding

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status     int
		wantErrIs  error
		wantSubstr string
	}{
		{http.StatusUnauthorized, ErrAuth, "401"},
		{http.StatusForbidden, ErrAuth, "403"},
		{http.StatusTooManyRequests, ErrQuota, "429"},
		{http.StatusServiceUnavailable, ErrUnreachable, "503"},
		{http.StatusInternalServerError, ErrUnreachable, "500"},
		{http.StatusBadRequest, nil, "400"},
	}
	for _, c := range cases {
		err := classify(c.status, "body hint")
		if err == nil {
			t.Errorf("status %d: nil error", c.status)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSubstr) {
			t.Errorf("status %d: %v missing %q", c.status, err, c.wantSubstr)
		}
		if c.wantErrIs != nil && !errors.Is(err, c.wantErrIs) {
			t.Errorf("status %d: not wrapping %v: %v", c.status, c.wantErrIs, err)
		}
	}
}

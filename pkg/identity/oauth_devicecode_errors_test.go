package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

func TestInitiate_MissingURLErrors(t *testing.T) {
	t.Parallel()
	_, err := (identity.DeviceCodeFlow{}).Initiate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DeviceAuthURL") {
		t.Errorf("err = %v", err)
	}
}

func TestInitiate_HTTPErrorBubblesUp(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL,
		ClientID:      "x",
		Client:        srv.Client(),
	}
	_, err := flow.Initiate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("err = %v", err)
	}
}

func TestInitiate_MalformedResponseErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Missing device_code field
		_, _ = w.Write([]byte(`{"verification_uri":"https://x"}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: srv.URL,
		ClientID:      "x",
		Client:        srv.Client(),
	}
	_, err := flow.Initiate(context.Background())
	if err == nil {
		t.Errorf("expected error for malformed response")
	}
}

func TestPollOnce_MissingTokenURLErrors(t *testing.T) {
	t.Parallel()
	_, err := (identity.DeviceCodeFlow{}).PollOnce(context.Background(),
		&identity.DeviceAuth{DeviceCode: "x"})
	if err == nil || !strings.Contains(err.Error(), "TokenURL") {
		t.Errorf("err = %v", err)
	}
}

func TestPollOnce_AccessDenied(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		TokenURL: srv.URL,
		ClientID: "x",
		Client:   srv.Client(),
	}
	_, err := flow.PollOnce(context.Background(), &identity.DeviceAuth{DeviceCode: "dc"})
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestPollOnce_AuthorizationPending(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		TokenURL: srv.URL,
		ClientID: "x",
		Client:   srv.Client(),
	}
	_, err := flow.PollOnce(context.Background(), &identity.DeviceAuth{DeviceCode: "dc"})
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestPollOnce_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"xyz","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		TokenURL:     srv.URL,
		ClientID:     "x",
		ClientSecret: "s",
		Client:       srv.Client(),
	}
	tokens, err := flow.PollOnce(context.Background(), &identity.DeviceAuth{DeviceCode: "dc"})
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if tokens.AccessToken != "xyz" {
		t.Errorf("AccessToken = %q", tokens.AccessToken)
	}
}

func TestPollOnce_MissingAccessTokenInOKResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	flow := identity.DeviceCodeFlow{
		TokenURL: srv.URL,
		Client:   srv.Client(),
	}
	_, err := flow.PollOnce(context.Background(), &identity.DeviceAuth{DeviceCode: "dc"})
	if err == nil || !strings.Contains(err.Error(), "missing access_token") {
		t.Errorf("err = %v", err)
	}
}

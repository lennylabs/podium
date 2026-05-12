package notification_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/notification"
)

func TestProviderIDs(t *testing.T) {
	t.Parallel()
	cases := map[string]notification.Provider{
		"noop":    notification.Noop{},
		"log":     notification.LogProvider{},
		"webhook": notification.Webhook{},
		"multi":   notification.MultiProvider{},
	}
	for want, p := range cases {
		if got := p.ID(); got != want {
			t.Errorf("%T.ID() = %q, want %q", p, got, want)
		}
	}
}

func TestLogProvider_WritesSingleLineToStdLog(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	n := notification.Notification{
		Severity: notification.SeverityWarning,
		Title:    "embedding-outage",
		Body:     "voyage timed out 3 times in 5min",
		Tags:     map[string]string{"region": "us-east-1", "provider": "voyage"},
	}
	if err := (notification.LogProvider{}).Notify(context.Background(), n); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	line := buf.String()
	for _, want := range []string{
		"[notify warning]",
		"embedding-outage",
		"voyage timed out 3 times in 5min",
		"provider=voyage",
		"region=us-east-1",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log output missing %q\n--- got ---\n%s", want, line)
		}
	}
}

func TestLogProvider_NoTagsOmitsTrailingSpace(t *testing.T) {
	var buf bytes.Buffer
	origOut, origFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()
	_ = (notification.LogProvider{}).Notify(context.Background(),
		notification.Notification{Severity: notification.SeverityInfo, Title: "x", Body: "y"})
	if strings.Contains(buf.String(), "  ") {
		t.Errorf("double space in %q", buf.String())
	}
}

func TestWebhook_EmptyURLErrors(t *testing.T) {
	t.Parallel()
	err := notification.Webhook{}.Notify(context.Background(), notification.Notification{})
	if err == nil || !strings.Contains(err.Error(), "URL required") {
		t.Errorf("err = %v, want URL-required error", err)
	}
}

func TestWebhook_CustomClientIsUsed(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	wh := notification.Webhook{URL: ts.URL, Client: ts.Client()}
	if err := wh.Notify(context.Background(), notification.Notification{Title: "t"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d", hits)
	}
}

func TestMultiProvider_ReturnsFirstError(t *testing.T) {
	bad := stubProvider{err: errors.New("boom")}
	good := stubProvider{}
	multi := notification.MultiProvider{Providers: []notification.Provider{good, bad, good}}
	err := multi.Notify(context.Background(), notification.Notification{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want surfaced boom error", err)
	}
}

func TestMultiProvider_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	multi := notification.MultiProvider{}
	if err := multi.Notify(context.Background(), notification.Notification{}); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

type stubProvider struct {
	err error
}

func (stubProvider) ID() string { return "stub" }
func (s stubProvider) Notify(context.Context, notification.Notification) error {
	return s.err
}

package notification

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

// captured records one intercepted SendMail call.
type captured struct {
	addr string
	from string
	to   []string
	msg  string
}

func withCapture(s *SMTP) *captured {
	c := &captured{}
	s.send = func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
		c.addr, c.from, c.to, c.msg = addr, from, to, string(msg)
		return nil
	}
	return c
}

// Spec: §9.1 — the email NotificationProvider sends to a Notification's
// own Recipients and renders the severity, title, and body into the mail.
func TestSMTP_NotifyUsesRecipientsAndRendersMessage(t *testing.T) {
	s := SMTP{Host: "smtp.acme.com", From: "podium@acme.com", To: []string{"ops@acme.com"}}
	cap := withCapture(&s)
	err := s.Notify(context.Background(), Notification{
		Severity:   SeverityError,
		Title:      "ingest-failed",
		Body:       "git fetch failed for layer team-shared",
		Recipients: []string{"alice@acme.com", "bob@acme.com"},
		Time:       time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if cap.addr != "smtp.acme.com:587" {
		t.Errorf("addr = %q, want smtp.acme.com:587 (default submission port)", cap.addr)
	}
	if cap.from != "podium@acme.com" {
		t.Errorf("from = %q", cap.from)
	}
	// Per-notification Recipients override the configured To list.
	if strings.Join(cap.to, ",") != "alice@acme.com,bob@acme.com" {
		t.Errorf("to = %v, want notification recipients", cap.to)
	}
	if !strings.Contains(cap.msg, "Subject: [podium error] ingest-failed") {
		t.Errorf("missing severity-prefixed subject in message:\n%s", cap.msg)
	}
	if !strings.Contains(cap.msg, "git fetch failed for layer team-shared") {
		t.Errorf("missing body in message:\n%s", cap.msg)
	}
}

// Spec: §9.1 — when a Notification carries no Recipients, the email
// provider falls back to its configured To list.
func TestSMTP_FallsBackToConfiguredRecipients(t *testing.T) {
	s := SMTP{Host: "relay", Port: 25, From: "p@acme.com", To: []string{"team@acme.com"}}
	cap := withCapture(&s)
	if err := s.Notify(context.Background(), Notification{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if cap.addr != "relay:25" {
		t.Errorf("addr = %q, want relay:25", cap.addr)
	}
	if strings.Join(cap.to, ",") != "team@acme.com" {
		t.Errorf("to = %v, want configured To", cap.to)
	}
}

// Spec: §9.1 — the email provider reports an error rather than silently
// dropping a notification when no recipient can be resolved, or when the
// host / sender is unset.
func TestSMTP_ErrorsOnMissingFields(t *testing.T) {
	cases := []struct {
		name string
		s    SMTP
	}{
		{"no host", SMTP{From: "p@acme.com", To: []string{"x@acme.com"}}},
		{"no from", SMTP{Host: "h", To: []string{"x@acme.com"}}},
		{"no recipients", SMTP{Host: "h", From: "p@acme.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.s.Notify(context.Background(), Notification{Title: "t"}); err == nil {
				t.Errorf("Notify(%s) = nil, want error", c.name)
			}
		})
	}
}

// Spec: §9.1 — the email provider identifies as "email".
func TestSMTP_ID(t *testing.T) {
	if id := (SMTP{}).ID(); id != "email" {
		t.Errorf("ID() = %q, want email", id)
	}
}

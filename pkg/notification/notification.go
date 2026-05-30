// Package notification implements the §9 NotificationProvider SPI.
// Providers deliver operational notifications (ingest failure,
// embedding-provider outage past N minutes, transparency-anchor
// failure, layer auto-disable on force-push) to a fanout target
// configured by the operator via PODIUM_NOTIFICATION_PROVIDER.
//
// This is distinct from the §7.3.2 outbound webhook stream, which
// carries change events for downstream consumers (manifest
// upserted, dependents changed, etc.). NotificationProvider is for
// operators; webhooks are for tooling.
package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Severity classifies a Notification.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Notification is one operational message.
type Notification struct {
	Severity   Severity          `json:"severity"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Recipients []string          `json:"recipients,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Time       time.Time         `json:"time"`
}

// Provider is the SPI an implementation satisfies.
type Provider interface {
	ID() string
	Notify(ctx context.Context, n Notification) error
}

// Noop discards notifications. Default when no provider is wired.
type Noop struct{}

// ID returns "noop".
func (Noop) ID() string { return "noop" }

// Notify discards n.
func (Noop) Notify(context.Context, Notification) error { return nil }

// LogProvider writes each notification to log.Printf. Useful for
// standalone deployments where stdout/stderr is the only sink.
type LogProvider struct{}

// ID returns "log".
func (LogProvider) ID() string { return "log" }

// Notify writes a single-line representation.
func (LogProvider) Notify(_ context.Context, n Notification) error {
	tags := ""
	if len(n.Tags) > 0 {
		keys := make([]string, 0, len(n.Tags))
		for k := range n.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := bytes.Buffer{}
		for _, k := range keys {
			fmt.Fprintf(&buf, " %s=%s", k, n.Tags[k])
		}
		tags = buf.String()
	}
	log.Printf("[notify %s] %s — %s%s", n.Severity, n.Title, n.Body, tags)
	return nil
}

// Webhook posts a JSON payload to URL with optional HMAC-SHA256
// signature. The signature mirrors the §7.3.2 outbound webhook
// envelope so receivers can reuse verification code.
type Webhook struct {
	URL    string
	Secret string // optional HMAC key
	Client *http.Client
}

// ID returns "webhook".
func (Webhook) ID() string { return "webhook" }

// Notify POSTs n to URL with Content-Type application/json. When
// Secret is set, an X-Podium-Signature header carries the
// hex-encoded HMAC-SHA256 of the body.
func (w Webhook) Notify(ctx context.Context, n Notification) error {
	if w.URL == "" {
		return errors.New("notification: webhook URL required")
	}
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.Secret))
		mac.Write(body)
		req.Header.Set("X-Podium-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	c := w.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("notification: webhook returned %d", resp.StatusCode)
	}
	return nil
}

// SMTP delivers notifications as email over SMTP, the email half of
// the §9.1 NotificationProvider "Email + webhook" default. Each
// Notification is sent to its own Recipients list when present,
// falling back to the provider's configured To addresses. The
// message body carries the severity, title, and body as an RFC 5322
// text/plain mail.
type SMTP struct {
	// Host and Port name the SMTP relay (for example "smtp.acme.com",
	// 587). Port 0 defaults to 587 (submission).
	Host string
	Port int
	// From is the envelope and header sender address.
	From string
	// To is the default recipient list used when a Notification
	// carries no Recipients of its own.
	To []string
	// Username and Password authenticate via PLAIN auth when both are
	// set. Left empty for relays that accept unauthenticated
	// submission from the registry host.
	Username string
	Password string

	// send is the injection seam for tests; nil uses smtp.SendMail.
	send func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// ID returns "email".
func (SMTP) ID() string { return "email" }

// Notify renders n as an email and sends it to the resolved
// recipient list. Returns an error when no recipient can be
// determined or when the relay rejects the message.
func (s SMTP) Notify(_ context.Context, n Notification) error {
	if s.Host == "" {
		return errors.New("notification: smtp host required")
	}
	if s.From == "" {
		return errors.New("notification: smtp from address required")
	}
	to := n.Recipients
	if len(to) == 0 {
		to = s.To
	}
	if len(to) == 0 {
		return errors.New("notification: smtp has no recipients (set To or Notification.Recipients)")
	}
	port := s.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", s.Host, port)
	var auth smtp.Auth
	if s.Username != "" && s.Password != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	send := s.send
	if send == nil {
		send = smtp.SendMail
	}
	return send(addr, auth, s.From, to, s.message(to, n))
}

// message builds the RFC 5322 representation of n addressed to the
// resolved recipient list. The subject prefixes the severity so an
// operator can filter on it; the body repeats the structured fields.
func (s SMTP) message(to []string, n Notification) []byte {
	subject := n.Title
	if n.Severity != "" {
		subject = fmt.Sprintf("[podium %s] %s", n.Severity, n.Title)
	}
	when := n.Time
	if when.IsZero() {
		when = time.Now()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", when.UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(n.Body)
	if len(n.Tags) > 0 {
		keys := make([]string, 0, len(n.Tags))
		for k := range n.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\r\n\r\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\r\n", k, n.Tags[k])
		}
	}
	return []byte(b.String())
}

// MultiProvider fans a Notify out to every wrapped provider. A
// failure from any single provider is logged and other providers
// still receive the notification.
type MultiProvider struct {
	Providers []Provider
}

// ID returns "multi".
func (MultiProvider) ID() string { return "multi" }

// Notify fans out to every provider concurrently. Returns the
// first error observed; all providers complete regardless.
func (m MultiProvider) Notify(ctx context.Context, n Notification) error {
	if len(m.Providers) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, len(m.Providers))
	for i, p := range m.Providers {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			errs[i] = p.Notify(ctx, n)
		}(i, p)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

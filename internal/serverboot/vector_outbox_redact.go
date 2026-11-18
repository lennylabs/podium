package serverboot

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// redactedText replaces a configured secret value in outbox error text.
const redactedText = "[redacted]"

var (
	// outboxURLUserinfoRE matches the userinfo of a URL inside error text, up
	// to the last '@' before a '/', whitespace, or a quote. net/http quotes
	// the request URL in a transport error, and a backend can echo a URL in
	// its response body.
	outboxURLUserinfoRE = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s"']*@`)
	// outboxURLQueryRE matches the query string of a URL inside error text,
	// which can carry an API key or a token (?api_key=..., ?sslpassword=...).
	outboxURLQueryRE = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^\s"'?]*)\?[^\s"']*`)
	// outboxPasswordRE matches a password=value pair of a key/value
	// connection string, the lib/pq DSN format.
	outboxPasswordRE = regexp.MustCompile(`(?i)(password\s*=\s*)('[^']*'|[^\s"']+)`)
)

// outboxRedactor removes credentials from the text of a failed §4.7 outbox
// drain before the worker stores it as the row's last error or logs it. The
// error comes from the embedder or the vector backend, and either can quote
// configuration: net/http quotes the request URL, a backend can echo the
// request in its response body, and a custom backend registered through
// vector.Default receives every vector setting, the pgvector DSN (which
// falls back to PODIUM_POSTGRES_DSN) included, and can wrap it in an error.
// A configured value is therefore removed by value, whatever path brought it
// into the text, and URL userinfo, URL query strings, and connection-string
// passwords are removed by pattern, which covers a credential the registry
// was not configured with.
//
// Spec: §4.7
type outboxRedactor struct {
	// secrets holds the configured secret values, longest first, so a value
	// that contains another is removed whole.
	secrets []string
}

// newOutboxRedactor returns a redactor for the given configured secret
// values. An empty value is skipped. A value that parses as a URL with a
// password, or that carries a password=value pair, also contributes that
// password, so a DSN quoted only in part is still covered.
func newOutboxRedactor(values ...string) outboxRedactor {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	for _, v := range values {
		add(v)
		for _, pw := range dsnPasswords(v) {
			add(pw)
		}
	}
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return outboxRedactor{secrets: secrets}
}

// dsnPasswords returns the passwords a connection string carries: the URL
// userinfo password, the password query parameter, and each password=value
// pair of the key/value form.
func dsnPasswords(v string) []string {
	var out []string
	if u, err := url.Parse(v); err == nil && u.Scheme != "" {
		if pw, ok := u.User.Password(); ok {
			out = append(out, pw)
		}
		if pw := u.Query().Get("password"); pw != "" {
			out = append(out, pw)
		}
	}
	for _, m := range outboxPasswordRE.FindAllStringSubmatch(v, -1) {
		out = append(out, strings.Trim(m[2], "'"))
	}
	return out
}

// text returns err's text with every configured secret value, URL userinfo,
// URL query string, and connection-string password removed.
//
// Spec: §4.7
func (r outboxRedactor) text(err error) string {
	s := err.Error()
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, redactedText)
	}
	s = outboxURLUserinfoRE.ReplaceAllString(s, "$1")
	s = outboxURLQueryRE.ReplaceAllString(s, "$1")
	return outboxPasswordRE.ReplaceAllString(s, "${1}"+redactedText)
}

// outboxSecrets returns every configured secret value an outbox drain error
// could quote: the metadata-store and pgvector DSNs, the embedding provider
// and vector backend API keys, the object-store secret key, the trusted-proxy
// secret, and the web UI's OAuth client secret.
func (c *Config) outboxSecrets() []string {
	return []string{
		c.postgresDSN, c.pgvectorDSN,
		c.openaiAPIKey, c.voyageAPIKey, c.cohereAPIKey,
		c.pineconeKey, c.weaviateKey, c.qdrantKey,
		c.s3SecretKey, c.trustedProxySecret, c.webUIOAuthClientSecret,
	}
}

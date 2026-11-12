package audit

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

// otlpURL turns an httptest server URL into an OTLP destination element.
func otlpURL(serverURL, path string) string {
	return "otlp+" + serverURL + path
}

// attrMap returns the attributes of a log record or a resource by key.
func attrMap(kvs []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	out := map[string]*commonpb.AnyValue{}
	for _, kv := range kvs {
		out[kv.GetKey()] = kv.GetValue()
	}
	return out
}

// stringValue returns v's string value, failing the test when v is not a
// StringValue.
func stringValue(t *testing.T, key string, v *commonpb.AnyValue) string {
	t.Helper()
	s, ok := v.GetValue().(*commonpb.AnyValue_StringValue)
	if !ok {
		t.Fatalf("attribute %s = %v, want a StringValue", key, v)
	}
	return s.StringValue
}

// exportRecords returns the resource, the scope, and the log records of a
// request that carries one resource and one scope.
func exportRecords(t *testing.T, req *collogspb.ExportLogsServiceRequest) (map[string]*commonpb.AnyValue, *commonpb.InstrumentationScope, []*logspb.LogRecord) {
	t.Helper()
	if req == nil || len(req.GetResourceLogs()) != 1 || len(req.GetResourceLogs()[0].GetScopeLogs()) != 1 {
		t.Fatalf("export request %v does not carry one resource and one scope", req)
	}
	rl := req.GetResourceLogs()[0]
	sl := rl.GetScopeLogs()[0]
	return attrMap(rl.GetResource().GetAttributes()), sl.GetScope(), sl.GetLogRecords()
}

// Each record maps to one log record as the §8.3 OTLP mapping states. The
// body is the file destination's line byte for byte, and it re-parses and
// re-hashes to the record's hash.
//
// Spec: §8.3
func TestOTLPDest_Mapping(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, nil)
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openStream(t, path+","+otlpURL(rcv.srv.URL, ""), OpenOptions{Service: "podium-registry"})

	ts := time.Date(2026, 11, 12, 9, 0, 0, 123456789, time.UTC)
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	events := []Event{
		{Type: EventArtifactLoaded, Timestamp: ts, TraceID: traceID, Tenant: "acme", Caller: "alice", CallerEmail: "alice@acme.com", Target: "finance/close"},
		{Type: EventDomainLoaded, Timestamp: ts.Add(time.Second), TraceID: traceID[:31], Caller: "bob"},
	}
	for _, e := range events {
		if err := s.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests, want 1", len(reqs))
	}
	resource, scope, records := exportRecords(t, reqs[0].export)
	if got := stringValue(t, "service.name", resource["service.name"]); got != "podium-registry" {
		t.Errorf("service.name = %q", got)
	}
	instance := stringValue(t, "service.instance.id", resource["service.instance.id"])
	if id, err := uuid.Parse(instance); err != nil || id.Version() != 4 || instance != s.instanceID {
		t.Errorf("service.instance.id = %q, want the stream's version 4 UUID %q", instance, s.instanceID)
	}
	if len(resource) != 2 {
		t.Errorf("resource attributes = %v, want service.name and service.instance.id", resource)
	}
	if scope.GetName() != "github.com/lennylabs/podium/audit" {
		t.Errorf("scope name = %q", scope.GetName())
	}
	fileLines := splitLines(mustRead(t, path))
	if len(records) != 2 || len(fileLines) < 2 {
		t.Fatalf("got %d log records and %d file lines, want 2 of each", len(records), len(fileLines))
	}
	wantTrace := [][]byte{mustHex(t, traceID), nil}
	for i, lr := range records {
		assertLogRecord(t, i, lr, events[i], fileLines[i], wantTrace[i])
	}
}

// assertLogRecord checks one log record against the event it carries and
// the file destination's line for it.
func assertLogRecord(t *testing.T, i int, lr *logspb.LogRecord, e Event, fileLine []byte, wantTrace []byte) {
	t.Helper()
	ns := uint64(e.Timestamp.UnixNano())
	if lr.GetTimeUnixNano() != ns || lr.GetObservedTimeUnixNano() != ns {
		t.Errorf("record %d times = %d, %d, want %d", i, lr.GetTimeUnixNano(), lr.GetObservedTimeUnixNano(), ns)
	}
	if lr.GetSeverityNumber() != 9 || lr.GetSeverityText() != "INFO" {
		t.Errorf("record %d severity = %d %q, want 9 INFO", i, lr.GetSeverityNumber(), lr.GetSeverityText())
	}
	if lr.GetEventName() != string(e.Type) {
		t.Errorf("record %d event_name = %q, want %q", i, lr.GetEventName(), e.Type)
	}
	if !bytes.Equal(lr.GetTraceId(), wantTrace) {
		t.Errorf("record %d trace_id = %x, want %x", i, lr.GetTraceId(), wantTrace)
	}
	body := lr.GetBody().GetStringValue()
	if body != string(fileLine) {
		t.Errorf("record %d body differs from the file line:\n%s\n%s", i, body, fileLine)
	}
	je, err := decodeNDJSON([]byte(body))
	if err != nil || len(je) != 1 {
		t.Fatalf("record %d body does not re-parse: %v", i, err)
	}
	if rehash := chainEvent(eventFromJSON(je[0]), je[0].PrevHash).Hash; rehash != je[0].Hash {
		t.Errorf("record %d body re-hashes to %s, want %s", i, rehash, je[0].Hash)
	}
	attrs := attrMap(lr.GetAttributes())
	if v, ok := attrs[attrSchemaVersion].GetValue().(*commonpb.AnyValue_IntValue); !ok || v.IntValue != 1 {
		t.Errorf("record %d %s = %v, want IntValue 1", i, attrSchemaVersion, attrs[attrSchemaVersion])
	}
	want := map[string]string{attrHash: je[0].Hash}
	if je[0].PrevHash != "" {
		want[attrPrevHash] = je[0].PrevHash
	}
	if e.Tenant != "" {
		want[attrTenant] = e.Tenant
	}
	if len(attrs) != len(want)+1 {
		t.Errorf("record %d attributes = %v, want %s and %v", i, attrs, attrSchemaVersion, want)
	}
	for key, value := range want {
		if got := stringValue(t, key, attrs[key]); got != value {
			t.Errorf("record %d %s = %q, want %q", i, key, got, value)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A tenant or type that is not valid UTF-8 reaches the attributes as the
// body carries it, each invalid byte replaced by U+FFFD, so the request
// still marshals.
//
// Spec: §8.3
func TestOTLPDest_InvalidUTF8MatchesBody(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, nil)
	s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{})
	if err := s.Append(context.Background(), Event{Type: "x\xffy", Tenant: "acme\xfe\xff"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests, want 1", len(reqs))
	}
	_, _, records := exportRecords(t, reqs[0].export)
	line := reqs[0].lines[0]
	tenant := stringValue(t, attrTenant, attrMap(records[0].GetAttributes())[attrTenant])
	if tenant != "acme��" || tenant != line.Tenant {
		t.Errorf("tenant attribute %q, body tenant %q, want both acme\\uFFFD\\uFFFD", tenant, line.Tenant)
	}
	if name := records[0].GetEventName(); name != "x�y" || name != line.Type {
		t.Errorf("event_name %q, body type %q, want both x\\uFFFDy", name, line.Type)
	}
}

// With an empty path or "/", the destination posts to exactly /v1/logs.
//
// Spec: §8.3
func TestOTLPDest_DefaultPath(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "/"} {
		rcv := newReceiver(t, nil)
		s := openStream(t, otlpURL(rcv.srv.URL, path), OpenOptions{})
		appendN(t, s, 1)
		if err := s.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		reqs := rcv.requests()
		if len(reqs) != 1 || reqs[0].path != "/v1/logs" {
			t.Fatalf("path %q: receiver requests %+v, want one at /v1/logs", path, reqs)
		}
	}
}

// A configured path is used as it stands: nothing replaces it with or
// appends /v1/logs.
//
// Spec: §8.3
func TestOTLPDest_ExplicitPathKept(t *testing.T) {
	t.Parallel()
	rcv := newReceiver(t, nil)
	s := openStream(t, otlpURL(rcv.srv.URL, "/collector/v1/logs"), OpenOptions{})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 || reqs[0].path != "/collector/v1/logs" {
		t.Fatalf("receiver requests %+v, want one at /collector/v1/logs", reqs)
	}
}

// otlpTarget strips the otlp+ prefix, keeps the query, and sets the default
// path only on an empty or "/" path.
//
// Spec: §8.3
func TestOTLPTarget(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"otlp+https://c:4318":           "https://c:4318/v1/logs",
		"otlp+http://c/":                "http://c/v1/logs",
		"otlp+https://c/x/y?tenant=t1":  "https://c/x/y?tenant=t1",
		"otlp+http://c/v1/logs/":        "http://c/v1/logs/",
		"otlp+https://c/a%2Fb/v1/logs":  "https://c/a%2Fb/v1/logs",
		"otlp+https://c?tenant=t1":      "https://c/v1/logs?tenant=t1",
		"OTLP+HTTPS://c:4318/v1/logs":   "https://c:4318/v1/logs",
		"otlp+http://[::1]:4318/v1/log": "http://[::1]:4318/v1/log",
	}
	for in, want := range cases {
		specs, err := parseDestinations(in, OpenOptions{})
		if err != nil {
			t.Fatalf("parseDestinations(%q): %v", in, err)
		}
		if got := otlpTarget(specs[0].url); got != want {
			t.Errorf("otlpTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// A trace_id decodes only when it is 32 hexadecimal characters.
//
// Spec: §8.3
func TestOTLPTraceID(t *testing.T) {
	t.Parallel()
	valid := "4BF92F3577B34DA6A3CE929D0E0E4736"
	cases := map[string][]byte{
		valid:                    mustHex(t, valid),
		strings.ToLower(valid):   mustHex(t, valid),
		valid[:31]:               nil,
		valid + "0":              nil,
		"":                       nil,
		strings.Repeat("zz", 16): nil,
	}
	for in, want := range cases {
		if got := otlpTraceID(in); !bytes.Equal(got, want) {
			t.Errorf("otlpTraceID(%q) = %x, want %x", in, got, want)
		}
	}
}

// A 2xx partial-success response counts its rejected_log_records as drops
// with reason rejected, never more than the batch held. A 2xx body that
// does not decode delivers the batch and counts nothing.
//
// Spec: §8.3
func TestOTLPDest_PartialSuccessCountsRejected(t *testing.T) {
	t.Parallel()
	respond := func(body []byte) func(int, http.ResponseWriter, *http.Request) {
		return func(_ int, w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", otlpContentType)
			_, _ = w.Write(body)
		}
	}
	partial := func(n int64) []byte { return partialSuccess(t, n, "schema") }
	cases := []struct {
		name string
		body []byte
		want int
	}{
		{"two rejected", partial(2), 2},
		{"over-reported", partial(10), 3},
		{"none rejected", partial(0), 0},
		{"empty body", nil, 0},
		{"undecodable body", []byte("not protobuf \xff\xff\xff"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rcv := newReceiver(t, respond(tc.body))
			obs := &fakeObserver{}
			s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{Observer: obs})
			appendN(t, s, 3)
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}
			queued, dropped := totals(obs)
			want := map[string]int{}
			if tc.want > 0 {
				want["otlp,rejected"] = tc.want
			}
			if len(dropped) != len(want) || dropped["otlp,rejected"] != want["otlp,rejected"] {
				t.Fatalf("dropped %v, want %v", dropped, want)
			}
			if queued["otlp"] != 0 || rcv.count() != 1 {
				t.Fatalf("queue depth %d after %d requests, want 0 after 1", queued["otlp"], rcv.count())
			}
		})
	}
}

// partialSuccess marshals a partial-success response that reports n
// rejected records with message as its error message.
func partialSuccess(t *testing.T, n int64, message string) []byte {
	t.Helper()
	b, err := proto.Marshal(&collogspb.ExportLogsServiceResponse{
		PartialSuccess: &collogspb.ExportLogsPartialSuccess{RejectedLogRecords: n, ErrorMessage: message},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A 2xx response whose body cannot be read in full may hide a
// partial-success count, so the batch is retried rather than counted as
// delivered. The first answer reports two rejections in a body that is
// either longer than the 64 KiB read bound or cut short by the connection
// closing; the retry's complete answer reports one, and only that one is
// counted.
//
// Spec: §8.3
func TestOTLPDest_IncompleteSuccessBodyRetries(t *testing.T) {
	cases := []struct {
		name  string
		msg   string
		first func(w http.ResponseWriter, body []byte)
	}{
		{
			name: "body over the read bound",
			msg:  strings.Repeat("m", maxResponseBody+1),
			first: func(w http.ResponseWriter, body []byte) {
				_, _ = w.Write(body)
			},
		},
		{
			name: "body cut short",
			msg:  "schema",
			first: func(w http.ResponseWriter, body []byte) {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
				_, _ = w.Write(body)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubTicker(t)
			gate := stubSleepGate(t, false)
			first := partialSuccess(t, 2, tc.msg)
			retry := partialSuccess(t, 1, "schema")
			rcv := newReceiver(t, func(n int, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", otlpContentType)
				if n == 1 {
					tc.first(w, first)
					return
				}
				_, _ = w.Write(retry)
			})
			obs := &fakeObserver{}
			s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{Observer: obs})
			appendN(t, s, 3)
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}
			queued, dropped := totals(obs)
			if len(dropped) != 1 || dropped["otlp,rejected"] != 1 {
				t.Fatalf("dropped %v, want one otlp rejected", dropped)
			}
			if queued["otlp"] != 0 || rcv.count() != 2 || len(gate.recorded()) != 1 {
				t.Fatalf("queue depth %d after %d requests and %d backoffs, want 0 after 2 and 1",
					queued["otlp"], rcv.count(), len(gate.recorded()))
			}
			if hs := rcv.hashes(); len(hs) != 3 {
				t.Fatalf("receiver holds %d distinct records, want 3", len(hs))
			}
		})
	}
}

// A complete 2xx body that is not an ExportLogsServiceResponse delivers
// the batch, and the destination logs it once rather than once per batch.
//
// Spec: §8.3
func TestOTLPDest_UndecodableBodyLogsOnce(t *testing.T) {
	logs := captureLog(t)
	fire := stubTicker(t)
	rcv := newReceiver(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not protobuf \xff\xff\xff"))
	})
	obs := &fakeObserver{}
	s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{Observer: obs})
	for i := 1; i <= 2; i++ {
		appendN(t, s, 1)
		fire()
		rcv.wait(t, i)
		waitFor(t, func() bool { q, _ := totals(obs); return q["otlp"] == 0 })
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, dropped := totals(obs); len(dropped) != 0 {
		t.Fatalf("dropped %v, want none", dropped)
	}
	if n := strings.Count(logs.String(), "is not an ExportLogsServiceResponse"); n != 1 {
		t.Fatalf("logged the undecodable body %d times, want once:\n%s", n, logs.String())
	}
}

// The OTLP destination reads no OTEL_EXPORTER_OTLP_* variable, so a second
// receiver those variables name gets no request.
//
// Spec: §8.3
func TestOTLPDest_IgnoresOTELEnv(t *testing.T) {
	other := newReceiver(t, nil)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", other.srv.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", other.srv.URL+"/v1/logs")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "X-Other=1")
	rcv := newReceiver(t, nil)
	s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rcv.count() != 1 || other.count() != 0 {
		t.Fatalf("configured receiver got %d requests and the OTEL_* receiver %d, want 1 and 0", rcv.count(), other.count())
	}
	if h := rcv.requests()[0].header.Get("X-Other"); h != "" {
		t.Fatalf("the request carries OTEL_EXPORTER_OTLP_HEADERS: X-Other=%q", h)
	}
}

// An otlp+https receiver that only the CA file trusts and that requires a
// client certificate receives the headers file's headers and the protobuf
// content type at /v1/logs. Without the client certificate, or against a
// receiver certificate the client does not trust, the receiver holds no
// request and the record is counted as shutdown.
//
// Spec: §8.3, §13.12
func TestOTLPDest_HeadersAndTLS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issue(t)
	caFile := writeFixture(t, dir, "ca.pem", ca.pem)
	certFile := writeFixture(t, dir, "client.crt", certPEM)
	keyFile := writeFixture(t, dir, "client.key", keyPEM)
	headersFile := writeFixture(t, dir, "headers", []byte("Authorization: Bearer zq7k\n"))

	mtls := ca.serverTLS(t)
	mtls.ClientAuth = tls.RequireAndVerifyClientCert
	mtls.ClientCAs = ca.pool()
	rcv := newTLSReceiver(t, mtls, nil)

	s := openStream(t, otlpURL(rcv.srv.URL, ""), OpenOptions{CAFile: caFile, CertFile: certFile, KeyFile: keyFile, HeadersFile: headersFile})
	appendN(t, s, 1)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close with the client certificate: %v", err)
	}
	reqs := rcv.requests()
	if len(reqs) != 1 {
		t.Fatalf("receiver got %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.path != "/v1/logs" || r.header.Get("Authorization") != "Bearer zq7k" || r.header.Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("request at %q with Authorization %q and Content-Type %q", r.path, r.header.Get("Authorization"), r.header.Get("Content-Type"))
	}
	_, _, records := exportRecords(t, r.export)
	if got := stringValue(t, attrHash, attrMap(records[0].GetAttributes())[attrHash]); got != r.lines[0].Hash {
		t.Fatalf("%s = %q, want the record's hash %q", attrHash, got, r.lines[0].Hash)
	}

	refused := func(name, url string, opts OpenOptions, rcv *receiver) {
		t.Run(name, func(t *testing.T) {
			obs := &fakeObserver{}
			opts.Observer = obs
			before := rcv.count()
			s := openStream(t, url, opts)
			appendN(t, s, 1)
			closeWithin(t, s, time.Second)
			if _, dropped := totals(obs); dropped["otlp,shutdown"] != 1 || len(dropped) != 1 {
				t.Fatalf("dropped %v, want one otlp shutdown", dropped)
			}
			if rcv.count() != before {
				t.Fatalf("the handler received a request")
			}
		})
	}
	refused("no client certificate", otlpURL(rcv.srv.URL, ""), OpenOptions{CAFile: caFile}, rcv)
	other := newTLSReceiver(t, newTestCA(t).serverTLS(t), nil)
	refused("untrusted CA with CAFile set", otlpURL(other.srv.URL, ""), OpenOptions{CAFile: caFile}, other)
	// A nil configuration serves httptest's own certificate, which no
	// system trust store holds.
	sysRcv := newTLSReceiver(t, nil, nil)
	refused("untrusted certificate with CAFile unset", otlpURL(sysRcv.srv.URL, ""), OpenOptions{}, sysRcv)
}

// A 307 is retried with a backoff and never followed, so the redirect
// target never receives the headers file's credential.
//
// Spec: §8.3
func TestOTLPDest_RedirectNotFollowed(t *testing.T) {
	stubTicker(t)
	gate := stubSleepGate(t, true)
	second := newReceiver(t, nil)
	first := newReceiver(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", second.srv.URL+"/v1/logs")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	headersFile := writeFixture(t, t.TempDir(), "headers", []byte("X-Audit-Token: zq7k\n"))
	obs := &fakeObserver{}
	s := openStream(t, otlpURL(first.srv.URL, ""), OpenOptions{HeadersFile: headersFile, Observer: obs})
	appendN(t, s, 1)
	closeWithin(t, s, time.Second)

	reqs := first.requests()
	if len(reqs) == 0 || reqs[0].header.Get("X-Audit-Token") != "zq7k" {
		t.Fatalf("first receiver requests %+v, want one carrying X-Audit-Token", reqs)
	}
	if len(gate.recorded()) == 0 {
		t.Fatalf("the 307 was not retried after a backoff")
	}
	if second.count() != 0 {
		t.Fatalf("the redirect target received %d requests", second.count())
	}
	if _, dropped := totals(obs); dropped["otlp,shutdown"] != 1 || len(dropped) != 1 {
		t.Fatalf("dropped %v, want one otlp shutdown", dropped)
	}
}

// The destination names itself in logs by scheme, host, and path, never
// by the query.
//
// Spec: §8.3
func TestOTLPDest_FailureLogOmitsQuery(t *testing.T) {
	logs := captureLog(t)
	stubTicker(t)
	stubSleepGate(t, true)
	rcv := newReceiver(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	s := openStream(t, otlpURL(rcv.srv.URL, "?tok=s3cr3tvalue"), OpenOptions{})
	appendN(t, s, 1)
	closeWithin(t, s, 300*time.Millisecond)
	u, err := url.Parse(rcv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, "otlp+http://"+u.Host) || strings.Contains(out, "s3cr3tvalue") {
		t.Fatalf("log does not name the destination without its query:\n%s", out)
	}
}

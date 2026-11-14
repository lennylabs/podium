package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// otlpContentType is the media type of an OTLP/HTTP protobuf export request.
const otlpContentType = "application/x-protobuf"

// otlpScopeName is the instrumentation scope name of every exported record.
//
// Spec: §8.3
const otlpScopeName = "github.com/lennylabs/podium/audit"

// otlpDefaultPath is the OTLP/HTTP logs path the destination posts to when
// the configured URL has no path.
const otlpDefaultPath = "/v1/logs"

// otlpSeverityText is the §8.3 OTLP mapping's severity text: every audit
// record is INFO, severity number 9.
const otlpSeverityText = "INFO"

// otlpTraceIDLen is the length of a trace_id the mapping decodes: 16 bytes
// in hexadecimal.
const otlpTraceIDLen = 32

// The §8.3 OTLP mapping's attribute keys.
const (
	attrSchemaVersion = "podium.audit.schema_version"
	attrHash          = "podium.audit.hash"
	attrPrevHash      = "podium.audit.prev_hash"
	attrTenant        = "podium.tenant"
)

// otlpDest is the §8.3 OTLP destination: it POSTs each batch as one
// OTLP/HTTP protobuf ExportLogsServiceRequest through the stream's
// transport. It reads no OTEL_EXPORTER_OTLP_* variable and no tracing
// setting, so enabling tracing never starts or redirects audit export.
//
// Spec: §8.3
type otlpDest struct {
	// name is the destination's log name, without the URL's query.
	name     string
	url      string
	tr       *transport
	resource *resourcepb.Resource
	// rejected is the stream's dropped method bound to this destination and
	// reason rejected, so a partial-success count goes through the one
	// drop funnel.
	rejected func(n int)
	// undecodable is set once the destination has logged a 2xx body that
	// yields no partial-success count, so a receiver that never answers in the OTLP format
	// writes one line rather than one per batch.
	undecodable atomic.Bool
}

// newOTLPDest builds the OTLP destination for u, an otlp+http or
// otlp+https URL Open already parsed, on s's transport. The resource
// carries s's service name and instance identifier.
func newOTLPDest(s *Stream, u *url.URL, name string) *otlpDest {
	return &otlpDest{
		name: name,
		url:  otlpTarget(u),
		tr:   s.transport,
		resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			stringAttr("service.name", s.opts.Service),
			stringAttr("service.instance.id", s.instanceID),
		}},
		rejected: func(n int) { s.dropped(kindOTLP, name, ReasonRejected, n) },
	}
}

// otlpTarget returns the URL the destination posts to: u without the otlp+
// prefix, at /v1/logs when u's path is empty or "/" and at u's path
// otherwise. The query is kept, as the HTTP destination keeps it.
//
// Spec: §8.3
func otlpTarget(u *url.URL) string {
	target := *u
	target.Scheme = strings.TrimPrefix(u.Scheme, "otlp+")
	if target.Path == "" || target.Path == "/" {
		target.Path, target.RawPath = otlpDefaultPath, ""
	}
	return target.String()
}

// send POSTs batch on ctx, which carries the forwarder's request deadline,
// and counts the records a 2xx partial-success response reports as
// rejected. A 2xx status delivers the batch whatever its body holds.
//
// Spec: §8.3
func (d *otlpDest) send(ctx context.Context, batch []record) (sendResult, error) {
	body, err := proto.Marshal(d.request(batch))
	// Every string field holds valid UTF-8 (validUTF8 and encoding/json
	// guarantee it), and the message has no required field, so Marshal
	// does not fail; the error is kept rather than discarded.
	if err != nil {
		return sendResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	// d.url is built from a URL Open already parsed, so this branch does
	// not run; the error is kept rather than discarded.
	if err != nil {
		return sendResult{}, err
	}
	setHeaders(req, d.tr.headers, otlpContentType)
	res, err := d.tr.do(req)
	if err != nil || res.status < 200 || res.status >= 300 {
		return res, err
	}
	d.countRejected(res, len(batch))
	return res, nil
}

// countRejected counts the partial_success.rejected_log_records of a 2xx
// response as drops with reason rejected, at most the batch's size, so a
// receiver that over-reports cannot count a record that was not in the
// request.
//
// The 2xx status has already delivered the batch (§8.3), so a body that
// yields no count counts nothing: a body whose read failed or was cut
// short, a body longer than maxResponseBody, and a body that does not
// decode. Resending would duplicate records the receiver accepted, and a
// receiver that always answers that way would get the same batch forever.
// The first such body is logged.
//
// Spec: §8.3
func (d *otlpDest) countRejected(res sendResult, sent int) {
	n, err := rejectedRecords(res)
	if err != nil {
		if !d.undecodable.Swap(true) {
			log.Printf("audit: %s answered 2xx with a body that yields no partial-success count: %v; counting its batches as delivered", d.name, err)
		}
		return
	}
	if n <= 0 {
		return
	}
	d.rejected(int(min(n, int64(sent))))
}

// rejectedRecords decodes the partial-success count of a 2xx response. An
// incomplete body is not decoded, because partial_success is
// length-delimited and a truncated message can decode to a wrong count.
func rejectedRecords(res sendResult) (int64, error) {
	if res.bodyErr != nil {
		return 0, fmt.Errorf("read the body: %w", res.bodyErr)
	}
	var resp collogspb.ExportLogsServiceResponse
	if err := proto.Unmarshal(res.body, &resp); err != nil {
		return 0, fmt.Errorf("decode the body as an ExportLogsServiceResponse: %w", err)
	}
	return resp.GetPartialSuccess().GetRejectedLogRecords(), nil
}

// request builds the export request of batch: one resource, one scope, and
// one log record per audit record.
func (d *otlpDest) request(batch []record) *collogspb.ExportLogsServiceRequest {
	records := make([]*logspb.LogRecord, len(batch))
	for i, r := range batch {
		records[i] = logRecord(r)
	}
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: d.resource,
		ScopeLogs: []*logspb.ScopeLogs{{
			Scope:      &commonpb.InstrumentationScope{Name: otlpScopeName},
			LogRecords: records,
		}},
	}}}
}

// logRecord maps one chained record to an OpenTelemetry log record. The
// body is the record's JSON line, byte for byte, so a receiver can re-parse
// it and recompute the hash. Caller identity stays in the body.
//
// Spec: §8.3
func logRecord(r record) *logspb.LogRecord {
	e := r.event
	ts := uint64(e.Timestamp.UnixNano())
	attrs := []*commonpb.KeyValue{
		{Key: attrSchemaVersion, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(e.SchemaVersion)}}},
		stringAttr(attrHash, e.Hash),
	}
	if e.PrevHash != "" {
		attrs = append(attrs, stringAttr(attrPrevHash, e.PrevHash))
	}
	if e.Tenant != "" {
		attrs = append(attrs, stringAttr(attrTenant, validUTF8(e.Tenant)))
	}
	return &logspb.LogRecord{
		TimeUnixNano:         ts,
		ObservedTimeUnixNano: ts,
		SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
		SeverityText:         otlpSeverityText,
		EventName:            validUTF8(string(e.Type)),
		TraceId:              otlpTraceID(e.TraceID),
		Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(r.line)}},
		Attributes:           attrs,
	}
}

// otlpTraceID decodes a 32-character hexadecimal trace_id, and returns nil
// for any other value.
func otlpTraceID(id string) []byte {
	if len(id) != otlpTraceIDLen {
		return nil
	}
	b, err := hex.DecodeString(id)
	if err != nil {
		return nil
	}
	return b
}

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// validUTF8 replaces each byte of s that is not part of a valid UTF-8
// sequence with U+FFFD, as encoding/json does when it writes the record's
// line. A protobuf string must be valid UTF-8, or Marshal fails and the
// batch would be retried forever, and the replacement keeps the attribute
// equal to the value the body carries.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
	}
	return b.String()
}

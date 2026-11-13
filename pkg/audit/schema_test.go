package audit

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// schemaV1 is the published v1 record schema. Only this test loads it; no
// production code embeds it (D-17 of proposal 0067).
//
//go:embed schema/audit-event.v1.schema.json
var schemaV1 []byte

// schemaV1ID is the $id the published file carries: its raw-content URL on
// main.
const schemaV1ID = "https://raw.githubusercontent.com/lennylabs/podium/main/pkg/audit/schema/audit-event.v1.schema.json"

// jsonSchema is the subset of draft 2020-12 keywords the v1 record schema
// uses. The module carries no JSON Schema library, and this subset is
// enough to check a record against the published file.
type jsonSchema struct {
	Dialect              string                 `json:"$schema"`
	ID                   string                 `json:"$id"`
	Type                 string                 `json:"type"`
	Required             []string               `json:"required"`
	Properties           map[string]*jsonSchema `json:"properties"`
	AdditionalProperties json.RawMessage        `json:"additionalProperties"`
	Items                *jsonSchema            `json:"items"`
	Const                json.RawMessage        `json:"const"`
	Pattern              string                 `json:"pattern"`
	Format               string                 `json:"format"`
	Minimum              *float64               `json:"minimum"`
}

func loadSchemaV1(t *testing.T) *jsonSchema {
	t.Helper()
	var s jsonSchema
	if err := json.Unmarshal(schemaV1, &s); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return &s
}

// property returns the named property schema of s and fails the test when
// s declares no such property.
func (s *jsonSchema) property(t *testing.T, name string) *jsonSchema {
	t.Helper()
	p, ok := s.Properties[name]
	if !ok {
		t.Fatalf("schema declares no property %q", name)
	}
	return p
}

// closed reports whether s sets additionalProperties to false.
func (s *jsonSchema) closed() bool {
	return string(bytes.TrimSpace(s.AdditionalProperties)) == "false"
}

// additional returns the schema s applies to undeclared members, or nil
// when s admits them without a schema.
func (s *jsonSchema) additional() (*jsonSchema, error) {
	if len(s.AdditionalProperties) == 0 || s.closed() {
		return nil, nil
	}
	var sub jsonSchema
	if err := json.Unmarshal(s.AdditionalProperties, &sub); err != nil {
		return nil, fmt.Errorf("decode additionalProperties: %w", err)
	}
	return &sub, nil
}

// validate returns one message per violation of s by the decoded JSON
// value v, each prefixed with the member path.
func (s *jsonSchema) validate(path string, v any) []string {
	var errs []string
	errs = append(errs, s.checkConst(path, v)...)
	errs = append(errs, s.checkScalar(path, v)...)
	switch val := v.(type) {
	case map[string]any:
		errs = append(errs, s.checkObject(path, val)...)
	case []any:
		if s.Items != nil {
			for i, item := range val {
				errs = append(errs, s.Items.validate(fmt.Sprintf("%s[%d]", path, i), item)...)
			}
		}
	}
	return errs
}

func (s *jsonSchema) checkConst(path string, v any) []string {
	if len(s.Const) == 0 {
		return nil
	}
	var want any
	if err := json.Unmarshal(s.Const, &want); err != nil {
		return []string{fmt.Sprintf("%s: decode const: %v", path, err)}
	}
	if !reflect.DeepEqual(v, want) {
		return []string{fmt.Sprintf("%s = %v, want const %v", path, v, want)}
	}
	return nil
}

// checkScalar applies type, pattern, format, and minimum.
func (s *jsonSchema) checkScalar(path string, v any) []string {
	if s.Type != "" && !hasJSONType(v, s.Type) {
		return []string{fmt.Sprintf("%s = %v (%T), want type %s", path, v, v, s.Type)}
	}
	var errs []string
	if str, ok := v.(string); ok {
		if s.Pattern != "" && !regexp.MustCompile(s.Pattern).MatchString(str) {
			errs = append(errs, fmt.Sprintf("%s = %q does not match %s", path, str, s.Pattern))
		}
		if s.Format == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, str); err != nil {
				errs = append(errs, fmt.Sprintf("%s = %q is not a date-time: %v", path, str, err))
			}
		}
	}
	if n, ok := v.(float64); ok && s.Minimum != nil && n < *s.Minimum {
		errs = append(errs, fmt.Sprintf("%s = %v, want minimum %v", path, n, *s.Minimum))
	}
	return errs
}

// checkObject applies required, properties, and additionalProperties.
func (s *jsonSchema) checkObject(path string, obj map[string]any) []string {
	var errs []string
	for _, name := range s.Required {
		if _, ok := obj[name]; !ok {
			errs = append(errs, fmt.Sprintf("%s: required member %q is missing", path, name))
		}
	}
	extra, err := s.additional()
	if err != nil {
		return append(errs, fmt.Sprintf("%s: %v", path, err))
	}
	for name, val := range obj {
		member := path + "." + name
		switch sub, declared := s.Properties[name]; {
		case declared:
			errs = append(errs, sub.validate(member, val)...)
		case s.closed():
			errs = append(errs, fmt.Sprintf("%s is not declared in the schema", member))
		case extra != nil:
			errs = append(errs, extra.validate(member, val)...)
		}
	}
	return errs
}

func hasJSONType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		n, ok := v.(float64)
		return ok && n == math.Trunc(n)
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	default:
		return false
	}
}

// jsonTagNames returns the JSON member names of a wire struct's fields.
func jsonTagNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no JSON member name", typ.Name(), typ.Field(i).Name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func propertyNames(s *jsonSchema) []string {
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The published schema declares exactly the members the wire structs
// encode, at every level, and admits no other member.
//
// Spec: §8.1
func TestSchema_PropertiesMatchWireStructs(t *testing.T) {
	t.Parallel()
	root := loadSchemaV1(t)
	if root.Dialect != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q, want draft 2020-12", root.Dialect)
	}
	if root.ID != schemaV1ID {
		t.Errorf("$id = %q, want %q", root.ID, schemaV1ID)
	}
	caller := root.property(t, "caller")
	cases := []struct {
		name   string
		schema *jsonSchema
		wire   reflect.Type
	}{
		{"record", root, reflect.TypeOf(jsonEvent{})},
		{"caller", caller, reflect.TypeOf(jsonCaller{})},
		{"caller.network", caller.property(t, "network"), reflect.TypeOf(jsonNetwork{})},
		{"caller.client", caller.property(t, "client"), reflect.TypeOf(jsonClient{})},
	}
	for _, tc := range cases {
		got, want := propertyNames(tc.schema), jsonTagNames(t, tc.wire)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s properties = %v, want the %s tags %v", tc.name, got, tc.wire.Name(), want)
		}
		if tc.schema.Type != "object" || !tc.schema.closed() {
			t.Errorf("%s: type = %q, additionalProperties = %s; want a closed object", tc.name, tc.schema.Type, tc.schema.AdditionalProperties)
		}
	}
}

// The schema requires the members every v1 record carries. The list is
// explicit because schema_version keeps omitempty on the wire struct, so a
// pre-v1 record that a §8.4 or §8.5 rewrite re-encodes still omits it and
// keeps its hash.
//
// Spec: §8.1
func TestSchema_RequiredSet(t *testing.T) {
	t.Parallel()
	got := append([]string(nil), loadSchemaV1(t).Required...)
	sort.Strings(got)
	want := []string{"hash", "schema_version", "timestamp", "type"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
}

// fullEvent sets every member of the record, so the line it produces
// carries every property the schema declares except prev_hash.
func fullEvent() Event {
	return Event{
		Type:          EventArtifactLoaded,
		TraceID:       "4bf92f3577b34da6a3ce929d0e0e4736",
		Caller:        "alice",
		CallerEmail:   "alice@acme.com",
		CallerGroups:  []string{"eng", "platform"},
		CallerNetwork: &CallerNetwork{SourceIP: "203.0.113.7", ForwardedUser: "alice"},
		CallerClient: &CallerClient{
			Kind: "mcp", Name: "podium-mcp", Version: "0.6.0",
			Host: "claude-code", HostVersion: "2.1.0", Harness: "claude-code",
		},
		PublicMode:     true,
		Target:         "finance/close-reconciliation",
		Context:        map[string]string{"version": "1.2.0"},
		Tenant:         "acme",
		ResolvedLayers: []string{"org-defaults", "alice-personal"},
		ResultSize:     3,
	}
}

// memberPaths adds the dotted path of every object member in v to seen.
// Array elements are not descended into, because the v1 arrays hold
// strings.
func memberPaths(prefix string, v any, seen map[string]bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for name, val := range obj {
		path := strings.TrimPrefix(prefix+"."+name, ".")
		seen[path] = true
		memberPaths(path, val, seen)
	}
}

// declaredPaths adds the dotted path of every property s declares to out.
// A property under a member whose additionalProperties is a schema, such as
// context, is open and has no fixed path.
func declaredPaths(prefix string, s *jsonSchema, out map[string]bool) {
	for name, sub := range s.Properties {
		path := strings.TrimPrefix(prefix+"."+name, ".")
		out[path] = true
		declaredPaths(path, sub, out)
	}
}

// A record that a Stream emits validates against the published schema, and
// the records from a fully populated event and its successor exercise every
// property the schema declares.
//
// Spec: §8.1
func TestSchema_StreamRecordConforms(t *testing.T) {
	t.Parallel()
	schema := loadSchemaV1(t)
	w := &writeRecorder{}
	s, err := Open(context.Background(), "stdout", OpenOptions{Stdout: w})
	if err != nil {
		t.Fatalf("Open(stdout): %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	for _, e := range []Event{fullEvent(), {Type: EventRetentionEnforced}} {
		if err := s.Append(context.Background(), e); err != nil {
			t.Fatalf("Append(%s): %v", e.Type, err)
		}
	}
	lines := w.snapshot()
	if len(lines) != 2 {
		t.Fatalf("stdout received %d lines, want 2", len(lines))
	}
	seen := map[string]bool{}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("line %d %q: %v", i, line, err)
		}
		for _, msg := range schema.validate("record", record) {
			t.Errorf("line %d: %s", i, msg)
		}
		memberPaths("", record, seen)
	}
	declared := map[string]bool{}
	declaredPaths("", schema, declared)
	for path := range declared {
		if !seen[path] {
			t.Errorf("no record carried the declared property %s", path)
		}
	}
}

// The validator in this file rejects what the schema excludes, so a passing
// conformance test is evidence. A record without schema_version predates v1
// and is outside the schema.
//
// Spec: §8.1
func TestSchema_RejectsNonConformingRecords(t *testing.T) {
	t.Parallel()
	schema := loadSchemaV1(t)
	const hash = "0000000000000000000000000000000000000000000000000000000000000000"
	valid := func() map[string]any {
		return map[string]any{
			"type": "artifact.loaded", "timestamp": "2026-11-12T10:00:00.5Z",
			"hash": hash, "schema_version": float64(1),
		}
	}
	if errs := schema.validate("record", valid()); len(errs) != 0 {
		t.Fatalf("minimal v1 record: %v", errs)
	}
	cases := map[string]func(r map[string]any){
		"pre-v1 record":          func(r map[string]any) { delete(r, "schema_version") },
		"schema_version 2":       func(r map[string]any) { r["schema_version"] = float64(2) },
		"undeclared member":      func(r map[string]any) { r["stream_id"] = "a" },
		"uppercase hash":         func(r map[string]any) { r["hash"] = strings.ToUpper("ab" + hash[2:]) },
		"undotted type":          func(r map[string]any) { r["type"] = "loaded" },
		"non-RFC 3339 timestamp": func(r map[string]any) { r["timestamp"] = "2026-11-12 10:00:00" },
		"zero result_size":       func(r map[string]any) { r["result_size"] = float64(0) },
		"public_mode false":      func(r map[string]any) { r["caller"] = map[string]any{"public_mode": false} },
		"undeclared client key":  func(r map[string]any) { r["caller"] = map[string]any{"client": map[string]any{"os": "linux"}} },
		"non-string context":     func(r map[string]any) { r["context"] = map[string]any{"n": float64(1)} },
	}
	for name, mutate := range cases {
		r := valid()
		mutate(r)
		if errs := schema.validate("record", r); len(errs) == 0 {
			t.Errorf("%s: the schema admitted %v", name, r)
		}
	}
}

// Every event type this package defines matches the schema's type pattern,
// so the pattern admits each record the registry emits.
//
// Spec: §8.1
func TestSchema_EventTypesMatchPattern(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(loadSchemaV1(t).property(t, "type").Pattern)
	for _, typ := range AllEventTypes() {
		if !pattern.MatchString(string(typ)) {
			t.Errorf("event type %q does not match %s", typ, pattern)
		}
	}
}

package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Whitespace around an element is trimmed, and each element maps to its
// destination kind.
//
// Spec: §8.3
func TestParseDestinations_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "a.log")
	specs, err := parseDestinations(" "+file+" , http://h/x , otlp+https://c:4318 , stdout ", OpenOptions{})
	if err != nil {
		t.Fatalf("parseDestinations: %v", err)
	}
	want := []struct {
		kind destKind
		name string
	}{{kindFile, file}, {kindHTTP, "http://h/x"}, {kindOTLP, "otlp+https://c:4318"}, {kindStdout, "stdout"}}
	if len(specs) != len(want) {
		t.Fatalf("got %d destinations, want %d", len(specs), len(want))
	}
	for i, w := range want {
		if specs[i].kind != w.kind || specs[i].name() != w.name {
			t.Errorf("destination %d = %s %q, want %s %q", i, specs[i].kind, specs[i].name(), w.kind, w.name)
		}
	}
}

// A recognized `scheme://` prefix is matched without regard to case.
//
// Spec: §8.3
func TestParseDestinations_SchemePrefixIgnoresCase(t *testing.T) {
	t.Parallel()
	specs, err := parseDestinations("HTTPS://h/x,Otlp+Http://c:4318", OpenOptions{})
	if err != nil {
		t.Fatalf("parseDestinations: %v", err)
	}
	if len(specs) != 2 || specs[0].kind != kindHTTP || specs[1].kind != kindOTLP {
		t.Fatalf("got %+v, want one http and one otlp destination", specs)
	}
	if got := specs[0].name(); got != "https://h/x" {
		t.Errorf("name = %q, want https://h/x", got)
	}
}

// Every destination-list refusal wraps ErrConfig, names the list variable, and never
// echoes a URL's userinfo, query, or unparseable text.
//
// Spec: §8.3, §13.12
func TestOpen_RefusesUnusableConfiguration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.log"), filepath.Join(dir, "b.log")
	cases := []struct {
		name, value string
		opts        OpenOptions
		want        []string
	}{
		{"empty element", a + ",,http://h/x", OpenOptions{}, []string{envRegistryPath, "element 2 is empty"}},
		{"trailing empty element", a + ",", OpenOptions{}, []string{envRegistryPath, "is empty"}},
		{"unrecognized scheme", "s3://alice:pw9x@bucket/key?sig=zq7k", OpenOptions{}, []string{envRegistryPath, "s3://bucket/key", "unrecognized scheme"}},
		{"opaque http prefix with userinfo", "http:x://alice:pw9x@host/p?tok=zq7k", OpenOptions{}, []string{envRegistryPath, "malformed-scheme", "unrecognized scheme"}},
		{"opaque otlp prefix with userinfo", "otlp+https:evil://alice:pw9x@c", OpenOptions{}, []string{envRegistryPath, "malformed-scheme", "unrecognized scheme"}},
		{"prefix with a leading digit", "1http://alice:pw9x@h/x", OpenOptions{}, []string{envRegistryPath, "malformed-scheme", "does not parse"}},
		{"uppercase unrecognized scheme", "S3://alice:pw9x@bucket/key", OpenOptions{}, []string{envRegistryPath, "s3://bucket/key", "unrecognized scheme"}},
		{"unparseable URL", "http://%zz", OpenOptions{}, []string{envRegistryPath, "scheme http"}},
		{"unparseable malformed scheme", "a b:c@d://x", OpenOptions{}, []string{envRegistryPath, "malformed-scheme"}},
		{"unparseable empty scheme", "://x", OpenOptions{}, []string{envRegistryPath, "scheme-less"}},
		{"two files", a + "," + b, OpenOptions{}, []string{envRegistryPath, b, "more than one file"}},
		{"repeated URL", "http://h/x?tok=zq7k,http://h/x?tok=zq7k", OpenOptions{}, []string{envRegistryPath, "http://h/x", "twice"}},
		{"userinfo", "https://alice:pw9x@h/x", OpenOptions{}, []string{envRegistryPath, "https://h/x", "userinfo"}},
		{"stdout with Local", "stdout", OpenOptions{Local: true}, []string{envLocalSink, "registry-only"}},
		{"stdout with TracingStdout", "stdout", OpenOptions{TracingStdout: true}, []string{envRegistryPath, envTracing}},
		{"local list variable", "default,,x", OpenOptions{Local: true}, []string{envLocalSink}},
	}
	for _, row := range transportRefusals(t, dir) {
		cases = append(cases, struct {
			name, value string
			opts        OpenOptions
			want        []string
		}{row.name, "http://h/x", row.opts, row.want})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := Open(context.Background(), tc.value, tc.opts)
			if !errors.Is(err, ErrConfig) {
				t.Fatalf("Open(%q) = %v, want ErrConfig", tc.value, err)
			}
			if s != nil {
				t.Fatalf("Open returned a stream with its error")
			}
			msg := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q does not contain %q", msg, w)
				}
			}
			for _, secret := range []string{"alice", "pw9x", "zq7k", "sig=", "tok=", "%zz", "BEGIN"} {
				if strings.Contains(msg, secret) {
					t.Errorf("error %q echoes %q", msg, secret)
				}
			}
		})
	}
}

// Until its constructor lands, Open refuses a stdout element with ErrConfig
// naming the list variable and the kind.
//
// Spec: §13.12
func TestOpen_RefusesDestinationsWithoutConstructor(t *testing.T) {
	t.Parallel()
	_, err := Open(context.Background(), "stdout", OpenOptions{})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("Open(stdout) = %v, want ErrConfig", err)
	}
	if !strings.Contains(err.Error(), envRegistryPath) || !strings.Contains(err.Error(), "the stdout destination") {
		t.Errorf("error %q does not name %s and the stdout kind", err, envRegistryPath)
	}
}

// A file destination that NewFileSink cannot open, or that cannot be opened
// for appending, refuses startup with ErrConfig and leaves an existing
// file's bytes unchanged.
//
// Spec: §13.12
func TestOpen_UnopenableFileRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	readOnlyDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })
	readOnlyFile := filepath.Join(dir, "ro.log")
	seed := []byte(`{"type":"artifact.loaded","timestamp":"2026-11-12T09:00:00Z","hash":"h"}` + "\n")
	if err := os.WriteFile(readOnlyFile, seed, 0o444); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		probe      bool
	}{
		{"directory", dir, false},
		{"parent is a regular file", filepath.Join(regular, "audit.log"), false},
		{"absent file in a read-only directory", filepath.Join(readOnlyDir, "audit.log"), true},
		{"read-only file", readOnlyFile, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.probe {
				skipIfRoot(t)
			}
			s, err := Open(context.Background(), tc.path, OpenOptions{})
			if !errors.Is(err, ErrConfig) || s != nil {
				t.Fatalf("Open(%s) = %v, %v; want nil and ErrConfig", tc.path, s, err)
			}
			if !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), envRegistryPath) {
				t.Errorf("error %q does not name the path and %s", err, envRegistryPath)
			}
		})
	}
	if got, _ := os.ReadFile(readOnlyFile); !bytes.Equal(got, seed) {
		t.Fatalf("a refused Open changed the existing file")
	}

	absent := filepath.Join(dir, "new", "audit.log")
	s, err := Open(context.Background(), absent, OpenOptions{})
	if err != nil {
		t.Fatalf("Open of an absent path in a writable directory: %v", err)
	}
	if info, err := os.Stat(absent); err != nil || info.Size() != 0 {
		t.Fatalf("probe did not leave an empty file: %v", err)
	}
	appendN(t, s, 1)
	if prev := fileRecords(t, absent)[0].PrevHash; prev != "" {
		t.Fatalf("first record of a new file has prev_hash %q", prev)
	}
}

// An empty value opens the default file in both streams, default opens it
// in the local stream, and default in the registry list is a file path.
//
// Spec: §6.2, §8.3, §13.12
func TestOpen_EmptyAndDefaultValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	defaultPath := filepath.Join(home, ".podium", "audit.log")
	cases := []struct {
		value string
		opts  OpenOptions
		want  string
	}{
		{"", OpenOptions{}, defaultPath},
		{"", OpenOptions{Local: true}, defaultPath},
		{"default", OpenOptions{Local: true}, defaultPath},
		{"default", OpenOptions{}, "default"},
	}
	for _, tc := range cases {
		s, err := Open(context.Background(), tc.value, tc.opts)
		if err != nil {
			t.Fatalf("Open(%q, Local=%v): %v", tc.value, tc.opts.Local, err)
		}
		if got := s.File().Path(); got != tc.want {
			t.Errorf("Open(%q, Local=%v) file = %q, want %q", tc.value, tc.opts.Local, got, tc.want)
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// An unresolvable default file refuses startup and names the default path.
//
// Spec: §13.12
func TestOpen_UnresolvableDefaultFileRefuses(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Open(context.Background(), "", OpenOptions{})
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), defaultFileName) {
		t.Fatalf("Open with no home directory = %v, want ErrConfig naming %s", err, defaultFileName)
	}
}

// errReader fails every Read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("entropy exhausted") }

// service.instance.id is a version 4 UUID drawn from OpenOptions.Rand, and a
// failing source refuses Open.
//
// Spec: §8.3
func TestOpen_InstanceIDFromRand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s := openFileStream(t, path, OpenOptions{Rand: bytes.NewReader(bytes.Repeat([]byte{0xab}, 16))})
	if s.instanceID != "abababab-abab-4bab-abab-abababababab" {
		t.Fatalf("instanceID = %q", s.instanceID)
	}
	if _, err := Open(context.Background(), path, OpenOptions{Rand: errReader{}}); !errors.Is(err, ErrConfig) {
		t.Fatalf("Open with a failing Rand = %v, want ErrConfig", err)
	}
}

// OpenFile opens a file-only stream for the offline forms, and its errors
// name the path and no variable.
//
// Spec: §8.4, §8.5
func TestOpenFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	s, err := OpenFile(path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	appendN(t, s, 2)
	if err := s.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	dir := t.TempDir()
	_, err = OpenFile(dir, OpenOptions{})
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("OpenFile(directory) = %v, want an error naming the path", err)
	}
	if strings.Contains(err.Error(), "PODIUM_") {
		t.Fatalf("OpenFile error %q names a variable", err)
	}
}

// The queue bound accepts only a positive base-10 integer.
//
// Spec: §13.12
func TestParseQueueMaxBytes(t *testing.T) {
	t.Parallel()
	ok := map[string]int64{"": 67108864, "1": 1, "67108864": 67108864}
	for raw, want := range ok {
		got, err := ParseQueueMaxBytes(raw)
		if err != nil || got != want {
			t.Errorf("ParseQueueMaxBytes(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "-1", "64MiB", " 5", "1e6", "9223372036854775808"} {
		_, err := ParseQueueMaxBytes(raw)
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), envQueueMaxBytes) || !strings.Contains(err.Error(), `"`+raw+`"`) {
			t.Errorf("ParseQueueMaxBytes(%q) = %v, want ErrConfig naming %s and the value", raw, err, envQueueMaxBytes)
		}
	}
}

// A non-positive QueueMaxBytes takes the default bound.
//
// Spec: §8.3, §13.12
func TestOpenOptions_NonPositiveQueueBoundTakesDefault(t *testing.T) {
	t.Parallel()
	for _, n := range []int64{0, -1} {
		if got := (OpenOptions{QueueMaxBytes: n}).withDefaults().QueueMaxBytes; got != DefaultQueueMaxBytes {
			t.Errorf("QueueMaxBytes %d resolved to %d, want %d", n, got, DefaultQueueMaxBytes)
		}
	}
	if got := (OpenOptions{QueueMaxBytes: 5}).withDefaults().QueueMaxBytes; got != 5 {
		t.Errorf("QueueMaxBytes 5 resolved to %d", got)
	}
}

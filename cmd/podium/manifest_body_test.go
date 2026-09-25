package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// manifestBodyResponse is a load_artifact body whose document travels by url.
func manifestBodyResponse(t *testing.T, typ, url, hash string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"id": "team/x", "type": typ, "frontmatter": "---\ntype: skill\n---\n", "manifest_body": "",
		"manifest_body_url": map[string]any{"presigned_url": url, "content_hash": hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func digestOf(doc string) string {
	sum := sha256.Sum256([]byte(doc))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Spec: §6.6, §7.6.1 — followManifestBody restores frontmatter for a
// non-skill and skill_raw for a skill, re-derives manifest_body with the
// ingest split, sends the token to a URL that is not SigV4 presigned, and
// returns a body without a link unchanged.
func TestFollowManifestBody_RestoresTheDocument(t *testing.T) {
	const doc = "---\nname: x\ndescription: d\n---\n\nthe body\n"
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(ts.Close)

	for typ, docField := range map[string]string{"context": "frontmatter", "skill": "skill_raw"} {
		out, err := followManifestBody(context.Background(), manifestBodyResponse(t, typ, ts.URL+"/objects/k", digestOf(doc)), "tok")
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatal(err)
		}
		if fields[docField] != doc || fields["manifest_body"] != "the body\n" || fields["manifest_body_url"] != nil {
			t.Errorf("%s: fields = %v", typ, fields)
		}
	}
	if len(auths) != 2 || auths[0] != "Bearer tok" {
		t.Errorf("Authorization headers = %v, want the token on the /objects URL", auths)
	}
	if _, err := followManifestBody(context.Background(), manifestBodyResponse(t, "context", ts.URL+"/k?X-Amz-Signature=s", digestOf(doc)), "tok"); err != nil || auths[2] != "" {
		t.Errorf("SigV4 URL: err %v, Authorization %q, want none", err, auths[2])
	}
	plain := []byte(`{"id":"team/x","frontmatter":"inline"}`)
	if out, err := followManifestBody(context.Background(), plain, "tok"); err != nil || string(out) != string(plain) {
		t.Errorf("no link: out %s err %v, want the body unchanged", out, err)
	}
}

// Spec: §7.6.1 — a document that does not hash to the link, a URL that
// answers an error status, and a document that does not parse fail.
func TestFollowManifestBody_Failures(t *testing.T) {
	const doc = "---\ntype: context\n---\nbody\n"
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(doc)) }))
	t.Cleanup(ok.Close)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(broken.Close)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("---\n: [\n---\n")) }))
	t.Cleanup(bad.Close)

	cases := map[string]struct {
		body []byte
		want string
	}{
		"digest mismatch": {manifestBodyResponse(t, "context", ok.URL, "sha256:"+strings.Repeat("0", 64)), "content hash mismatch"},
		"HTTP 500":        {manifestBodyResponse(t, "context", broken.URL, digestOf(doc)), "HTTP 500"},
		"unparseable":     {manifestBodyResponse(t, "context", bad.URL, digestOf("---\n: [\n---\n")), "parse ARTIFACT.md"},
		"unreachable":     {manifestBodyResponse(t, "context", "http://127.0.0.1:1/x", digestOf(doc)), "127.0.0.1:1"},
		"bad url":         {manifestBodyResponse(t, "context", "://no-scheme", digestOf(doc)), "missing protocol scheme"},
	}
	for name, tc := range cases {
		if _, err := followManifestBody(context.Background(), tc.body, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

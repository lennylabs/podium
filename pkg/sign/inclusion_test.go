package sign

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// refLeafHash and refTree build a reference RFC 6962 §2.1 tree from the
// RFC's recursive definitions, independent of the iterative RFC 9162
// algorithm under test.
func refLeafHash(i int) []byte {
	sum := sha256.Sum256(append([]byte{0x00}, fmt.Appendf(nil, "acme leaf %d", i)...))
	return sum[:]
}

func refSplit(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

func refRoot(leaves [][]byte) []byte {
	if len(leaves) == 1 {
		return leaves[0]
	}
	k := refSplit(len(leaves))
	return interiorHash(refRoot(leaves[:k]), refRoot(leaves[k:]))
}

func refPath(m int, leaves [][]byte) [][]byte {
	if len(leaves) <= 1 {
		return nil
	}
	k := refSplit(len(leaves))
	if m < k {
		return append(refPath(m, leaves[:k]), refRoot(leaves[k:]))
	}
	return append(refPath(m-k, leaves[k:]), refRoot(leaves[:k]))
}

// Spec: §4.7.9 — the RFC 9162 §2.1.3.2 recomputation reproduces the
// reference root at every position, refuses leftover or missing hashes,
// and verifyInclusion refuses an index outside [0, tree size).
func TestVerifyInclusion(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 12; n++ {
		leaves := make([][]byte, n)
		for i := range leaves {
			leaves[i] = refLeafHash(i)
		}
		want := refRoot(leaves)
		for i := 0; i < n; i++ {
			path := refPath(i, leaves)
			got, ok := rootFromInclusionProof(uint64(i), uint64(n), leaves[i], path)
			if !ok || !bytes.Equal(got, want) {
				t.Errorf("size %d index %d: root mismatch", n, i)
			}
			if _, ok := rootFromInclusionProof(uint64(i), uint64(n), leaves[i], append(path, want)); ok {
				t.Errorf("size %d index %d: leftover hash accepted", n, i)
			}
			if len(path) > 0 {
				if got, ok := rootFromInclusionProof(uint64(i), uint64(n), leaves[i], path[:len(path)-1]); ok && bytes.Equal(got, want) {
					t.Errorf("size %d index %d: short path accepted", n, i)
				}
			}
		}
	}

	h := sigstoreharness.New(t)
	sum := sha256.Sum256([]byte("acme artifact"))
	env, err := decodeEnvelope(h.Envelope(t, "sha256:"+hex.EncodeToString(sum[:])))
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	root, err := parseTrustedRoot(h.TrustedRootJSON())
	if err != nil {
		t.Fatalf("trusted root: %v", err)
	}
	if err := verifyInclusion(root, env.TLog, h.Clock()); err != nil {
		t.Fatalf("genuine entry: %v", err)
	}
	for _, index := range []int64{-1, 7, 8} {
		tl := *env.TLog
		tl.LogIndex = index
		if err := verifyInclusion(root, &tl, h.Clock()); err == nil || !strings.Contains(err.Error(), "inclusion proof does not verify") {
			t.Errorf("index %d: err = %v", index, err)
		}
	}
	bad := []struct {
		name string
		edit func(*tlogEntry)
	}{
		{"body not base64", func(tl *tlogEntry) { tl.Body = "%%%" }},
		{"hash not base64", func(tl *tlogEntry) { tl.Hashes = append([]string{"%%%"}, tl.Hashes[1:]...) }},
	}
	for _, b := range bad {
		tl := *env.TLog
		b.edit(&tl)
		if err := verifyInclusion(root, &tl, h.Clock()); err == nil || !strings.Contains(err.Error(), "inclusion proof does not verify") {
			t.Errorf("%s: err = %v", b.name, err)
		}
	}
}

// Spec: §4.7.9 — a checkpoint is a signed note: origin, decimal size, and
// a base64 32-byte root, optional extension lines, a blank line, and one
// or more signature lines.
func TestParseCheckpoint(t *testing.T) {
	t.Parallel()
	root := base64.StdEncoding.EncodeToString(make([]byte, 32))
	sig := base64.StdEncoding.EncodeToString([]byte("hint+signature"))
	body := "log.acme.test\n7\n" + root + "\n"
	good := body + "acme extension\n\n— log.acme.test " + sig + "\n"
	cp, err := parseCheckpoint(good)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cp.size != 7 || cp.body != body+"acme extension\n" || len(cp.sigs) != 1 || string(cp.sigs[0]) != "+signature" {
		t.Fatalf("checkpoint = %+v", cp)
	}
	cases := map[string]string{
		"missing blank line":    body + "— log.acme.test " + sig + "\n",
		"fewer than 3 lines":    "log.acme.test\n7\n\n— log.acme.test " + sig + "\n",
		"non-decimal size":      "log.acme.test\nseven\n" + root + "\n\n— log.acme.test " + sig + "\n",
		"short root":            "log.acme.test\n7\n" + base64.StdEncoding.EncodeToString([]byte("short")) + "\n\n— log.acme.test " + sig + "\n",
		"no signature line":     body + "\n",
		"line without em dash":  body + "\n- log.acme.test " + sig + "\n",
		"signature not base64":  body + "\n— log.acme.test %%%\n",
		"signature only a hint": body + "\n— log.acme.test " + base64.StdEncoding.EncodeToString([]byte("hint")) + "\n",
	}
	for name, text := range cases {
		if _, err := parseCheckpoint(text); err == nil || !strings.Contains(err.Error(), "checkpoint does not parse") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

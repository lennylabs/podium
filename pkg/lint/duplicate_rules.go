package lint

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// minUnitWords is the §7.3.1 bound below which a unit is not compared.
const minUnitWords = 50

// Unit names for an artifact's prose body (§7.3.1).
const (
	skillBodyUnit    = "SKILL.md body"
	artifactBodyUnit = "ARTIFACT.md body"
)

// nearDuplicateRemedy is the action a near-duplicate diagnostic recommends.
// A skill must be self-contained, so the advice cannot be to keep one shared
// copy; it names the risk, which is that the copies drift apart, and the two
// ways to manage it. The same advice serves a body and a prose resource.
const nearDuplicateRemedy = "the copies can drift; keep them in step, or move the shared text into one artifact and reference that artifact by its canonical ID"

// contentUnit is one compared unit of lint.near_duplicate_content: the
// index of the record that holds it and its name (a body name or a resource
// path).
type contentUnit struct {
	record int
	name   string
}

// ruleNearDuplicateContent flags units of different artifacts whose
// five-word shingle sets have a similarity of t or higher. A unit is an
// artifact's prose body (SKILL.md for a skill, ARTIFACT.md for any other
// type) or one of its prose resources (isProseResource). A unit of fewer
// than 50 words is not compared, and two units of one artifact are not
// compared. Each flagged unit draws one warning on its artifact naming
// the peer units by descending similarity and then by identifier.
// Suppression is one-sided, as for lint.colliding_descriptions.
//
// Spec: §7.3.1
type ruleNearDuplicateContent struct {
	t float64
}

func (ruleNearDuplicateContent) Code() string { return "lint.near_duplicate_content" }

// Check returns no diagnostics when ctx is cancelled before the join
// finishes. Rule.Check returns no error, so the caller checks ctx.Err()
// itself rather than reading an empty result as a clean catalog.
func (r ruleNearDuplicateContent) Check(ctx context.Context, _ *filesystem.Registry, records []filesystem.ArtifactRecord) []Diagnostic {
	units, sets, groups, pairs, err := nearDuplicateJoin(ctx, records, r.t)
	if err != nil {
		return nil
	}
	ident := func(s int) string { return records[units[s].record].ID + " " + units[s].name }
	peers := newPeerIndex(sets, groups, pairs, ident)
	var out []Diagnostic
	for s, u := range units {
		rec := records[u.record]
		if rec.Artifact.Suppresses(r.Code()) {
			continue
		}
		first, total := peers.summary(s)
		if total == 0 {
			continue
		}
		out = append(out, warn(rec.ID, r.Code(),
			fmt.Sprintf("%s nearly duplicates %s; %s", u.name, formatPeers(first, total, ident), nearDuplicateRemedy)))
	}
	return out
}

// nearDuplicateJoin collects the units of records in record order and then
// path order, builds their shingle sets, groups identical sets, and joins
// the groups at threshold t. units and sets are parallel. A unit of fewer
// than 50 words is skipped.
func nearDuplicateJoin(ctx context.Context, records []filesystem.ArtifactRecord, t float64) (units []contentUnit, sets []tokenSet, groups []setGroup, pairs []scoredPair, err error) {
	in := newInterner()
	for i, rec := range records {
		for _, u := range recordUnits(i, rec) {
			words := wordTokens(u.text)
			if len(words) < minUnitWords {
				continue
			}
			units = append(units, u.contentUnit)
			sets = append(sets, tokenSet{owner: i, tokens: in.shingles(words)})
		}
	}
	groups = groupIdentical(sets)
	pairs, err = similarPairs(ctx, groups, t)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return units, sets, groups, pairs, nil
}

// textUnit is a contentUnit with its text.
type textUnit struct {
	contentUnit
	text string
}

// recordUnits returns the prose body of rec and each of its prose resources,
// the resources in path order. Resources exclude the root ARTIFACT.md and
// SKILL.md, so a body is never also a resource unit.
func recordUnits(i int, rec filesystem.ArtifactRecord) []textUnit {
	var out []textUnit
	switch {
	case rec.Skill != nil:
		out = append(out, textUnit{contentUnit{record: i, name: skillBodyUnit}, rec.Skill.Body})
	case rec.Artifact != nil:
		out = append(out, textUnit{contentUnit{record: i, name: artifactBodyUnit}, rec.Artifact.Body})
	}
	paths := make([]string, 0, len(rec.Resources))
	for p, b := range rec.Resources {
		if isProseResource(p, b) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		out = append(out, textUnit{contentUnit{record: i, name: p}, string(rec.Resources[p])})
	}
	return out
}

// proseResourceExts are the file-name endings of a prose resource, compared
// in lower case.
var proseResourceExts = []string{".md", ".markdown", ".txt", ".rst"}

// licenseWords are the file-name parts that mark a license file when they
// stand alone or lead a two-part name, such as LICENSE and LICENSE-MIT.
var licenseWords = map[string]bool{
	"license": true,
	"licence": true,
	"copying": true,
	"notice":  true,
}

// fontLicensePart marks a font license file when it is the first or the last
// part of the name, such as OFL.txt and ArsenalSC-OFL.txt.
const fontLicensePart = "ofl"

// isProseResource reports whether the bundled resource at resourcePath with
// content b is a prose resource: its file name ends in .md, .markdown, .txt,
// or .rst without regard to case, it is valid UTF-8 with no NUL byte, and it
// is not a license file. Scripts and data files are excluded because a skill
// must carry its own copy of every file it runs, and license files because
// each package carries one, so neither duplication is something an author
// can act on.
//
// Spec: §7.3.1
func isProseResource(resourcePath string, b []byte) bool {
	name := strings.ToLower(path.Base(resourcePath))
	prose := false
	for _, ext := range proseResourceExts {
		if strings.HasSuffix(name, ext) {
			prose = true
			break
		}
	}
	return prose && !isLicenseFile(name) && utf8.Valid(b) && bytes.IndexByte(b, 0) < 0
}

// isLicenseFile reports whether a file name is a license file. The base name,
// lowercased and without its final extension, is split into parts at its -,
// _, and . characters, and every part that consists only of digits is
// removed. It is a license file when the remaining parts are license,
// licence, copying, or notice alone or followed by one more part, or when ofl
// is the first or the last part. LICENSE, LICENSE-MIT, COPYING, NOTICE.md,
// OFL.txt, ArsenalSC-OFL.txt, LICENSE_1_0.txt, and LICENSE-APACHE-2.0.txt are
// license files: a version number in the name does not count as a part, so
// the Boost and Apache file names match like their unversioned forms.
// notice-of-changes.md, privacy-notice.md, and licensing-guide.md are not: a
// license word that leads a longer name, or that follows another word, names
// a document about the subject rather than the license each package carries.
//
// Spec: §7.3.1
func isLicenseFile(name string) bool {
	name = strings.ToLower(path.Base(name))
	name = strings.TrimSuffix(name, path.Ext(name))
	parts := slices.DeleteFunc(
		strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' }),
		isDigits)
	if len(parts) == 0 {
		return false
	}
	if licenseWords[parts[0]] && len(parts) <= 2 {
		return true
	}
	return parts[0] == fontLicensePart || parts[len(parts)-1] == fontLicensePart
}

// isDigits reports whether a name part consists only of ASCII digits, as the
// version numbers in LICENSE_1_0 and LICENSE-APACHE-2.0 do. FieldsFunc never
// yields an empty part, so an empty string does not reach it.
func isDigits(part string) bool {
	return strings.Trim(part, "0123456789") == ""
}

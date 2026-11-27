package source

import (
	"context"
	"fmt"
)

// LineageAnswer is one answer of a lineage query: where a commit of the
// request stands against the snapshot reference and the base commit.
//
// Spec: §9.1 (Lineage query)
type LineageAnswer string

// LineageAnswer values. A provider returns only these, and the registry
// treats any other value as a failed query (CheckLineageAnswers).
const (
	// InLineage states that the commit is the reference or an ancestor of it,
	// and that the request names no base or the commit equals the base or
	// descends from it.
	InLineage LineageAnswer = "in_lineage"
	// NotInHistory states that the commit is neither the reference nor an
	// ancestor of it.
	NotInHistory LineageAnswer = "not_in_history"
	// NotDescendant states that the commit is the reference or an ancestor of
	// it, that the request names a base, and that the commit neither equals
	// the base nor descends from it.
	NotDescendant LineageAnswer = "not_descendant"
)

// Limits of the lineage query members (§9.3 bounded payloads).
const (
	// MaxLineageStringBytes bounds the reference, the base, and each commit.
	MaxLineageStringBytes = 128
	// MaxLineageCommits bounds the number of commits of one request.
	MaxLineageCommits = 64
)

// LineageProvider is the optional lineage member of the LayerSourceProvider
// SPI. A provider that implements it declares through AnswersLineage whether
// it answers lineage queries; the declaration is a constant, as the trigger
// model is, so the registry reads it without a snapshot.
//
// Spec: §4.6 (Source types), §9.1 (Lineage query)
type LineageProvider interface {
	Provider
	// AnswersLineage reports whether the provider answers lineage queries. It
	// returns the same value on every call.
	AnswersLineage() bool
	// Lineage answers, for each entry of commits and in the same order,
	// whether the commit is in the lineage of the snapshot at reference: it is
	// reference or an ancestor of it, and, when base is not empty, it equals
	// base or descends from it. cfg is the layer source configuration the
	// snapshot operation receives. A provider returns answers only when it
	// has decided every one of them, and an ingest.source_unreachable error
	// otherwise. The call keeps no state and changes nothing, so a retry is
	// safe.
	Lineage(ctx context.Context, cfg LayerConfig, reference, base string, commits []string) ([]LineageAnswer, error)
}

// AnswersLineage reports a provider's lineage declaration. A provider that
// does not implement LineageProvider answers no lineage query.
//
// Spec: §4.6 (Source types)
func AnswersLineage(p Provider) bool {
	lp, ok := p.(LineageProvider)
	return ok && lp.AnswersLineage()
}

// ValidateLineageRequest checks the request members against their §9.1
// limits. A request that breaks one is refused with ErrInvalidConfig
// (registry.invalid_argument) and no commit of it is answered.
//
// Spec: §9.1 (Lineage query)
func ValidateLineageRequest(reference, base string, commits []string) error {
	if reference == "" || len(reference) > MaxLineageStringBytes {
		return fmt.Errorf("%w: lineage reference must hold 1 to %d bytes, got %d", ErrInvalidConfig, MaxLineageStringBytes, len(reference))
	}
	if len(base) > MaxLineageStringBytes {
		return fmt.Errorf("%w: lineage base must hold at most %d bytes, got %d", ErrInvalidConfig, MaxLineageStringBytes, len(base))
	}
	if len(commits) == 0 || len(commits) > MaxLineageCommits {
		return fmt.Errorf("%w: lineage query must name 1 to %d commits, got %d", ErrInvalidConfig, MaxLineageCommits, len(commits))
	}
	for i, c := range commits {
		if len(c) > MaxLineageStringBytes {
			return fmt.Errorf("%w: lineage commit %d must hold at most %d bytes, got %d", ErrInvalidConfig, i, MaxLineageStringBytes, len(c))
		}
	}
	return nil
}

// CheckLineageAnswers applies the §9.1 no-default rule to a provider's
// response: a response whose answers differ in number from the request's
// commits, or that holds a value other than the three answers, is an
// ingest.source_unreachable failure. No value stands in for a missing answer,
// so a response that drops a member cannot turn a refusal into a pass.
//
// Spec: §9.1 (Lineage query)
func CheckLineageAnswers(commits []string, answers []LineageAnswer) error {
	if len(answers) != len(commits) {
		return fmt.Errorf("%w: lineage query for %d commits returned %d answers", ErrSourceUnreachable, len(commits), len(answers))
	}
	for i, a := range answers {
		switch a {
		case InLineage, NotInHistory, NotDescendant:
		default:
			return fmt.Errorf("%w: lineage answer %d is %q, not one of %s, %s, or %s",
				ErrSourceUnreachable, i, a, InLineage, NotInHistory, NotDescendant)
		}
	}
	return nil
}

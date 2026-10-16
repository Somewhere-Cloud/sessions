package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// ComparisonSchema identifies the comparison record format.
const ComparisonSchema = "sessions.review-comparison/1"

// Identity names a reviewer. For an external adapter, FileSHA256BeforeRun is
// the sha256 of the executable file observed just before invocation, hashed
// within the reviewer timeout (omitted when the file is not regular, too
// large, unreadable, or the timeout or cancellation cut hashing short). It
// records what was on disk, not which code answered.
type Identity struct {
	ID                  string   `json:"id"`
	Kind                string   `json:"kind"`
	Version             string   `json:"version"`
	Path                string   `json:"path,omitempty"`
	Args                []string `json:"args,omitempty"`
	FileSHA256BeforeRun string   `json:"file_sha256_before_run,omitempty"`
}

// Input is what every reviewer receives: the canonical snapshot and question
// bytes plus their hashes. Each reviewer gets its own copy of the same bytes.
type Input struct {
	Snapshot     []byte
	SnapshotHash string
	Questions    []byte
	QuestionHash string
	snapshot     FrozenSnapshot
	questions    FrozenQuestions
}

// Answer is a reviewer's reply. Scores are uncalibrated and optional.
type Answer struct {
	Choice   string             `json:"choice"`
	Scores   map[string]float64 `json:"scores,omitempty"`
	Notes    string             `json:"notes,omitempty"`
	Declared *DeclaredReviewer  `json:"declared_reviewer,omitempty"`
}

// DeclaredReviewer is what an external adapter says about itself (a claim).
type DeclaredReviewer struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// ErrorKind classifies a reviewer failure.
type ErrorKind string

const (
	ErrUnavailable   ErrorKind = "unavailable"
	ErrTimeout       ErrorKind = "timeout"
	ErrMalformed     ErrorKind = "malformed"
	ErrInvalidAnswer ErrorKind = "invalid_answer"
	ErrCancelled     ErrorKind = "cancelled"
)

// Error is a typed reviewer failure.
type Error struct {
	Kind   ErrorKind `json:"kind"`
	Detail string    `json:"detail"`
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Detail }

func reviewError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

// Reviewer is the seam. Implementations must not mutate Input, write state or
// act on the lane; they only answer.
type Reviewer interface {
	Identity() Identity
	Review(ctx context.Context, in Input) (Answer, error)
}

// Result is one reviewer's outcome in a comparison.
type Result struct {
	Reviewer    Identity `json:"reviewer"`
	Status      string   `json:"status"`
	Answer      *Answer  `json:"answer,omitempty"`
	Error       *Error   `json:"error,omitempty"`
	InputSHA256 string   `json:"input_sha256"`
	DurationMS  int64    `json:"duration_ms"`
	Authority   string   `json:"authority"`
}

// Comparison is the record of one explicit comparison.
type Comparison struct {
	Schema       string   `json:"schema"`
	SnapshotHash string   `json:"snapshot_hash"`
	QuestionHash string   `json:"question_hash"`
	Subject      string   `json:"subject"`
	NullPaths    []string `json:"null_paths"`
	Results      []Result `json:"results"`
	Authority    string   `json:"authority"`
	Note         string   `json:"note"`
}

const comparisonNote = "Reviewer outputs are uncalibrated suggestions with no authority. They are not verification, " +
	"do not decide runtime facts, and are not acted on by Sessions."

// Compare gives every reviewer byte-identical input, one at a time, and
// records each identity, answer or typed error. It performs no other action.
func Compare(ctx context.Context, snapshot FrozenSnapshot, questions FrozenQuestions, reviewers []Reviewer) Comparison {
	out := Comparison{Schema: ComparisonSchema, SnapshotHash: snapshot.Hash, QuestionHash: questions.Hash,
		Subject: snapshot.Snapshot.Subject, NullPaths: snapshot.NullPaths(), Authority: "none", Note: comparisonNote}
	for _, reviewer := range reviewers {
		in := Input{Snapshot: bytes.Clone(snapshot.Canonical), SnapshotHash: snapshot.Hash,
			Questions: bytes.Clone(questions.Canonical), QuestionHash: questions.Hash, snapshot: snapshot, questions: questions}
		started := time.Now()
		answer, err := reviewer.Review(ctx, in)
		result := Result{Reviewer: reviewer.Identity(), InputSHA256: Hash(in.Snapshot), Authority: "none",
			DurationMS: time.Since(started).Milliseconds()}
		if Hash(in.Snapshot) != snapshot.Hash || Hash(in.Questions) != questions.Hash {
			err = reviewError(ErrInvalidAnswer, "reviewer modified its input")
		}
		if err == nil {
			err = validateAnswer(answer, questions)
		}
		result.Status, result.Answer, result.Error = classify(answer, err, questions)
		out.Results = append(out.Results, result)
	}
	return out
}

func classify(answer Answer, err error, questions FrozenQuestions) (string, *Answer, *Error) {
	if err != nil {
		var typed *Error
		if !errors.As(err, &typed) {
			typed = reviewError(ErrUnavailable, "%v", err)
		}
		return "error", nil, typed
	}
	if answer.Choice == "" || answer.Choice == questions.AbstainID() {
		return "abstained", &answer, nil
	}
	return "ok", &answer, nil
}

func validateAnswer(answer Answer, questions FrozenQuestions) error {
	valid := func(id string) bool { return questions.Has(id) || (id != "" && id == questions.AbstainID()) }
	if answer.Choice != "" && !valid(answer.Choice) {
		return reviewError(ErrInvalidAnswer, "choice %q is not an option of question set %s", answer.Choice, questions.Questions.ID)
	}
	for id, score := range answer.Scores {
		if !valid(id) {
			return reviewError(ErrInvalidAnswer, "score for %q, which is not an option", id)
		}
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return reviewError(ErrInvalidAnswer, "score for %q is %v; scores must be finite in [0,1]", id, score)
		}
	}
	if len(answer.Notes) > 2048 {
		return reviewError(ErrInvalidAnswer, "notes exceed 2048 bytes")
	}
	return nil
}

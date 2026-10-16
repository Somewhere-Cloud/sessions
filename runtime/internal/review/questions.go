package review

import (
	"encoding/json"
	"fmt"
	"strings"
)

// QuestionsSchema is the only question-set version this package accepts.
const QuestionsSchema = "sessions.review-questions/1"

// Option is one answer a reviewer may choose.
type Option struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// Questions is a caller-supplied closed set of options plus an optional
// abstention, which is not a lane state.
type Questions struct {
	Schema  string   `json:"schema"`
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Options []Option `json:"options"`
	Abstain *Option  `json:"abstain,omitempty"`
}

// FrozenQuestions is a validated question set with its canonical bytes.
type FrozenQuestions struct {
	Questions Questions
	Canonical []byte
	Hash      string
}

// DecodeQuestions validates encoded strictly and freezes it.
func DecodeQuestions(encoded []byte) (FrozenQuestions, error) {
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(encoded, &probe); err != nil {
		return FrozenQuestions{}, fmt.Errorf("questions are not a JSON object: %w", err)
	}
	if probe.Schema != QuestionsSchema {
		return FrozenQuestions{}, fmt.Errorf("questions schema %q is not supported; this version reads only %q", probe.Schema, QuestionsSchema)
	}
	var questions Questions
	if err := decodeStrict(encoded, &questions); err != nil {
		return FrozenQuestions{}, fmt.Errorf("questions: %w", err)
	}
	if err := validateQuestions(questions); err != nil {
		return FrozenQuestions{}, fmt.Errorf("questions: %w", err)
	}
	canonical, err := Canonical(encoded)
	if err != nil {
		return FrozenQuestions{}, fmt.Errorf("questions: %w", err)
	}
	return FrozenQuestions{Questions: questions, Canonical: canonical, Hash: Hash(canonical)}, nil
}

func validateQuestions(q Questions) error {
	if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.Version) == "" {
		return fmt.Errorf("id and version are required")
	}
	if len(q.Options) < 2 || len(q.Options) > 64 {
		return fmt.Errorf("need 2 to 64 options, got %d", len(q.Options))
	}
	seen := map[string]bool{}
	all := append([]Option(nil), q.Options...)
	if q.Abstain != nil {
		all = append(all, *q.Abstain)
	}
	for _, option := range all {
		if strings.TrimSpace(option.ID) == "" || strings.TrimSpace(option.Description) == "" {
			return fmt.Errorf("every option needs an id and a description")
		}
		if seen[option.ID] {
			return fmt.Errorf("option id %q is repeated", option.ID)
		}
		seen[option.ID] = true
	}
	return nil
}

// Has reports whether id is a state option (not the abstention).
func (q FrozenQuestions) Has(id string) bool {
	for _, option := range q.Questions.Options {
		if option.ID == id {
			return true
		}
	}
	return false
}

// AbstainID is the abstention option id, or "" when the set has none.
func (q FrozenQuestions) AbstainID() string {
	if q.Questions.Abstain == nil {
		return ""
	}
	return q.Questions.Abstain.ID
}

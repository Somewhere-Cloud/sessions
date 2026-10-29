package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// prepareNativeTarget records creation intent before the provider call. A lost
// creation acknowledgement cannot safely be retried: the native API has no
// caller-supplied idempotency key or lookup receipt for thread/new.
func (r *codexAppRunner) prepareNativeTarget(recordedID string, create func() (string, error), resume func(string) error) error {
	c := r.continuation
	if c != nil {
		if c.NativeCreation == "creating" {
			return errors.New("Codex target creation has an unknown prior outcome; inspect native threads and source history before intentionally creating another continuation; no new target was requested")
		}
		if c.NativeTargetID != "" {
			if recordedID != "" && recordedID != c.NativeTargetID {
				return errors.New("Codex continuation target differs from runner metadata; inspect both identities before recovery")
			}
			recordedID = c.NativeTargetID
		}
		if recordedID == "" && c.ProviderContext != "" {
			return errors.New("Codex continuation has import evidence but no recorded native target; inspect native threads and source history before intentionally creating another continuation; no new target was requested")
		}
	}
	if recordedID != "" {
		r.conversationID = recordedID
		return resume(recordedID)
	}
	if c != nil {
		c.SchemaVersion = state.ContinuationTargetSchemaVersion
		c.NativeCreation = "creating"
		if err := state.WriteContinuation(r.paths.Continuation, *c); err != nil {
			return fmt.Errorf("record Codex target creation intent: %w", err)
		}
	}
	id, err := create()
	if err != nil {
		return fmt.Errorf("create Codex native target (outcome may be unknown; inspect native threads before retrying): %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("Codex target creation returned no identity; inspect native threads before retrying")
	}
	r.conversationID = id
	if c != nil {
		c.NativeCreation, c.NativeTargetID = "created", id
		if err := state.WriteContinuation(r.paths.Continuation, *c); err != nil {
			return fmt.Errorf("record created Codex target %q before import; inspect this thread before recovery: %w", id, err)
		}
	}
	return nil
}

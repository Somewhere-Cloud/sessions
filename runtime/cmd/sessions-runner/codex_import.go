package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func (r *codexAppRunner) importContinuation(continuation *state.ContinuationContext, inject func(context.Context, string, []codexapp.ImportedMessage) error) error {
	continuation.ProviderContext = "applying"
	if err := state.WriteContinuation(r.paths.Continuation, *continuation); err != nil {
		return fmt.Errorf("mark Codex continuation applying: %w", err)
	}
	messages := make([]codexapp.ImportedMessage, 0, len(continuation.Messages))
	for _, message := range continuation.Messages {
		messages = append(messages, codexapp.ImportedMessage{Role: message.Role, Text: message.Text})
	}
	if err := inject(r.ctx, r.conversationID, messages); err != nil {
		if !errors.Is(err, codexapp.ErrImportNotSent) {
			return fmt.Errorf("Codex continuation import outcome is unknown; inspect target thread %q and source history before intentionally creating another continuation; history will not be injected again: %w", r.conversationID, err)
		}
		continuation.ProviderContext = ""
		if persistErr := state.WriteContinuation(r.paths.Continuation, *continuation); persistErr != nil {
			return fmt.Errorf("Codex history was not sent, but clearing the applying marker failed; inspect continuation state before retrying: %w", persistErr)
		}
		return err
	}
	continuation.ProviderContext = "applied"
	return nil
}

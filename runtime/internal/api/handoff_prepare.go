package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const handoffTextBytes = state.MaxHandoffTextBytes

func (s *Server) prepareHandoff(ctx context.Context, body handoffRequest) (handoffRecord, error) {
	if body.DestinationProvider != "claude" && body.DestinationProvider != "codex" {
		return handoffRecord{}, errors.New("destinationProvider must be claude or codex")
	}
	source, ok := s.registry.Get(body.SourceSessionID)
	if !ok {
		return handoffRecord{}, errors.New("source session not found; select its exact runtime")
	}
	info := source.Info()
	if info.Kind != state.KindClaudeStructured && info.Kind != state.KindCodexAppServer {
		return handoffRecord{}, errors.New("provider switching requires a structured Claude or Codex source")
	}
	historyID := body.HistoryID
	if historyID == "" {
		historyID = body.SourceSessionID
	}
	prepared, err := s.prepareContinuation(ctx, continuationRequest{PreserveAuthored: true, HistoryID: historyID, SourceSessionID: body.SourceSessionID, DestinationProvider: body.DestinationProvider})
	if err != nil {
		return handoffRecord{}, err
	}
	// Validate that an explicitly supplied history is actually this runtime's.
	history, err := s.integrationEndpoints.LookupHistory(s.registry.List(true), historyID)
	if err != nil {
		return handoffRecord{}, err
	}
	if (info.Tool == state.ToolClaude && prepared.context.SourceProvider != "claude") || (info.Tool == state.ToolCodex && prepared.context.SourceProvider != "codex") {
		return handoffRecord{}, errors.New("selected history belongs to a different source provider")
	}
	if _, err = s.continuationSource(body.SourceSessionID, history.ProviderSessionID); err != nil {
		return handoffRecord{}, err
	}
	model, name, effort, err := s.resolveContinuationModel(ctx, body.DestinationProvider, body.Model, body.Effort)
	if err != nil {
		return handoffRecord{}, err
	}
	c := prepared.context
	messages, coverage := boundedHandoffMessages(c.Messages)
	if prepared.skippedRecords > 0 {
		coverage.Limitation += fmt.Sprintf(" Source reader skipped %d unreadable records; source coverage is incomplete.", prepared.skippedRecords)
	}
	if len(messages) == 0 {
		return handoffRecord{}, fmt.Errorf("no complete authored message fits the %d-byte handoff bound; use an explicit reviewed briefing", handoffTextBytes)
	}
	c.Messages, c.Handoff, c.SchemaVersion = messages, true, state.ContinuationTargetSchemaVersion
	c.DestinationModel, c.DestinationModelName, c.DestinationEffort = model, name, effort
	return handoffRecord{Version: 1, Request: body, Artifact: c, Coverage: coverage, SourceTags: state.CloneTags(info.Tags), SourceDescription: info.Description, SourceDisplayParent: info.DisplayParentSessionID, Creation: "pending"}, nil
}

func boundedHandoffMessages(all []state.ContinuationMessage) ([]state.ContinuationMessage, handoffCoverage) {
	start, bytes := len(all), 0
	for start > 0 {
		size := len(all[start-1].Text)
		if bytes+size > handoffTextBytes {
			break
		}
		start--
		bytes += size
	}
	coverage := handoffCoverage{Included: len(all) - start, Omitted: start, Bytes: bytes,
		Limitation: "Only complete authored text and textual file references are transferred. Binary images/files, tool output and provider internals are not transferred. Referenced files remain on the source machine; reattach required assets. Older messages may be omitted by the fixed byte bound. Source history is unchanged."}
	return append([]state.ContinuationMessage(nil), all[start:]...), coverage
}

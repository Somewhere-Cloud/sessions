package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func (s *Server) handoffStatus(receipt handoffRecord) handoffView {
	view := handoffView{handoffRecord: receipt, Context: "pending", Response: "not-observed"}
	// Creation's existing ledger operation is authoritative even if a crash
	// interrupted writing our receipt after Create returned.
	var target *state.SessionInfo
	for _, info := range s.registry.List(true) {
		if info.ID == receipt.TargetSessionID || (info.Start != nil && info.Start.OperationID == receipt.Request.OperationID) {
			copy := s.withStartReceipt(info)
			target, view.TargetSessionID = &copy, info.ID
			break
		}
	}
	if _, running := s.handoffsRunning.Load(receipt.Request.OperationID); !running && view.Creation == "pending" {
		view.Creation = "unknown"
	}
	if view.TargetSessionID != "" {
		c, err := state.ReadContinuation(state.For(s.config.RunnerStateDir, view.TargetSessionID).Continuation)
		if err == nil && !sameHandoffArtifact(receipt.Artifact, c) {
			err = errors.New("target context differs from the saved handoff artifact; inspect both records before sending")
		}
		if err == nil {
			view.NativeTargetID = c.NativeTargetID
			if c.ProviderContext == "applying" {
				view.Context = "unknown"
			}
			if c.DestinationProvider == "claude" && c.ProviderContext == "dispatched" {
				view.Context = "sent-with-request"
			}
			if c.ProviderContext == "applied" {
				view.Context = "applied"
			}
			if c.DestinationProvider == "claude" && c.LocalHistoryReady && c.ProviderContext == "" {
				view.Context = "staged-for-first-request"
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			view.Context = "unknown"
			view.Error = "Read target context: " + err.Error()
		}
	}
	if _, running := s.handoffsRunning.Load(receipt.Request.OperationID); !running && view.Context == "pending" {
		view.Context = "unknown"
	}
	view.Prompt = s.startPrompt(handoffPromptID(receipt.Request.OperationID))
	if target != nil {
		if view.NativeTargetID == "" {
			view.NativeTargetID = target.ConversationID
			if view.NativeTargetID == "" {
				view.NativeTargetID = target.ClaudeSessionID
			}
		}
		if view.Creation == "unknown" && !target.Exited && !target.RunnerGone {
			view.Creation = "created"
		}
		if target.Start != nil && view.Prompt != nil && view.Prompt.Status == state.StartPromptAccepted {
			view.Activity = target.Start.Phase
			view.Response = s.handoffResponse(view.TargetSessionID, receipt.Request.OperationID)
		}
	}
	if view.Creation == "unknown" || view.Context == "unknown" || (view.Prompt != nil && (view.Prompt.Status == "unknown" || view.Prompt.Status == "unreadable")) {
		view.Recovery = "Inspect the target and source history before creating another handoff. Do not replay an uncertain import or first request. Read this operation again for later evidence."
	}
	return view
}

// First requests share the ordinary submit route and its durable content-bound
// operation receipt. No hidden prompt, retry queue or provider probe is added.
func (s *Server) handleHandoffPrompt(w http.ResponseWriter, r *http.Request, origin, id string) {
	path, err := s.handoffPath(id)
	var receipt handoffRecord
	if err == nil {
		receipt, err = readHandoff(path)
	}
	if err != nil {
		s.sendJSON(w, 404, map[string]string{"error": fmt.Sprint(err)}, origin)
		return
	}
	view := s.handoffStatus(receipt)
	if view.Creation != "created" || (view.Context != "applied" && view.Context != "staged-for-first-request" && view.Context != "sent-with-request") {
		s.sendJSON(w, 409, map[string]string{"error": "target context is not ready; inspect handoff status before sending"}, origin)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
		return
	}
	data, _ := json.Marshal(map[string]string{"data": body.Text, "operation_id": handoffPromptID(id)})
	forwarded := r.Clone(r.Context())
	copiedURL := *r.URL
	forwarded.URL = &copiedURL
	forwarded.URL.Path = "/api/sessions/" + view.TargetSessionID + "/submit"
	forwarded.Body = io.NopCloser(bytes.NewReader(data))
	forwarded.ContentLength = int64(len(data))
	s.ServeHTTP(w, forwarded)
}

// This is bounded observed evidence, not an inference from idle/completed. An
// evicted event remains not-observed rather than inventing a generated answer.
func (s *Server) handoffResponse(targetID, operationID string) string {
	session, ok := s.registry.Get(targetID)
	if !ok || s.deliveries == nil {
		return "not-observed"
	}
	record, err := s.deliveries.Get(handoffPromptID(operationID))
	if err != nil {
		return "not-observed"
	}
	for _, raw := range session.RetainedClaudeEvents() {
		var event struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Type != "assistant" || event.Subtype == "imported_message" || !handoffAssistantText(raw) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err == nil && at.UnixMilli() >= record.CreatedAtMS {
			return "assistant-observed"
		}
	}
	return "not-observed"
}

func sameHandoffArtifact(a, b state.ContinuationContext) bool {
	a.NativeCreation, a.NativeTargetID, a.ProviderContext, a.LocalHistoryReady = "", "", "", false
	b.NativeCreation, b.NativeTargetID, b.ProviderContext, b.LocalHistoryReady = "", "", "", false
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func handoffAssistantText(raw json.RawMessage) bool {
	var value struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var text string
	if json.Unmarshal(value.Message.Content, &text) == nil {
		return strings.TrimSpace(text) != ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(value.Message.Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return true
		}
	}
	return false
}

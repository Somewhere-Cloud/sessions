package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/recovery"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func (s *Server) handleHandoffRoute(w http.ResponseWriter, r *http.Request, origin string) bool {
	const root = "/api/handoffs"
	if r.URL.Path != root && !strings.HasPrefix(r.URL.Path, root+"/") {
		return false
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == root+"/preview":
		var body handoffRequest
		if err := readJSON(r, &body); err != nil {
			s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
			return true
		}
		receipt, err := s.prepareHandoff(r.Context(), body)
		if err != nil {
			s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
		} else {
			s.sendJSON(w, 200, receipt, origin)
		}
	case r.Method == http.MethodPost && r.URL.Path == root:
		s.handleHandoffStart(w, r, origin)
	case r.Method == http.MethodGet && r.URL.Path == root:
		s.listHandoffs(w, r, origin)
	case strings.HasPrefix(r.URL.Path, root+"/"):
		id := strings.TrimPrefix(r.URL.Path, root+"/")
		if r.Method == http.MethodPost && strings.HasSuffix(id, "/prompt") {
			s.handleHandoffPrompt(w, r, origin, strings.TrimSuffix(id, "/prompt"))
		} else if r.Method == http.MethodGet {
			s.handleHandoffGet(w, r, origin, id)
		} else {
			s.sendJSON(w, 405, map[string]string{"error": "method not allowed"}, origin)
		}
	default:
		s.sendJSON(w, 405, map[string]string{"error": "method not allowed"}, origin)
	}
	return true
}

func (s *Server) handleHandoffStart(w http.ResponseWriter, r *http.Request, origin string) {
	var body handoffRequest
	if err := readJSON(r, &body); err != nil {
		s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
		return
	}
	path, err := s.handoffPath(body.OperationID)
	if err != nil {
		s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
		return
	}
	handoffMu.Lock()
	defer handoffMu.Unlock()
	receipt, err := readHandoff(path)
	if err == nil {
		if receipt.Request != body {
			s.sendJSON(w, 409, map[string]string{"error": "operationId already names different handoff choices; inspect the original operation"}, origin)
			return
		}
		s.sendJSON(w, 200, s.handoffStatus(receipt), origin)
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		s.sendJSON(w, 409, map[string]string{"error": err.Error()}, origin)
		return
	}
	receipt, err = s.prepareHandoff(r.Context(), body)
	if err == nil {
		err = writeHandoff(path, receipt)
	}
	if err != nil {
		s.sendJSON(w, 400, map[string]string{"error": err.Error()}, origin)
		return
	}
	s.handoffsRunning.Store(body.OperationID, struct{}{})
	go s.runHandoff(path, receipt)
	s.sendJSON(w, http.StatusAccepted, s.handoffStatus(receipt), origin)
}

func (s *Server) runHandoff(path string, receipt handoffRecord) {
	defer s.handoffsRunning.Delete(receipt.Request.OperationID)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request, err := recovery.HandoffCreateRequest(receipt.Artifact, receipt.Request.OperationID, handoffPromptID(receipt.Request.OperationID))
	if err == nil {
		request.Tags, request.Description, request.DisplayParentSessionID = receipt.SourceTags, receipt.SourceDescription, receipt.SourceDisplayParent
		var info state.SessionInfo
		info, err = s.registry.Create(ctx, request)
		receipt.TargetSessionID = info.ID
	}
	receipt.Creation = "created"
	if err != nil {
		receipt.Creation, receipt.Error = "unknown", err.Error()
		var failed *state.StartCreateFailedError
		var replay *state.StartCreateReplayError
		if errors.As(err, &failed) {
			receipt.TargetSessionID = failed.SessionID
		}
		if errors.As(err, &replay) {
			receipt.TargetSessionID = replay.SessionID
		}
	}
	handoffMu.Lock()
	defer handoffMu.Unlock()
	// Failed outcome persistence leaves pending on disk; after restart it is
	// unknown and never launches another target automatically.
	if err := writeHandoff(path, receipt); err != nil {
		log.Printf("handoff %s outcome could not be persisted; inspect target %s before recovery: %v", receipt.Request.OperationID, receipt.TargetSessionID, err)
	}
}

func handoffPromptID(operation string) string {
	return restartOperationID("handoff-first-request:" + operation)
}

func (s *Server) handleHandoffGet(w http.ResponseWriter, r *http.Request, origin, id string) {
	path, err := s.handoffPath(id)
	var receipt handoffRecord
	if err == nil {
		receipt, err = readHandoff(path)
	}
	if err != nil {
		s.sendJSON(w, 404, map[string]string{"error": err.Error()}, origin)
		return
	}
	s.sendJSON(w, 200, s.handoffStatus(receipt), origin)
}

func (s *Server) listHandoffs(w http.ResponseWriter, r *http.Request, origin string) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		s.sendJSON(w, 400, map[string]string{"error": "sessionId is required"}, origin)
		return
	}
	path, _ := s.handoffPath("00000000-0000-4000-8000-000000000000")
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.sendJSON(w, 500, map[string]string{"error": err.Error()}, origin)
		return
	}
	views := []handoffView{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		receipt, e := readHandoff(filepath.Join(filepath.Dir(path), entry.Name()))
		if e != nil {
			s.sendJSON(w, 500, map[string]string{"error": "Saved handoff " + entry.Name() + " could not be read; operation discovery is incomplete. Inspect this receipt before starting another switch: " + e.Error()}, origin)
			return
		}
		view := s.handoffStatus(receipt)
		if receipt.Request.SourceSessionID == sessionID || view.TargetSessionID == sessionID {
			views = append(views, view)
		}
	}
	s.sendJSON(w, 200, views, origin)
}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// One daemon owns a state root. Serialize receipt replacement and duplicate
// starts here, independently of the slower runner/provider startup.
var handoffMu sync.Mutex

type handoffRequest struct {
	OperationID         string `json:"operationId"`
	SourceSessionID     string `json:"sourceSessionId"`
	HistoryID           string `json:"historyId,omitempty"`
	DestinationProvider string `json:"destinationProvider"`
	Model               string `json:"model,omitempty"`
	Effort              string `json:"effort,omitempty"`
}

type handoffCoverage struct {
	Included   int    `json:"included"`
	Omitted    int    `json:"omitted"`
	Bytes      int    `json:"bytes"`
	Limitation string `json:"limitation"`
}

type handoffRecord struct {
	Version             int                       `json:"version"`
	Request             handoffRequest            `json:"request"`
	Artifact            state.ContinuationContext `json:"artifact"`
	Coverage            handoffCoverage           `json:"coverage"`
	SourceTags          map[string]string         `json:"sourceTags,omitempty"`
	SourceDescription   string                    `json:"sourceDescription,omitempty"`
	SourceDisplayParent *string                   `json:"sourceDisplayParent,omitempty"`
	TargetSessionID     string                    `json:"targetSessionId,omitempty"`
	Creation            string                    `json:"creation"`
	Error               string                    `json:"error,omitempty"`
}

type handoffView struct {
	handoffRecord
	Context        string             `json:"context"`
	NativeTargetID string             `json:"nativeTargetId,omitempty"`
	Prompt         *state.StartPrompt `json:"prompt,omitempty"`
	Activity       string             `json:"activity,omitempty"`
	Response       string             `json:"response"`
	Recovery       string             `json:"recovery,omitempty"`
}

func (s *Server) handoffPath(id string) (string, error) {
	if id == "" {
		return "", errors.New("operationId is required")
	}
	if err := state.ValidateStartOperationIDs(id, ""); err != nil {
		return "", err
	}
	root := s.config.StateRoot
	if root == "" {
		root = s.config.UserStateRoot
	}
	return filepath.Join(root, "handoffs", id+".json"), nil
}

func readHandoff(path string) (handoffRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return handoffRecord{}, err
	}
	var r handoffRecord
	if len(data) > 512*1024 {
		return r, errors.New("handoff receipt exceeds size limit")
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	if r.Version != 1 {
		return r, fmt.Errorf("unsupported handoff receipt version %d", r.Version)
	}
	return r, r.Artifact.Validate()
}

func writeHandoff(path string, r handoffRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return errors.New("handoff receipt exceeds size limit; reduce the source metadata or use a reviewed briefing")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".handoff-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

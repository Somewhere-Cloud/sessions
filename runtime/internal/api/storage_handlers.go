package api

import (
	"context"
	"net/http"
	"os"

	"github.com/somewhere-tech/sessions/runtime/internal/storage"
)

type storageService interface {
	StorageInputs(context.Context) ([]storage.Seed, []storage.Reference, error)
}

func (s *Server) handleStorageRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	if request.URL.Path != "/api/storage" && request.URL.Path != "/api/storage/preview" {
		return false
	}
	if request.Method != http.MethodGet {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	service, ok := s.registry.(storageService)
	if !ok {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "storage inventory unavailable on this runtime; update the selected host"}, corsOrigin)
		return true
	}
	if s.identityError != nil || s.identity.ID == "" {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "storage machine identity unavailable; check the selected host"}, corsOrigin)
		return true
	}
	if !s.storageScan.TryLock() {
		s.sendJSON(response, http.StatusTooManyRequests, map[string]any{"error": "storage scan already running on this machine; wait and retry"}, corsOrigin)
		return true
	}
	ctx, cancel := context.WithTimeout(request.Context(), storage.Budget)
	defer cancel()
	type answer struct {
		report storage.Report
		err    error
	}
	done := make(chan answer, 1)
	// The slot stays occupied if an OS read stalls beyond the request budget.
	// No subsequent request can accumulate another blocked worker.
	go func() {
		defer s.storageScan.Unlock()
		report, err := s.storageReport(ctx, service)
		done <- answer{report, err}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "storage inventory could not read repository provenance; check host state and retry"}, corsOrigin)
		} else {
			s.sendJSON(response, http.StatusOK, result.report, corsOrigin)
		}
	case <-ctx.Done():
		s.sendJSON(response, http.StatusGatewayTimeout, map[string]any{"error": "storage scan canceled or timed out; narrow known project roots or retry after the current scan finishes"}, corsOrigin)
	}
	return true
}
func (s *Server) storageReport(ctx context.Context, service storageService) (storage.Report, error) {
	seeds, references, err := service.StorageInputs(ctx)
	if err != nil {
		return storage.Report{}, err
	}
	if s.projects != nil {
		projects, err := s.projects.List()
		if err != nil {
			return storage.Report{}, err
		}
		for _, project := range projects {
			for _, path := range project.Roots {
				seeds = append(seeds, storage.Seed{Path: path})
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return storage.Report{}, err
	}
	report := storage.Scan(ctx, seeds, references, []string{home})
	report.MachineID, report.MachineName = s.identity.ID, s.identity.Name
	return report, nil
}

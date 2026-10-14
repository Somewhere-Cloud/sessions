package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/storage"
)

type storageRegistry struct {
	*state.Registry
	block <-chan struct{}
	err   error
}

func (r *storageRegistry) StorageInputs(ctx context.Context) ([]storage.Seed, []storage.Reference, error) {
	if r.block != nil {
		<-r.block
	}
	return nil, nil, r.err
}
func TestStorageIdentityReadOnlyAndAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	daemon := newTestDaemon(t)
	registry := &storageRegistry{Registry: daemon.registry}
	daemon.handler.registry = registry
	daemon.handler.identity = machineIdentity{ID: "selected-remote", Name: "Selected workstation"}
	daemon.handler.identityError = nil
	for _, path := range []string{"/api/storage", "/api/storage/preview"} {
		response := serve(t, daemon.handler, http.MethodGet, path, nil, "127.0.0.1:1", nil)
		var report storage.Report
		decodeBody(t, response, &report)
		if response.Code != 200 || report.MachineID != "selected-remote" || !report.Preview {
			t.Fatalf("response=%d %s", response.Code, response.Body)
		}
		response = serve(t, daemon.handler, http.MethodPost, path, nil, "127.0.0.1:1", nil)
		if response.Code != 405 {
			t.Fatalf("write accepted: %d", response.Code)
		}
		response = serve(t, daemon.handler, http.MethodGet, path, nil, "198.51.100.1:1", nil)
		if response.Code != 401 {
			t.Fatalf("auth bypass: %d", response.Code)
		}
	}
	registry.err = errors.New("ledger broken")
	response := serve(t, daemon.handler, http.MethodGet, "/api/storage", nil, "127.0.0.1:1", nil)
	if response.Code != 500 {
		t.Fatalf("false empty success: %d", response.Code)
	}
}
func TestStorageCanceledWorkerRetainsConcurrencySlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	daemon := newTestDaemon(t)
	blocked := make(chan struct{})
	daemon.handler.registry = &storageRegistry{Registry: daemon.registry, block: blocked}
	daemon.handler.identity = machineIdentity{ID: "fixture", Name: "Fixture"}
	daemon.handler.identityError = nil
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/storage", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { daemon.handler.handleStorageRoute(response, request, ""); close(done) }()
	deadline := time.Now().Add(time.Second)
	for daemon.handler.storageScan.TryLock() {
		daemon.handler.storageScan.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if response.Code != 504 {
		t.Fatalf("cancel=%d", response.Code)
	}
	retry := serve(t, daemon.handler, http.MethodGet, "/api/storage", nil, "127.0.0.1:1", nil)
	if retry.Code != 429 {
		t.Fatalf("new blocked worker allowed=%d", retry.Code)
	}
	close(blocked)
	deadline = time.Now().Add(time.Second)
	for !daemon.handler.storageScan.TryLock() {
		if time.Now().After(deadline) {
			t.Fatal("slot did not release")
		}
		time.Sleep(time.Millisecond)
	}
	daemon.handler.storageScan.Unlock()
}

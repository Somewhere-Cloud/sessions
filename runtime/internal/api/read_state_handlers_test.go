package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestReadStateRouteMarksUnreadAndSeen(t *testing.T) {
	root := t.TempDir()
	config := state.Config{
		DefaultShell: "/bin/sh", DefaultCwd: root, DefaultCols: 120, DefaultRows: 40,
		StateRoot: filepath.Join(root, "state"), UserStateRoot: filepath.Join(root, "user-state"),
		RunnerStateDir: filepath.Join(root, "state", "runners"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}
	store, err := ledger.Open(context.Background(), ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := sessionruntime.NewManager(config, prototest.NewLauncher(), sessionruntime.ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
	})
	t.Cleanup(manager.Close)
	server := New(config, manager, manager.Push())
	body, _ := json.Marshal(state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root})
	created := serve(t, server, http.MethodPost, "/api/sessions", bytes.NewReader(body), "127.0.0.1:1", nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var session state.SessionInfo
	decodeBody(t, created, &session)
	if session.Unread || !strings.Contains(created.Body.String(), `"unread":false`) {
		t.Fatalf("a new session must report unread explicitly as false: %s", created.Body.String())
	}
	path := "/api/sessions/" + session.ID + "/read-state"

	marked := serve(t, server, http.MethodPut, path, strings.NewReader(`{"unread":true}`), "127.0.0.1:1", nil)
	if marked.Code != http.StatusOK || !strings.Contains(marked.Body.String(), `"unread":true`) {
		t.Fatalf("mark unread status=%d body=%s", marked.Code, marked.Body.String())
	}
	if !listedSession(t, manager, session.ID).Unread {
		t.Fatal("the unread mark was accepted but the listing does not report it")
	}
	metadata, err := state.ReadRunnerMetadata(filepath.Join(config.RunnerStateDir, session.ID+".json"))
	if err != nil || !metadata.MarkedUnread {
		t.Fatalf("the unread mark never reached runner metadata: %+v, %v", metadata, err)
	}

	seen := serve(t, server, http.MethodPut, path, strings.NewReader(`{"unread":false}`), "127.0.0.1:1", nil)
	if seen.Code != http.StatusOK || !strings.Contains(seen.Body.String(), `"unread":false`) {
		t.Fatalf("seen status=%d body=%s", seen.Code, seen.Body.String())
	}
	if info := listedSession(t, manager, session.ID); info.Unread || info.SeenAt == nil {
		t.Fatalf("seen did not clear unread or stamp seenAt: %+v", info)
	}

	if missing := serve(t, server, http.MethodPut, path, strings.NewReader(`{}`), "127.0.0.1:1", nil); missing.Code != http.StatusBadRequest {
		t.Fatalf("a body without unread status=%d", missing.Code)
	}
	if wrong := serve(t, server, http.MethodGet, path, nil, "127.0.0.1:1", nil); wrong.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET read-state status=%d", wrong.Code)
	}
	unknown := serve(t, server, http.MethodPut, "/api/sessions/cccccccc-dddd-4eee-8fff-0000000000c1/read-state",
		strings.NewReader(`{"unread":true}`), "127.0.0.1:1", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown session status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

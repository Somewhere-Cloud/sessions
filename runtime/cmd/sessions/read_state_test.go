package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func readStateFixtureDaemon(t *testing.T, id string, unread *bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/sessions":
			_, _ = response.Write([]byte(`{"sessions":[{"id":"` + id + `","name":"bolo","description":"","cmd":"codex",` +
				`"cwd":"/tmp","createdAt":1,"pid":12,"tool":"codex","working":false,"lastDataAt":1,` +
				`"lastUserMessageAt":null,"exited":false,"pinned":false,"unread":` + boolText(*unread) + `}]}`))
		case request.Method == http.MethodPut && request.URL.Path == "/api/sessions/"+id+"/read-state":
			var body struct {
				Unread *bool `json:"unread"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Unread == nil {
				t.Errorf("read-state body: %v %v", body.Unread, err)
				return
			}
			*unread = *body.Unread
			_, _ = response.Write([]byte(`{"unread":` + boolText(*unread) + `,"seenAt":42,"attentionAt":null}`))
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/verdict"):
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"error":"not found"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSeenAndUnreadChangeTheDaemonReadState(t *testing.T) {
	const id = "33000000-0000-4000-8000-000000000001"
	t.Setenv("HOME", t.TempDir())
	unread := false
	server := readStateFixtureDaemon(t, id, &unread)

	stdout, stderr, code := runOwnershipCLI(t, server.URL, "unread", id[:8])
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "unread") || !strings.Contains(stdout, "bolo") || !unread {
		t.Fatalf("unread exit=%d stdout=%q stderr=%q unread=%v", code, stdout, stderr, unread)
	}
	stdout, stderr, code = runOwnershipCLI(t, server.URL, "--json", "seen", id)
	if code != 0 || stderr != "" || unread {
		t.Fatalf("seen exit=%d stdout=%q stderr=%q unread=%v", code, stdout, stderr, unread)
	}
	assertCodeMatchesExit(t, "seen", code, stdout)
	var result readStateResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("undecodable seen document: %v (%q)", err, stdout)
	}
	if !result.OK || result.Unread || result.ID != id || result.SeenAt == nil || *result.SeenAt != 42 {
		t.Fatalf("seen document = %+v", result)
	}
	if _, _, code = runOwnershipCLI(t, server.URL, "seen"); code != 1 {
		t.Fatalf("seen with no session exit=%d", code)
	}
}

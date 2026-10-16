package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A restored record with no recorded activity reports its creation time as a
// floor and says so, instead of presenting the floor as observed activity.
func TestStatusJSONReportsUnknownActivity(t *testing.T) {
	root := t.TempDir()
	const id = "23000000-0000-4000-8000-000000000002"
	for _, unknown := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/sessions":
				_ = json.NewEncoder(response).Encode(map[string]any{"sessions": []any{map[string]any{
					"id": id, "name": "restored", "description": "", "cmd": "/bin/sh", "args": []string{},
					"cwd": root, "createdAt": int64(1_000), "lastDataAt": int64(1_000), "lastDataUnknown": unknown,
					"tool": "terminal", "exited": true, "exitedAt": int64(5_000),
				}}})
			case "/api/sessions/" + id + "/verdict":
				response.WriteHeader(http.StatusNotFound)
			default:
				http.NotFound(response, request)
			}
		}))
		t.Setenv("HOME", root)
		var stdout, stderr bytes.Buffer
		code := run([]string{"--host", server.URL, "--json", "status", id[:8]}, strings.NewReader(""), &stdout, &stderr)
		server.Close()
		if code != 0 {
			t.Fatalf("status exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		var output map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
			t.Fatal(err)
		}
		if got, _ := output["last_activity_unknown"].(bool); got != unknown {
			t.Fatalf("lastDataUnknown=%v: last_activity_unknown=%v\n%s", unknown, output["last_activity_unknown"], stdout.String())
		}
	}
}

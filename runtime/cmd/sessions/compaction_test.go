package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompactionCLIStatusAndToggles(t *testing.T) {
	current := compactionStatus{Lanes: []compactionLane{{
		ID: "11111111-2222-4333-8444-555555555555", Name: "Parser", Enabled: false, Reason: "idle compaction is off for this lane",
	}}}
	var puts []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/api/idle-compaction" && request.Method == http.MethodGet:
		case request.URL.Path == "/api/idle-compaction" && request.Method == http.MethodPut:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			current.Enabled = body.Enabled
			puts = append(puts, "global")
		case strings.HasPrefix(request.URL.Path, "/api/idle-compaction/lanes/") && request.Method == http.MethodPut:
			var body struct {
				Override string `json:"override"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			puts = append(puts, strings.TrimPrefix(request.URL.Path, "/api/idle-compaction/lanes/")+"="+body.Override)
		default:
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(current)
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "compaction"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("status exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "off by default") || !strings.Contains(stdout.String(), "11111111 Parser [default]") {
		t.Fatalf("status output = %q", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"--host", server.URL, "--json", "compaction", "on"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("on exit=%d stderr=%q", code, stderr.String())
	}
	var decoded compactionStatus
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || !decoded.Enabled {
		t.Fatalf("json output = %q (%v)", stdout.String(), err)
	}
	if code := run([]string{"--host", server.URL, "compaction", "default"}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("default without --lane succeeded")
	}
	if len(puts) != 1 || puts[0] != "global" {
		t.Fatalf("puts = %v", puts)
	}
}

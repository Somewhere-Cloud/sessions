package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutoArchiveCLIParity(t *testing.T) {
	enabled := false
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auto-archive" {
			t.Errorf("unexpected operation %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			enabled = body.Enabled
			puts++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "inactivityMs": 86400000, "scope": "machine-structured-sessions"})
	}))
	defer server.Close()
	for _, action := range []string{"status", "on", "off"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--host", server.URL, "--json", "auto-archive", action}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("%s exit=%d %s", action, code, stderr.String())
		}
		var result struct {
			Enabled      bool   `json:"enabled"`
			InactivityMS int64  `json:"inactivityMs"`
			Scope        string `json:"scope"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Enabled != (action == "on") || result.InactivityMS != 86400000 || result.Scope != "machine-structured-sessions" {
			t.Fatalf("%s: %s %v", action, stdout.String(), err)
		}
	}
	if puts != 2 {
		t.Fatalf("writes=%d", puts)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "auto-archive", "nonsense"}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("invalid action accepted")
	}
	if puts != 2 {
		t.Fatal("invalid action wrote policy")
	}
}

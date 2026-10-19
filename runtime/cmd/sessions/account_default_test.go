package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountDefaultCLI(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/account-default" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"note":"saved"}`))
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	for _, choice := range []string{"bean", "--cli-default"} {
		var out, stderr bytes.Buffer
		code := run([]string{"--host", server.URL, "accounts", "default", choice, "--tool", "claude"}, strings.NewReader(""), &out, &stderr)
		want := choice
		if choice == "--cli-default" {
			want = ""
		}
		if code != 0 || received["tool"] != "claude" || received["name"] != want {
			t.Fatalf("exit=%d received=%v error=%s", code, received, stderr.String())
		}
	}
}

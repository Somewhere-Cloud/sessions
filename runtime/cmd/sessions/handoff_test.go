package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandoffCLIStatusAndExplicitFirstRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "POST" {
			if r.URL.Path != "/api/handoffs/operation/prompt" {
				t.Error(r.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["text"] != "Continue." {
				t.Errorf("body %v %v", body, err)
			}
		} else if r.URL.Path != "/api/handoffs/operation" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"creation":"created","context":"unknown","prompt":{"status":"unknown","retry":false},"recovery":"Inspect target."}`))
	}))
	defer server.Close()
	for _, args := range [][]string{{"status", "operation"}, {"send", "operation", "--text", "Continue."}} {
		var out, errOut bytes.Buffer
		code := run(append([]string{"--host", server.URL, "--json", "handoff"}, args...), strings.NewReader(""), &out, &errOut)
		if code != 0 || !strings.Contains(out.String(), `"context": "unknown"`) || !strings.Contains(out.String(), "Inspect target.") {
			t.Fatalf("exit %d out %s err %s", code, &out, &errOut)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"--host", server.URL, "handoff", "send", "operation"}, strings.NewReader(""), &out, &errOut); code == 0 || calls != 2 {
		t.Fatal("implicit request sent")
	}
}

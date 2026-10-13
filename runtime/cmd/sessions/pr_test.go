package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPRCommandsUseTheDaemonContractAndFailOnRefreshError(t *testing.T) {
	const lane = "pr-lane-1"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/sessions" {
			_, _ = io.WriteString(response, `{"sessions":[]}`)
			return
		}
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch request.Method + " " + request.URL.Path {
		case "PUT /api/sessions/" + lane + "/pr":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["url"] != "https://github.com/acme/app/pull/9" {
				response.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(response, `{"error":"invalid pull request URL"}`)
				return
			}
			_, _ = io.WriteString(response, `{"session_id":"`+lane+`","linked":true,"changed":true,"runner":"live",
				"pull_request":{"url":"https://github.com/acme/app/pull/9","host":"github.com","owner":"acme","repo":"app","number":9,"linked_at":"2026-10-12T00:00:00Z"},
				"status":{"state":"unknown","draft":false,"checks":"unknown","checks_total":0,"checks_failing":0,"checks_pending":0,"mergeable":"unknown"},
				"freshness":"unknown","branch_match":"unknown","work":"unknown","closure":{"action":"none","reason":"unknown"}}`)
		case "POST /api/sessions/" + lane + "/pr/refresh":
			_, _ = io.WriteString(response, `{"session_id":"`+lane+`","linked":true,"runner":"live",
				"pull_request":{"url":"https://github.com/acme/app/pull/9","host":"github.com","owner":"acme","repo":"app","number":9,"linked_at":"2026-10-12T00:00:00Z"},
				"freshness":"unknown","work":"unknown","refresh":{"ok":false,"error_kind":"auth","error":"run gh auth login","recorded":false}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "--json", "pr", "link", lane, "https://github.com/acme/app/pull/9"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"changed": true`) {
		t.Fatalf("link exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--host", server.URL, "--json", "pr", "refresh", lane}, strings.NewReader(""), &stdout, &stderr)
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("refresh JSON must stay one document: %v stdout=%q", err, stdout.String())
	}
	if code != 1 || decoded["refresh"].(map[string]any)["error_kind"] != "auth" {
		t.Fatalf("refresh failure exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--host", server.URL, "pr", "link", lane}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("missing URL exit=%d stderr=%q", code, stderr.String())
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %q; a usage error must not reach the daemon", requests)
	}
}

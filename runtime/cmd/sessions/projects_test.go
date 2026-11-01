package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectsNameReportsSuggestionFailure(t *testing.T) {
	putCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/projects/suggest" {
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"error":"Sessions could not suggest this project: read projects: permission denied"}`))
			return
		}
		if request.Method == http.MethodPut {
			putCalled = true
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	folder := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "projects", "name", folder, "Work"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "could not name this project: Sessions could not suggest this project: read projects: permission denied") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if putCalled {
		t.Fatal("CLI tried to save a project without a trustworthy suggestion")
	}
}

func TestProjectsNewAndScratchPrintOnlyTheFolder(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(request.Body)
		bodies = append(bodies, request.URL.Path+" "+string(raw))
		switch request.URL.Path {
		case "/api/projects/new":
			_, _ = response.Write([]byte(`{"path":"/Users/me/Projects/Weather-Bot","name":"Weather Bot","git":false,"git_error":"git is not installed"}`))
		case "/api/projects/scratch":
			_, _ = response.Write([]byte(`{"path":"/Users/me/Projects/scratch/2026-10-31-try-ab12cd34","name":"No project"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "projects", "new", "Weather", "Bot"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stdout.String() != "/Users/me/Projects/Weather-Bot\n" || !strings.Contains(stderr.String(), "git is not installed") {
		t.Fatalf("new: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	code = run([]string{"--host", server.URL, "projects", "scratch", "try"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || stdout.String() != "/Users/me/Projects/scratch/2026-10-31-try-ab12cd34\n" {
		t.Fatalf("scratch: exit=%d stdout=%q", code, stdout.String())
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], `"name":"Weather Bot"`) || !strings.Contains(bodies[1], `"hint":"try"`) {
		t.Fatalf("requests = %q", bodies)
	}
	if code := run([]string{"--host", server.URL, "projects", "new"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("nameless new exited %d", code)
	}
}

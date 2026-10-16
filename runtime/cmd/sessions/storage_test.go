package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStorageUsesSelectedFleetRouteAndHasNoApply(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"machine_id":"machine-mini","machine_name":"Fixture workstation","preview":true,"complete":true,"roots":[],"totals":{"logical":0,"allocated":0,"confidence":"observed"},"issues":[],"limitations":[]}`))
	}))
	defer server.Close()
	scopeHome(t, server.URL)
	t.Setenv("SESSIONS_HOST", server.URL)
	for _, suffix := range [][]string{{"storage"}, {"storage", "preview"}} {
		args := append([]string{"--machine", "mini", "--json"}, suffix...)
		report := runScoped(t, args...)
		if report["machine_id"] != "machine-mini" {
			t.Fatalf("identity=%v", report)
		}
	}
	if strings.Join(requests, ",") != "GET /api/fleet/machine-mini/api/storage,GET /api/fleet/machine-mini/api/storage/preview" {
		t.Fatalf("route=%v", requests)
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"storage", "--apply"}, strings.NewReader(""), &out, &stderr); code == 0 {
		t.Fatal("apply accepted")
	}
	if len(requests) != 2 {
		t.Fatalf("apply reached daemon=%v", requests)
	}
}

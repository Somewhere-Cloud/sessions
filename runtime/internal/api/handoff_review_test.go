package api

import (
	"context"
	"encoding/json"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewHandoffIncludesLatestBeyondReaderCap(t *testing.T) {
	daemon, _, body := newHandoffFixture(t)
	path := filepath.Join(os.Getenv("HOME"), ".claude", "projects", watch.EncodeClaudeCWD(daemon.root), "11111111-2222-4333-8444-555555555555.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(strings.Repeat(" ", 32<<20) + "\n" + `{"type":"user","message":{"role":"user","content":"LATEST-REVIEW-REQUEST"}}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	receipt, err := daemon.handler.prepareHandoff(context.Background(), body)
	if err != nil {
		if !strings.Contains(err.Error(), "reader limit") {
			t.Fatal(err)
		}
		return
	}
	if !strings.Contains(receipt.Artifact.Messages[len(receipt.Artifact.Messages)-1].Text, "LATEST-REVIEW-REQUEST") {
		t.Fatalf("latest request lost; selected=%+v coverage=%+v", receipt.Artifact.Messages, receipt.Coverage)
	}
}
func TestReviewHandoffPreservesAuthoredWhitespace(t *testing.T) {
	daemon, _, body := newHandoffFixture(t)
	path := filepath.Join(os.Getenv("HOME"), ".claude", "projects", watch.EncodeClaudeCWD(daemon.root), "11111111-2222-4333-8444-555555555555.jsonl")
	text := "    indented code\n\n"
	raw, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := daemon.handler.prepareHandoff(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Artifact.Messages[0].Text != text {
		t.Fatalf("authored %q became %q", text, receipt.Artifact.Messages[0].Text)
	}
}
func TestReviewHandoffListDisclosesUnreadableReceipt(t *testing.T) {
	daemon, _, body := newHandoffFixture(t)
	receipt, err := daemon.handler.prepareHandoff(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := daemon.handler.handoffPath(body.OperationID)
	if err := writeHandoff(path, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	response := serve(t, daemon.handler, "GET", "/api/handoffs?sessionId="+body.SourceSessionID, nil, "127.0.0.1:1", nil)
	if response.Code == 200 && strings.TrimSpace(response.Body.String()) == "[]" {
		t.Fatal("unreadable saved operation silently returned as successful empty list")
	}
}

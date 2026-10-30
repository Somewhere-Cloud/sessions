package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/integrations"
)

// Controlled corpus: repeated searches must use fingerprints, then refresh
// only the conversation that changed. No provider files or processes.
func TestRankedSearchRepresentativeWarmLatency(t *testing.T) {
	fixture := &fakeHistory{transcript: make(map[string]integrations.TranscriptResponse)}
	for index := 0; index < 288; index++ {
		id := fmt.Sprintf("fixture-%03d", index)
		fixture.sessions = append(fixture.sessions, integrations.HistorySession{
			ID: id, Name: id, Tool: "claude", ConversationAvailable: true, SourceFingerprint: "fixture-v1",
		})
		messages := make([]integrations.TranscriptMessage, 64)
		for message := range messages {
			messages[message] = integrations.TranscriptMessage{Role: "user", Text: "migration marker " + strings.Repeat("sample ", 80)}
		}
		fixture.transcript[id] = integrations.TranscriptResponse{Messages: messages}
	}
	path := filepath.Join(t.TempDir(), "index.db")
	for pass := 0; pass < 2; pass++ {
		before := len(fixture.limits)
		start := time.Now()
		result, err := Run(context.Background(), fixture, nil, Options{Query: "migration", Ranked: true}, path)
		if err != nil {
			t.Fatal(err)
		}
		reads := len(fixture.limits) - before
		t.Logf("pass=%d conversations=288 messages=18432 elapsed=%s transcript_reads=%d", pass, time.Since(start), reads)
		if result.TotalHits != 18432 {
			t.Fatalf("hits=%d", result.TotalHits)
		}
		if pass == 1 && reads != 0 {
			t.Fatalf("warm search reread %d transcripts", reads)
		}
	}
	fixture.sessions[0].SourceFingerprint = "fixture-v2"
	before := len(fixture.limits)
	if _, err := Run(context.Background(), fixture, nil, Options{Query: "marker", Ranked: true}, path); err != nil {
		t.Fatal(err)
	}
	if reads := len(fixture.limits) - before; reads != 1 {
		t.Fatalf("changed search read %d transcripts, want 1", reads)
	}
}

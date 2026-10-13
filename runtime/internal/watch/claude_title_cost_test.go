package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func titleCostFixture(b *testing.B) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "conversation.jsonl")
	message, err := json.Marshal(map[string]any{
		"type": "assistant", "message": map[string]any{
			"content": []map[string]string{{"type": "text", "text": strings.Repeat("tool output ", 8000)}},
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	var transcript strings.Builder
	for i := 0; i < 32; i++ {
		transcript.Write(message)
		transcript.WriteByte('\n')
	}
	transcript.WriteString(`{"type":"custom-title","customTitle":"A recognizable conversation"}` + "\n")
	if err := os.WriteFile(path, []byte(transcript.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

func BenchmarkClaudeTitleWithLargeMessages(b *testing.B) {
	path := titleCostFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if title := claudeConversationTitle(path); title != "A recognizable conversation" {
			b.Fatalf("title = %q", title)
		}
	}
}

func TestClaudeTitlePreservesEscapedFieldsAndPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	content := strings.Join([]string{
		`{"type":"ai-title","aiTitle":"Earlier AI title"}`,
		`{"type":"custom-title","customTitle":"Earlier custom title"}`,
		`{"type":"assistant","message":{"content":"embedded title"},"customTitle":"Not a title"}`,
		`{"type":"custom-title","customTitle":"   "}`,
		`{"type":"custom-title","customTitle":42}`,
		`{"type":"custom-title","customTitle":"Before escaped","aiTitle":{"not":"a string"}}`,
		`{"type":"custom-title","customTitle":"Malformed"`,
		`{"type":"custom\u002dtitle","custom\u0054itle":"  Latest custom title  "}`,
		`{"type":"ai-title","aiTitle":"Latest AI title"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeConversationTitle(path); got != "Latest custom title" {
		t.Fatalf("title = %q", got)
	}
}

func TestClaudeTitleTailStartsInsideALargeRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	// The tail starts inside this valid message, not at a JSONL boundary.
	// Discard that fragment and retain the final title without a newline.
	body, err := json.Marshal(map[string]any{
		"type": "assistant", "message": strings.Repeat("x", claudeTitleBytes+1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	body = append(body, []byte(`{"type":"ai-title","aiTitle":"Tail title"}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeConversationTitle(path); got != "Tail title" {
		t.Fatalf("title after oversized record = %q", got)
	}
}

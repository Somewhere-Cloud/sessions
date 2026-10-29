package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
)

// AuthoredTranscriptContext preserves authored text instead of display formatting.
// A byte-limited prefix is never represented as the end of a conversation.
func (s *Service) AuthoredTranscriptContext(ctx context.Context, live []state.SessionInfo, id string, maxBytes int64) (TranscriptResponse, error) {
	h := s.history
	source, ok := h.find(live, id)
	if !ok || source.archived != nil {
		return TranscriptResponse{}, ErrHistoryNotFound
	}
	session, path, tool, err := h.describeSource(source, countNone)
	if err != nil {
		return TranscriptResponse{}, err
	}
	if path == "" || !session.ConversationAvailable {
		return TranscriptResponse{}, ErrHistoryNotFound
	}
	file, err := os.Open(path)
	if err != nil {
		return TranscriptResponse{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return TranscriptResponse{}, err
	}
	if int64(len(data)) > maxBytes {
		return TranscriptResponse{}, fmt.Errorf("source history exceeds the %d-byte handoff reader limit; use a reviewed briefing instead of an incomplete snapshot", maxBytes)
	}
	messages, skipped, err := authoredMessages(ctx, data, path, tool)
	return TranscriptResponse{SchemaVersion: SchemaVersion, Session: session, Messages: messages, SkippedRecords: skipped}, err
}

func authoredMessages(ctx context.Context, data []byte, path, tool string) ([]TranscriptMessage, int, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	var messages []TranscriptMessage
	skipped := 0
	for index := 0; ; index++ {
		if err := ctx.Err(); err != nil {
			return nil, skipped, err
		}
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				skipped++
			} else {
				messages = append(messages, authoredRecord(record, path, tool, index)...)
			}
		}
		if errors.Is(err, io.EOF) {
			return messages, skipped, nil
		}
		if err != nil {
			return nil, skipped, err
		}
	}
}

func authoredRecord(record map[string]any, path, tool string, index int) []TranscriptMessage {
	events := []watch.SessionEvent{record}
	if tool == "codex" {
		events = watch.NormalizeCodexRolloutLine(record, watch.CodexNormalizeContext{RolloutBasename: filepath.Base(path), LineIndex: index}).Events
	}
	var result []TranscriptMessage
	for _, event := range events {
		message, _ := event["message"].(map[string]any)
		role, _ := message["role"].(string)
		if role != "user" && role != "assistant" {
			continue
		}
		content := message["content"]
		if payload, ok := record["payload"].(map[string]any); tool == "codex" && ok && payload["type"] == "message" && payload["role"] == role {
			content = payload["content"]
		}
		text := authoredContent(content)
		if strings.TrimSpace(text) == "" || isHiddenProviderControlMessage(text) || (role == "user" && isSyntheticUserMessage(text)) {
			continue
		}
		result = append(result, TranscriptMessage{Role: role, Text: text, Timestamp: normalizedTimestamp(event["timestamp"])})
	}
	return result
}

func authoredContent(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var text strings.Builder
	blocks, _ := content.([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block["type"] == "text" || block["type"] == "input_text" || block["type"] == "output_text" {
			part, _ := block["text"].(string)
			text.WriteString(part)
		}
	}
	return text.String()
}

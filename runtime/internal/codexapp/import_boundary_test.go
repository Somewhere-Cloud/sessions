package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestImportAcceptanceBoundary(t *testing.T) {
	for _, outcome := range []string{"success", "disconnect", "timeout", "rpc-error", "partial-write", "closed", "cancelled", "validation"} {
		t.Run(outcome, func(t *testing.T) {
			c := &Client{pending: make(map[string]chan callResponse)}
			consumed := 0
			c.transport = steeringBoundaryTransport{write: func(data []byte) error {
				request, err := decodeJSONRPC(data)
				if err != nil {
					return err
				}
				if request.Method != "thread/inject_items" {
					t.Fatalf("method %s", request.Method)
				}
				consumed++
				switch outcome {
				case "success":
					c.handleResponse(wireMessage{ID: request.ID, Result: json.RawMessage(`{}`)})
				case "disconnect":
					c.fail(io.EOF)
				case "rpc-error":
					c.handleResponse(wireMessage{ID: request.ID, Error: &rpcError{Code: -32603, Message: "failed after consumption"}})
				case "partial-write":
					return io.ErrShortWrite
				}
				return nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			messages := []ImportedMessage{{Role: "user", Text: "context"}}
			local := outcome == "closed" || outcome == "cancelled" || outcome == "validation"
			if outcome == "closed" {
				c.closed = true
			}
			if outcome == "cancelled" {
				cancel()
			}
			if outcome == "validation" {
				messages[0].Role = "tool"
			}
			err := c.InjectMessages(ctx, "target", messages)
			want := 1
			if local {
				want = 0
			}
			if consumed != want || (err == nil) != (outcome == "success") || errors.Is(err, ErrImportNotSent) != local {
				t.Fatalf("consumed=%d err=%v", consumed, err)
			}
		})
	}
}

// Exercise the actual JSONL read loop when a fake app-server consumes an import
// and either disconnects without its acknowledgement or keeps the call pending.
func TestImportLostAcknowledgementFromAppServer(t *testing.T) {
	for _, disconnect := range []bool{true, false} {
		t.Run(map[bool]string{true: "disconnect", false: "timeout"}[disconnect], func(t *testing.T) {
			serverInput, clientInput := io.Pipe()
			clientOutput, serverOutput := io.Pipe()
			release := make(chan struct{})
			defer close(release)
			consumed := make(chan error, 1)
			go func() {
				defer serverOutput.Close()
				decoder := json.NewDecoder(serverInput)
				request, err := readTestRequest(decoder, "initialize")
				if err != nil {
					consumed <- err
					return
				}
				if err = json.NewEncoder(serverOutput).Encode(map[string]any{"id": request.ID, "result": map[string]any{}}); err != nil {
					consumed <- err
					return
				}
				if _, err = readTestRequest(decoder, "initialized"); err != nil {
					consumed <- err
					return
				}
				_, err = readTestRequest(decoder, "thread/inject_items")
				consumed <- err
				if !disconnect {
					<-release
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, err := newClient(ctx, clientInput, clientOutput, func() { clientInput.Close(); clientOutput.Close(); serverInput.Close() }, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			importCtx, stop := context.WithTimeout(ctx, 50*time.Millisecond)
			defer stop()
			err = client.InjectMessages(importCtx, "target", []ImportedMessage{{Role: "user", Text: "consumed context"}})
			if err == nil || errors.Is(err, ErrImportNotSent) {
				t.Fatalf("ambiguous consumption classified as %v", err)
			}
			if serverErr := <-consumed; serverErr != nil {
				t.Fatal(serverErr)
			}
		})
	}
}

func TestHandoffFakeNativeImportCarriesExactTextIntoFollowups(t *testing.T) {
	c := &Client{pending: make(map[string]chan callResponse), turns: make(map[string]*turnState), convs: map[string]conversationDefaults{"target": {}}}
	memory := ""
	imports, turns := 0, 0
	want := "  Inherited fact: octopus-42. File: '/tmp/design notes.md'\n"
	c.transport = steeringBoundaryTransport{write: func(data []byte) error {
		request, err := decodeJSONRPC(data)
		if err != nil {
			return err
		}
		switch request.Method {
		case "thread/inject_items":
			imports++
			var p struct {
				ThreadID string `json:"threadId"`
				Items    []struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"items"`
			}
			if err := json.Unmarshal(request.Params, &p); err != nil {
				return err
			}
			if p.ThreadID != "target" || len(p.Items) != 1 || len(p.Items[0].Content) != 1 {
				return errors.New("wrong import target/items")
			}
			memory = p.Items[0].Content[0].Text
			if memory != want {
				return errors.New("authored whitespace or file reference changed")
			}
			c.handleResponse(wireMessage{ID: request.ID, Result: json.RawMessage(`{}`)})
		case "turn/start":
			turns++
			if memory != want {
				return errors.New("followup lost inherited context")
			}
			var p TurnStartParams
			if err := json.Unmarshal(request.Params, &p); err != nil {
				return err
			}
			if p.ThreadID != "target" || len(p.Input) != 1 {
				return errors.New("wrong followup target")
			}
			id := fmt.Sprintf("turn-%d", turns)
			result, _ := json.Marshal(map[string]any{"turn": map[string]any{"id": id, "status": "inProgress", "items": []any{}}})
			c.handleResponse(wireMessage{ID: request.ID, Result: result})
			item, _ := json.Marshal(map[string]any{"threadId": "target", "turnId": id, "item": map[string]any{"id": "answer", "type": "agentMessage", "text": memory, "phase": "final"}})
			c.handleNotification("item/completed", item)
			completed, _ := json.Marshal(map[string]any{"threadId": "target", "turn": map[string]any{"id": id, "status": "completed", "items": []any{}}})
			c.handleNotification("turn/completed", completed)
		default:
			return errors.New("unexpected fake native method")
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.InjectMessages(ctx, "target", []ImportedMessage{{Role: "user", Text: want}}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Continue.", "What was the inherited fact?"} {
		stream, err := c.SendUserTurn(ctx, "target", text)
		if err != nil {
			t.Fatal(err)
		}
		result, err := stream.Result(ctx)
		if err != nil || result.Message != want {
			t.Fatalf("fake answer %+v %v", result, err)
		}
		for range stream.Events {
		}
	}
	if imports != 1 || turns != 2 {
		t.Fatalf("imports %d turns %d", imports, turns)
	}
}

package codexapp

import (
	"context"
	"encoding/json"
	"errors"
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

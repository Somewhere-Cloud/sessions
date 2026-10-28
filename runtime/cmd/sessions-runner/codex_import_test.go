package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func importFixture() state.ContinuationContext {
	return state.ContinuationContext{SchemaVersion: 1, SourceHistoryID: "source", SourceProvider: "claude", DestinationProvider: "codex", Mode: state.ContinuationNativeImport, Messages: []state.ContinuationMessage{{Role: "user", Text: "remember"}}}
}

func TestContinuationImportDurableBoundary(t *testing.T) {
	for _, outcome := range []string{"success", "not-sent", "lost-ack", "finish-write-failure", "clear-write-failure", "intent-write-failure"} {
		t.Run(outcome, func(t *testing.T) {
			r := newCodexTestRunner(t)
			defer r.closeHistory()
			value := importFixture()
			calls := 0
			if outcome == "intent-write-failure" {
				if err := os.Mkdir(r.paths.Continuation, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := r.importContinuation(&value, func(context.Context, string, []codexapp.ImportedMessage) error {
				calls++
				persisted, e := state.ReadContinuation(r.paths.Continuation)
				if e != nil || persisted.ProviderContext != "applying" {
					t.Fatalf("intent: %+v %v", persisted, e)
				}
				if outcome == "clear-write-failure" {
					if e := os.Mkdir(r.paths.Continuation+".tmp", 0700); e != nil {
						t.Fatal(e)
					}
					return codexapp.ErrImportNotSent
				}
				if outcome == "not-sent" {
					return codexapp.ErrImportNotSent
				}
				if outcome == "lost-ack" {
					return io.EOF
				}
				return nil
			})
			if outcome == "intent-write-failure" {
				if err == nil || calls != 0 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				return
			}
			if outcome == "clear-write-failure" {
				if err == nil {
					t.Fatal("hidden persistence failure")
				}
				persisted, e := state.ReadContinuation(r.paths.Continuation)
				if e != nil || persisted.ProviderContext != "applying" {
					t.Fatalf("lost intent: %+v %v", persisted, e)
				}
				return
			}
			if outcome == "success" {
				if err != nil || value.ProviderContext != "applied" {
					t.Fatalf("%+v %v", value, err)
				}
				value.LocalHistoryReady = true
				if e := state.WriteContinuation(r.paths.Continuation, value); e != nil {
					t.Fatal(e)
				}
			}
			if outcome == "finish-write-failure" {
				if e := os.Mkdir(r.paths.Continuation+".tmp", 0700); e != nil {
					t.Fatal(e)
				}
				if e := state.WriteContinuation(r.paths.Continuation, value); e == nil {
					t.Fatal("finish write unexpectedly succeeded")
				}
			}
			persisted, e := state.ReadContinuation(r.paths.Continuation)
			if e != nil {
				t.Fatal(e)
			}
			if outcome == "not-sent" {
				if persisted.ProviderContext != "" || !errors.Is(err, codexapp.ErrImportNotSent) {
					t.Fatalf("%+v %v", persisted, err)
				}
				return
			}
			// A failed finish write or crash leaves the on-disk applying marker intact.
			restarted := &codexAppRunner{paths: r.paths}
			restartErr := restarted.prepareContinuation()
			if outcome == "success" {
				if restartErr != nil {
					t.Fatal(restartErr)
				}
			} else if restartErr == nil || persisted.ProviderContext != "applying" {
				t.Fatalf("restart %+v %v", persisted, restartErr)
			}
			if calls != 1 {
				t.Fatalf("injected %d times", calls)
			}
		})
	}
}

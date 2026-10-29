package main

import (
	"context"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"io"
	"os"
	"testing"
)

func TestContinuationTargetCreationRestart(t *testing.T) {
	for _, outcome := range []string{"created", "lost-ack", "empty-id", "intent-write-failure", "receipt-write-failure"} {
		t.Run(outcome, func(t *testing.T) {
			r := newCodexTestRunner(t)
			defer r.closeHistory()
			c := importFixture()
			r.continuation = &c
			calls := 0
			if outcome == "intent-write-failure" {
				if err := os.Mkdir(r.paths.Continuation, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := r.prepareNativeTarget("", func() (string, error) {
				calls++
				disk, err := state.ReadContinuation(r.paths.Continuation)
				if err != nil || disk.NativeCreation != "creating" || disk.SchemaVersion != 3 {
					t.Fatalf("intent %+v %v", disk, err)
				}
				if outcome == "lost-ack" {
					return "", io.EOF
				}
				if outcome == "empty-id" {
					return "", nil
				}
				if outcome == "receipt-write-failure" {
					if err := os.Mkdir(r.paths.Continuation+".tmp", 0700); err != nil {
						t.Fatal(err)
					}
				}
				return "native-target", nil
			}, func(string) error { t.Fatal("unexpected resume"); return nil })
			if outcome == "intent-write-failure" {
				if err == nil || calls != 0 {
					t.Fatalf("calls %d err %v", calls, err)
				}
				return
			}
			if (err == nil) != (outcome == "created") {
				t.Fatalf("unexpected result: %v", err)
			}
			disk, err := state.ReadContinuation(r.paths.Continuation)
			if err != nil {
				t.Fatal(err)
			}
			restarted := &codexAppRunner{paths: r.paths, continuation: &disk}
			resumes := 0
			err = restarted.prepareNativeTarget("", func() (string, error) { t.Fatal("creation replayed"); return "", nil }, func(id string) error {
				resumes++
				if id != "native-target" {
					t.Fatalf("wrong target %q", id)
				}
				return nil
			})
			if outcome == "created" {
				if err != nil || resumes != 1 {
					t.Fatalf("resumes %d err %v", resumes, err)
				}
			} else if err == nil || resumes != 0 {
				t.Fatalf("unknown creation resumed: %v", err)
			}
			if calls != 1 {
				t.Fatalf("created %d times", calls)
			}
		})
	}
}

func TestContinuationTargetLegacyAndConflict(t *testing.T) {
	for _, phase := range []string{"applying", "applied"} {
		c := importFixture()
		c.ProviderContext = phase
		r := &codexAppRunner{continuation: &c}
		if err := r.prepareNativeTarget("", nil, nil); err == nil {
			t.Fatal("legacy unknown target allocated")
		}
		if err := r.prepareNativeTarget("recorded", nil, func(id string) error {
			if id != "recorded" {
				t.Fatal(id)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	c := importFixture()
	c.SchemaVersion, c.NativeCreation, c.NativeTargetID = 3, "created", "target"
	r := &codexAppRunner{continuation: &c}
	if err := r.prepareNativeTarget("different", nil, nil); err == nil {
		t.Fatal("identity conflict accepted")
	}
}

func TestContinuationTargetIdentityPrecedesImportAndSurvivesLostAck(t *testing.T) {
	r := newCodexTestRunner(t)
	defer r.closeHistory()
	c := importFixture()
	r.continuation = &c
	creates, imports := 0, 0
	if err := r.prepareNativeTarget("", func() (string, error) { creates++; return "persisted-target", nil }, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.writeMetadata(); err != nil {
		t.Fatal(err)
	}
	err := r.importContinuation(&c, func(_ context.Context, id string, _ []codexapp.ImportedMessage) error {
		imports++
		metadata, err := state.ReadRunnerMetadata(r.paths.Meta)
		if err != nil || metadata.Info.ConversationID != id || id != "persisted-target" {
			t.Fatalf("metadata before injection: %+v %v", metadata, err)
		}
		return io.EOF
	})
	if err == nil {
		t.Fatal("lost import ack became success")
	}
	disk, err := state.ReadContinuation(r.paths.Continuation)
	if err != nil || disk.NativeTargetID != "persisted-target" || disk.ProviderContext != "applying" {
		t.Fatalf("receipt %+v %v", disk, err)
	}
	restarted := &codexAppRunner{paths: r.paths, continuation: &disk}
	if err := restarted.prepareNativeTarget("persisted-target", func() (string, error) { t.Fatal("created duplicate"); return "", nil }, func(id string) error {
		if id != "persisted-target" {
			t.Fatal(id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.prepareContinuation(); err == nil {
		t.Fatal("ambiguous import replayed")
	}
	if creates != 1 || imports != 1 {
		t.Fatalf("creates %d imports %d", creates, imports)
	}
}

package search

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRankedSearchCancelledWhileWaitingForIndex(t *testing.T) {
	rankedSearchGate <- struct{}{}
	defer func() { <-rankedSearchGate }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, rankedFixture("marker"), nil, Options{Query: "marker", Ranked: true}, filepath.Join(t.TempDir(), "index.db"))
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled search: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled search still waiting for another index refresh")
	}
}

func TestRankedSearchCancelledWaitDoesNotConsumeGate(t *testing.T) {
	rankedSearchGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acquireRankedSearch(ctx); !errors.Is(err, context.Canceled) {
		<-rankedSearchGate
		t.Fatalf("cancelled wait: %v", err)
	}
	<-rankedSearchGate
	next, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := acquireRankedSearch(next); err != nil {
		t.Fatalf("next search blocked: %v", err)
	}
	<-rankedSearchGate
}

package state

import (
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func TestRetryMetadataDoesNotInventObservedWorking(t *testing.T) {
	for _, kind := range []string{KindClaudeStructured, KindCodexAppServer} {
		for _, working := range []bool{false, true} {
			s := &Session{}
			info := SessionInfo{Kind: kind, Working: working, Retry: &proto.ProviderRetry{}}
			o := s.observationsLocked(info)
			if o.Working == nil || o.Working.Value != working || o.Working.Source != WorkingFromProviderStream {
				t.Fatalf("working=%v with retained retry: %+v", working, o.Working)
			}
			if info.Retry == nil {
				t.Fatal("projection erased retry metadata")
			}
		}
	}
}

func TestRetryMetadataDoesNotInventClassifierEvidence(t *testing.T) {
	s := &Session{}
	o := s.observationsLocked(SessionInfo{Retry: &proto.ProviderRetry{}})
	if o.Working != nil {
		t.Fatalf("retry metadata manufactured classifier evidence: %+v", o.Working)
	}
}

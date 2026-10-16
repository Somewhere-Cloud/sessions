package pullrequest

import (
	"strings"
	"testing"
)

// A merged pull request says only that it landed. No closure reason, for any
// work state or runner state, claims the lane's task or work is done.
func TestClosureNeverClaimsTheTaskIsDone(t *testing.T) {
	for _, work := range []string{WorkOpen, WorkMerged, WorkClosedUnmerged, WorkUnknown} {
		for _, runner := range []string{RunnerLive, RunnerNotConnected, RunnerEnded} {
			reason := strings.ToLower(closure(work, runner, "lane").Reason)
			for _, claim := range []string{"work is done", "task is done.", "work is complete", "completes the", "lane's work is still open"} {
				if strings.Contains(reason, claim) {
					t.Fatalf("closure(%s, %s) = %q claims %q", work, runner, reason, claim)
				}
			}
		}
	}
}

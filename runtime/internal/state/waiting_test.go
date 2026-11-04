package state

import "testing"

func TestWaitingForNamesTheQuestionOrThePermission(t *testing.T) {
	cases := []struct {
		info SessionInfo
		want string
		ok   bool
	}{
		{SessionInfo{IdleReason: IdleReasonNeedsInput, IdleDetail: " Which branch? "}, "Which branch?", true},
		{SessionInfo{IdleReason: IdleReasonNeedsInput}, "needs input", true},
		{SessionInfo{PendingApproval: &ApprovalPrompt{ID: "a", Command: "rm -rf build"}}, "needs permission for rm -rf build", true},
		{SessionInfo{IdleReason: IdleReasonNeedsInput, Exited: true}, "", false},
		{SessionInfo{IdleReason: IdleReasonCompleted}, "", false},
	}
	for _, c := range cases {
		got, ok := WaitingFor(c.info)
		if got != c.want || ok != c.ok {
			t.Fatalf("WaitingFor(%+v) = %q, %v; want %q, %v", c.info, got, ok, c.want, c.ok)
		}
	}
}

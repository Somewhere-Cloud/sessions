package state

import "strings"

// WaitingFor says what a live session is waiting on a person for: the
// question it asked, or the permission it requested. ok is false when it is
// not waiting, including every ended session.
func WaitingFor(info SessionInfo) (text string, ok bool) {
	if info.Exited {
		return "", false
	}
	if approval := info.PendingApproval; approval != nil {
		what := firstNonBlank(approval.Summary, approval.Command, approval.Kind, "an action")
		return "needs permission for " + what, true
	}
	if info.IdleReason == IdleReasonNeedsInput {
		return firstNonBlank(info.IdleDetail, "needs input"), true
	}
	return "", false
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

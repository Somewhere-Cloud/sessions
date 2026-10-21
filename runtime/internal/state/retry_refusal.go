package state

import "strings"

// continueRefusalAdvice is the next safe step after a Claude runner declined
// to continue saved messages. It reads only the runner's own answer, never
// its version: the answer is the evidence of what the runner can do. Sessions
// takes no action on it; restarting a runtime would leave its saved messages
// behind, because the queue belongs to that runtime.
func continueRefusalAdvice(reason string) string {
	switch {
	case strings.Contains(reason, "has not exited"):
		// The runner already said what to do.
		return ""
	case reason == "Claude turn is active":
		return "Claude's runner still has a turn open. Wait for it to finish, or interrupt it, then use Retry again"
	case strings.Contains(reason, "saved next-turn message"):
		return "Claude's runner has no saved message waiting to be dispatched. Messages it already dispatched are never sent again; " +
			"read the conversation to see whether Claude answered them, and send a new message only for anything still needed"
	case strings.Contains(reason, "no failed turn to retry"):
		return "This answer does not mention saved messages, which is how a runner without a next-turn queue answers, so this runner " +
			"cannot continue saved messages. Read the conversation and send a new message for anything still needed"
	}
	return "Read the conversation before sending anything again"
}

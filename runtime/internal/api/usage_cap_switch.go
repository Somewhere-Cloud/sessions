package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/limitrecovery"
	"github.com/somewhere-tech/sessions/runtime/internal/recovery"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/usagecap"
)

// usageCapSwitchWait bounds one switch: copying the conversation, starting
// the new session, and handing it the continue message.
const usageCapSwitchWait = 3 * time.Minute

// usageCapHoldUntil is when a lane's account drops below the cap again, for
// limit recovery: a resume is never sent while the account is still over it.
func (s *Server) usageCapHoldUntil(ctx context.Context, lane limitrecovery.Lane) (time.Time, bool) {
	policy, err := s.usageCapPolicy()
	if err != nil || !s.laneCapped(lane.ID, policy) {
		return time.Time{}, false
	}
	reading := s.capRead(ctx, lane.Tool, lane.Account, usageCapReadWait)
	verdict := usagecap.Evaluate(reading, policy.cap(lane.Tool), time.Now())
	if verdict.State != usagecap.StateOver || verdict.ResetsAtMS == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(verdict.ResetsAtMS), true
}

// usageCapSwitchTarget names another signed-in account of the same provider
// with headroom for a session waiting on a usage limit or the cap.
func (s *Server) usageCapSwitchTarget(ctx context.Context, lane limitrecovery.Lane) (limitrecovery.Target, bool) {
	policy, err := s.usageCapPolicy()
	if err != nil || !policy.switch_ || s.usageCap == nil {
		return limitrecovery.Target{}, false
	}
	if s.usageCap.store != nil {
		if value, ok := s.usageCap.store.Override(lane.ID); ok && !value {
			return limitrecovery.Target{}, false
		}
	}
	current, ok := s.registry.Get(lane.ID)
	if !ok || !current.Info().MessageSubmit {
		return limitrecovery.Target{}, false
	}
	reading := s.capRead(ctx, lane.Tool, lane.Account, usageCapGateWait)
	target, found := s.capTarget(ctx, reading, policy, usageCapGateWait)
	if !found {
		return limitrecovery.Target{}, false
	}
	return limitrecovery.Target{Account: target.Reading.Name, Note: switchNote(reading, target.Reading, policy, time.Now())}, true
}

// switchNote is the line the conversation shows, such as "Switched to account
// work (personal at 92% of weekly, resets Tue 09:00)."
func switchNote(from, to usagecap.Reading, policy usageCapPolicy, now time.Time) string {
	why := capAccountLabel(from) + " reached its usage limit"
	if verdict := usagecap.Evaluate(from, policy.switchCap(from.Tool), now); verdict.State == usagecap.StateOver {
		why = capAccountLabel(from) + " at " + verdict.Detail
	}
	return fmt.Sprintf("Switched to account %s (%s).", capAccountLabel(to), why)
}

// usageCapSwitch continues a waiting session's conversation on another
// account: a new session in that account's own provider home, with the
// conversation copied the way `sessions fork --profile` copies it, then the
// continue message. Credentials are never copied, and the original session
// is left as it was.
func (s *Server) usageCapSwitch(ctx context.Context, lane limitrecovery.Lane, target limitrecovery.Target, operationID string) limitrecovery.Switched {
	ctx, cancel := context.WithTimeout(ctx, usageCapSwitchWait)
	defer cancel()
	result, err := s.forkOnAccount(ctx, lane.ID, target.Account)
	if err != nil && result.LaneID == "" {
		return limitrecovery.Switched{Detail: "Could not continue on another account (" + err.Error() + "); waiting for the limit to reset instead."}
	}
	newID := result.LaneID
	if err != nil {
		return limitrecovery.Switched{State: limitrecovery.StateFailed, TargetID: newID,
			Detail: fmt.Sprintf("Started session %s on another account, but it did not finish: %v. Inspect it before continuing.", newID, err)}
	}
	s.noteUsageCapSwitch(lane, target, newID)
	text := target.Note + " " + limitrecovery.ContinueText
	outcome, detail := limitrecovery.StateFailed, "the new session did not start accepting messages"
	if s.awaitMessageSubmit(ctx, newID) {
		if unlock, lockErr := s.submits.lockContext(ctx, newID); lockErr == nil {
			outcome, detail = s.submitAutomationText(ctx, newID, operationID, text, limitRecoveryAutomation)
			unlock()
		} else {
			detail = "the new session's input stayed busy: " + lockErr.Error()
		}
	}
	summary := fmt.Sprintf("%s Continued in session %s.", target.Note, shortID(newID))
	switched := limitrecovery.Switched{State: limitrecovery.StateSwitched, TargetID: newID, Text: text, OperationID: operationID, Detail: summary}
	if outcome == limitrecovery.StateFailed {
		switched.Text = ""
		switched.Detail += " The continue message was not sent (" + detail + "); open the new session and send it yourself."
	} else if outcome != limitrecovery.StateSent {
		switched.Detail += " The continue message was not confirmed (" + detail + "); check the new session before sending anything."
	}
	return switched
}

// forkOnAccount copies a session's conversation into a new session on
// account, the same operation as `sessions fork ID --profile ACCOUNT`.
func (s *Server) forkOnAccount(ctx context.Context, sourceID, account string) (recovery.AdoptResult, error) {
	recoveryMutationMu.Lock()
	defer recoveryMutationMu.Unlock()
	body := recoveryForkRequest{SourceSessionID: sourceID, ContextMode: "conversation", Profile: &account}
	candidates := s.registry.List(true)
	candidate, live, failure := s.recoveryForkCandidate(sourceID, candidates)
	if failure != nil {
		return recovery.AdoptResult{}, errors.New(failure.message)
	}
	plan, failure := s.resolveRecoveryForkPlan(ctx, candidate, live, body)
	if failure != nil {
		return recovery.AdoptResult{}, errors.New(failure.message)
	}
	continuation, failure := s.recoveryForkContinuation(candidate, candidates, plan, body)
	if failure != nil {
		return recovery.AdoptResult{}, errors.New(failure.message)
	}
	continuation.MainCollaborator, continuation.DestinationProfile = true, &account
	return recovery.ForkConversation(ctx, continuation, "", s.registry, adoptSourceFromSession(candidate))
}

// noteUsageCapSwitch marks both sessions so each says where the work went.
func (s *Server) noteUsageCapSwitch(lane limitrecovery.Lane, target limitrecovery.Target, newID string) {
	now := time.Now().UnixMilli()
	from := capAccountLabel(usagecap.Reading{Tool: lane.Tool, Name: lane.Account})
	to := capAccountLabel(usagecap.Reading{Tool: lane.Tool, Name: target.Account})
	if source, ok := s.registry.Get(lane.ID); ok {
		source.SetUsageCap(&state.UsageCapHold{State: state.UsageCapSwitched, Tool: lane.Tool, Account: lane.Account,
			AccountLabel: from, SwitchedTo: newID, Detail: target.Note + " Continued in session " + shortID(newID) + ".", UpdatedAt: now})
	}
	if created, ok := s.registry.Get(newID); ok {
		created.SetUsageCap(&state.UsageCapHold{State: state.UsageCapSwitched, Tool: lane.Tool, Account: target.Account,
			AccountLabel: to, SwitchedFrom: from, Detail: target.Note, UpdatedAt: now})
	}
}

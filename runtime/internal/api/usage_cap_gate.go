package api

import (
	"context"
	"fmt"
	"time"

	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/usagecap"
)

type usageCapGateInstaller interface {
	SetCreateAccountGate(sessionruntime.CreateAccountGate)
}

// usageCapRefusal is a start the usage cap refused; nothing was launched.
type usageCapRefusal struct{ message string }

func (e *usageCapRefusal) Error() string { return e.message }

func profileFlag(name string) string {
	if name == "" {
		return "--default-profile"
	}
	return "--profile " + name
}

func forkProfileFlag(name string) string {
	if name == "" {
		return "--profile ''"
	}
	return "--profile " + name
}

// usageCapCreateGate runs before a new Claude or Codex session launches. An
// account at or past the cap moves the start to the account with the most
// headroom when the caller did not name one; otherwise the start is refused
// with the reading and what to do. An unknown reading never refuses.
func (s *Server) usageCapCreateGate(ctx context.Context, tool, account string, explicit bool) (string, *state.UsageCapHold, error) {
	policy, err := s.usageCapPolicy()
	if err != nil || !policy.enabled {
		return account, nil, nil
	}
	reading := s.capRead(ctx, tool, account, usageCapGateWait)
	verdict := usagecap.Evaluate(reading, policy.cap(tool), time.Now())
	if verdict.State != usagecap.StateOver {
		return account, nil, nil
	}
	from := capAccountLabel(reading)
	target, found := s.capTarget(ctx, reading, policy, usageCapGateWait)
	if found && !explicit {
		to := capAccountLabel(target.Reading)
		note := capHold(reading, verdict)
		note.State, note.Account, note.AccountLabel, note.SwitchedFrom = state.UsageCapSwitched, target.Reading.Name, to, from
		note.Detail = fmt.Sprintf("Started on %s because %s is at %s (cap %d%%).", to, from, verdict.Detail, verdict.Cap)
		logUsageCap("start moved from %s to %s: %s", from, to, verdict.Detail)
		return target.Reading.Name, &note, nil
	}
	message := fmt.Sprintf("usage cap: %s is at %s (cap %d%%); nothing was started.", from, verdict.Detail, verdict.Cap)
	if found {
		message += fmt.Sprintf(" %s has headroom (%s); start there with %s.", capAccountLabel(target.Reading), target.Verdict.Detail, profileFlag(target.Reading.Name))
	} else {
		message += " No other signed-in " + tool + " account has headroom; wait for the reset."
	}
	message += fmt.Sprintf(" Raise the cap with `sessions cap set N --tool %s`, or turn it off with `sessions cap off`.", tool)
	return account, nil, &usageCapRefusal{message: message}
}

// usageCapSendRefusal holds back a message that would start a new turn on an
// account at or past the cap. Only the cached reading is consulted, so a
// send never waits on a provider; an unknown reading never refuses.
func (s *Server) usageCapSendRefusal(info state.SessionInfo) (string, bool) {
	if s.usageCap == nil || !limitRecoveryEligible(info) {
		return "", false
	}
	policy, err := s.usageCapPolicy()
	if err != nil || !s.laneCapped(info.ID, policy) {
		return "", false
	}
	lane := limitRecoveryLane(info)
	reading := s.capRead(context.Background(), lane.Tool, lane.Account, usageCapSendWait)
	verdict := usagecap.Evaluate(reading, policy.cap(lane.Tool), time.Now())
	if verdict.State != usagecap.StateOver {
		return "", false
	}
	hold := capHold(reading, verdict)
	if current, ok := s.registry.Get(info.ID); ok && !info.Working && !movedAway(info) {
		if info.UsageCap != nil && info.UsageCap.State == state.UsageCapWaiting {
			hold.SwitchAccount, hold.SwitchLabel = info.UsageCap.SwitchAccount, info.UsageCap.SwitchLabel
		}
		current.SetUsageCap(&hold)
	}
	message := fmt.Sprintf("message not sent: usage cap. %s is at %s (cap %d%%), so Sessions holds new turns on this account; nothing reached the provider, so send it again after the reset.",
		hold.AccountLabel, verdict.Detail, verdict.Cap)
	if info.UsageCap != nil && info.UsageCap.SwitchLabel != "" {
		message += fmt.Sprintf(" To continue now on another account: `sessions fork %s %s`.", info.ID, forkProfileFlag(info.UsageCap.SwitchAccount))
	}
	message += fmt.Sprintf(" Raise the cap with `sessions cap set N`, or lift it for this session with `sessions cap off --lane %s`.", info.ID)
	return message, true
}

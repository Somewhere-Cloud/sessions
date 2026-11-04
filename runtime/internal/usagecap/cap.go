// Package usagecap decides whether a provider account has used more of its
// metered allowance than the person allows Sessions to spend, and which other
// account on the same computer has the most headroom.
//
// The cap is a policy about new turns only. Sessions cannot stop a provider
// turn safely, so nothing here interrupts work in progress: a caller uses a
// verdict to hold back the next turn, refuse a new start, or pick another
// account. A reading Sessions does not trust (stale, unavailable, signed out)
// never holds anything back; it is reported as unknown instead.
package usagecap

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultPercent is the cap when the person has not chosen one.
	DefaultPercent = 90
	// MinPercent and MaxPercent bound a chosen cap. 100 holds back only at
	// the provider's own limit.
	MinPercent = 1
	MaxPercent = 100
)

// Verdict states.
const (
	StateUnder   = "under"
	StateOver    = "over"
	StateUnknown = "unknown"
)

// Window is one rolling allowance an account reported.
type Window struct {
	// Limit names a model-specific allowance; empty for the shared one.
	Limit       string
	Minutes     int64
	UsedPercent int
	// ResetsAtMS is zero when the provider did not say.
	ResetsAtMS int64
}

// Reading is one account's latest usage as Sessions read it.
type Reading struct {
	Tool  string
	Name  string
	Label string
	// Key identifies the provider account behind the home when the provider
	// said (email and account id); two homes with one key share a quota.
	Key string
	// Available is a fresh reading. Stale is an earlier reading kept beside
	// a failed one; neither stale nor unavailable readings hold anything back.
	Available bool
	Stale     bool
	Message   string
	ReadAtMS  int64
	// Reached is the provider's own statement that a limit is reached.
	Reached bool
	Windows []Window
}

// Verdict is what the cap means for one account right now.
type Verdict struct {
	State       string `json:"state"`
	Cap         int    `json:"cap"`
	UsedPercent int    `json:"usedPercent"`
	// Window names the window the verdict rests on: the one that holds the
	// account back longest when over, the fullest one otherwise.
	Window     string `json:"window,omitempty"`
	ResetsAtMS int64  `json:"resetsAt,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// ValidPercent reports whether percent is a cap a person may choose.
func ValidPercent(percent int) bool {
	return percent >= MinPercent && percent <= MaxPercent
}

// Evaluate compares a reading with the cap. A window at or above the cap with
// a reset still ahead (or no reset reported) is over; a window whose reset has
// passed has reset and does not count.
func Evaluate(reading Reading, cap int, now time.Time) Verdict {
	verdict := Verdict{State: StateUnknown, Cap: cap}
	if !reading.Available || reading.Stale {
		verdict.Detail = unknownDetail(reading)
		return verdict
	}
	verdict.State = StateUnder
	var binding, fullest *Window
	for index := range reading.Windows {
		window := &reading.Windows[index]
		if window.ResetsAtMS != 0 && window.ResetsAtMS <= now.UnixMilli() {
			continue
		}
		if fullest == nil || window.UsedPercent > fullest.UsedPercent {
			fullest = window
		}
		if window.UsedPercent < cap {
			continue
		}
		if binding == nil || laterReset(*window, *binding) {
			binding = window
		}
	}
	switch {
	case binding != nil:
		verdict.State = StateOver
		verdict.UsedPercent, verdict.Window, verdict.ResetsAtMS = binding.UsedPercent, WindowLabel(*binding), binding.ResetsAtMS
	case reading.Reached:
		verdict.State, verdict.UsedPercent, verdict.Window = StateOver, 100, "usage limit"
	case fullest != nil:
		verdict.UsedPercent, verdict.Window, verdict.ResetsAtMS = fullest.UsedPercent, WindowLabel(*fullest), fullest.ResetsAtMS
	}
	verdict.Detail = Describe(verdict, now)
	return verdict
}

// laterReset orders windows by when they let the account go again; an
// unknown reset is the latest of all.
func laterReset(candidate, current Window) bool {
	if candidate.ResetsAtMS == 0 || current.ResetsAtMS == 0 {
		return candidate.ResetsAtMS == 0 && current.ResetsAtMS != 0
	}
	return candidate.ResetsAtMS > current.ResetsAtMS
}

func unknownDetail(reading Reading) string {
	if reading.Stale {
		return "The latest usage reading is stale, so the cap holds nothing back until a fresh one."
	}
	if strings.TrimSpace(reading.Message) != "" {
		return "No usage reading: " + strings.TrimSpace(reading.Message) + " The cap holds nothing back without one."
	}
	return "No usage reading yet; the cap holds nothing back without one."
}

// WindowLabel names a window the way people talk about it.
func WindowLabel(window Window) string {
	span := ""
	switch window.Minutes {
	case 0:
		span = "limit"
	case 300:
		span = "5-hour"
	case 1440:
		span = "daily"
	case 10_080:
		span = "weekly"
	default:
		span = (time.Duration(window.Minutes) * time.Minute).String()
		span = strings.TrimSuffix(strings.TrimSuffix(span, "0s"), "0m")
	}
	if limit := strings.TrimSpace(window.Limit); limit != "" {
		return limit + " " + span
	}
	return span
}

// Describe is the verdict in a short sentence fragment, such as
// "92% of weekly, resets Tue 09:00".
func Describe(verdict Verdict, now time.Time) string {
	if verdict.State == StateUnknown {
		return verdict.Detail
	}
	if verdict.Window == "" {
		return fmt.Sprintf("no metered window reported (cap %d%%)", verdict.Cap)
	}
	text := fmt.Sprintf("%d%% of %s", verdict.UsedPercent, verdict.Window)
	if verdict.ResetsAtMS > 0 {
		text += ", resets " + ResetLabel(verdict.ResetsAtMS, now)
	} else if verdict.State == StateOver {
		text += ", reset time not reported"
	}
	return text
}

// ResetLabel is a clock time today, or a weekday and time within a week.
func ResetLabel(atMS int64, now time.Time) string {
	at := time.UnixMilli(atMS).In(now.Location())
	if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return at.Format("15:04")
	}
	if at.Sub(now) < 6*24*time.Hour {
		return at.Format("Mon 15:04")
	}
	return at.Format("Jan 2 15:04")
}

// Candidate is an account Pick may choose, with its verdict.
type Candidate struct {
	Reading Reading
	Verdict Verdict
}

// Pick returns the account of current's tool, other than current, with the
// most headroom: under the cap on a fresh reading, lowest fullest window
// first, then by name so the choice is stable. It never picks an account
// whose reading is unknown, or another home signed in to the same account.
func Pick(readings []Reading, current Reading, cap int, now time.Time) (Candidate, bool) {
	var candidates []Candidate
	for _, reading := range readings {
		if reading.Tool != current.Tool || reading.Name == current.Name || (current.Key != "" && reading.Key == current.Key) {
			continue
		}
		verdict := Evaluate(reading, cap, now)
		if verdict.State == StateUnder {
			candidates = append(candidates, Candidate{Reading: reading, Verdict: verdict})
		}
	}
	if len(candidates) == 0 {
		return Candidate{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Verdict.UsedPercent != candidates[j].Verdict.UsedPercent {
			return candidates[i].Verdict.UsedPercent < candidates[j].Verdict.UsedPercent
		}
		return candidates[i].Reading.Name < candidates[j].Reading.Name
	})
	return candidates[0], true
}

// AccountLabel names an account for people: its label, else tool/name, with
// the CLI default login called "default".
func AccountLabel(reading Reading) string {
	if strings.TrimSpace(reading.Label) != "" {
		return reading.Label
	}
	if reading.Name == "" {
		return reading.Tool + " default"
	}
	return reading.Tool + "/" + reading.Name
}

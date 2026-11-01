// Package limitrecovery resumes a session whose turn ended on a provider usage
// limit. When the limit resets, Sessions sends ContinueText through the normal
// submit path, once per limit, attributed to automation. The plan is persisted
// before it is acted on, the same write-ahead shape idle compaction uses
// (runtime/internal/compaction), so a daemon restart neither loses a plan nor
// sends one twice.
package limitrecovery

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reset time sources, reported so a reader can tell a provider's own answer
// from Sessions' fallback.
const (
	SourceProvider = "provider-message"
	SourceUsage    = "account-usage"
	SourceUnknown  = "unknown"
)

var (
	// Claude Code's subscription limit line: "Claude AI usage limit reached|1761930000".
	epochRE = regexp.MustCompile(`(?i)limit reached\|(\d{10,13})`)
	// "resets 3pm", "resets at 3:30 pm (Europe/London)".
	resetsClockRE = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s*\(([A-Za-z_]+/[A-Za-z_/]+|UTC)\))?`)
	// "try again at 3:05 PM".
	againClockRE = regexp.MustCompile(`(?i)\btry again at\s+(\d{1,2}):(\d{2})\s*(am|pm)?`)
	// "try again in 2 hours 5 minutes", "try again in 4 days 3 hours".
	againInRE = regexp.MustCompile(`(?i)\btry again in\s+((?:\d+\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b[\s,and]*)+)`)
	partRE    = regexp.MustCompile(`(?i)(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

// ResetFromText reads when a usage limit resets from the provider's own words.
// It understands the forms Claude Code and Codex print; anything else is
// unknown rather than guessed. A clock time without a date is the next time
// that clock time occurs.
func ResetFromText(text string, now time.Time) (time.Time, bool) {
	if match := epochRE.FindStringSubmatch(text); match != nil {
		value, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil {
			if len(match[1]) > 10 {
				return time.UnixMilli(value), true
			}
			return time.Unix(value, 0), true
		}
	}
	if match := againInRE.FindStringSubmatch(text); match != nil {
		if delay := parseDuration(match[1]); delay > 0 {
			return now.Add(delay), true
		}
	}
	if match := resetsClockRE.FindStringSubmatch(text); match != nil && (match[2] != "" || match[3] != "") {
		return nextClock(now, match[1], match[2], match[3], match[4])
	}
	if match := againClockRE.FindStringSubmatch(text); match != nil {
		return nextClock(now, match[1], match[2], match[3], "")
	}
	return time.Time{}, false
}

func parseDuration(text string) time.Duration {
	var total time.Duration
	for _, part := range partRE.FindAllStringSubmatch(text, -1) {
		count, err := strconv.Atoi(part[1])
		if err != nil {
			return 0
		}
		unit := strings.ToLower(part[2])
		switch {
		case strings.HasPrefix(unit, "d"):
			total += time.Duration(count) * 24 * time.Hour
		case strings.HasPrefix(unit, "h"):
			total += time.Duration(count) * time.Hour
		case strings.HasPrefix(unit, "m"):
			total += time.Duration(count) * time.Minute
		default:
			total += time.Duration(count) * time.Second
		}
	}
	return total
}

func nextClock(now time.Time, hourText, minuteText, meridiem, zone string) (time.Time, bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return time.Time{}, false
	}
	minute := 0
	if minuteText != "" {
		if minute, err = strconv.Atoi(minuteText); err != nil || minute > 59 {
			return time.Time{}, false
		}
	}
	switch strings.ToLower(meridiem) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 12 {
			hour += 12
		}
	}
	if hour > 23 {
		return time.Time{}, false
	}
	location := now.Location()
	if zone != "" {
		if loaded, err := time.LoadLocation(zone); err == nil {
			location = loaded
		}
	}
	local := now.In(location)
	at := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, location)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, true
}

// Window is one rolling allowance as an account usage read reports it.
type Window struct {
	UsedPercent int
	ResetsAtMS  int64
}

// ResetFromWindows returns when every exhausted window has reset, the latest
// of their reset times, or false when no window is exhausted with a known
// future reset.
func ResetFromWindows(windows []Window, now time.Time) (time.Time, bool) {
	var latest int64
	for _, window := range windows {
		if window.UsedPercent >= 100 && window.ResetsAtMS > now.UnixMilli() && window.ResetsAtMS > latest {
			latest = window.ResetsAtMS
		}
	}
	if latest == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(latest), true
}

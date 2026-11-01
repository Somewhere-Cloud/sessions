package limitrecovery

import (
	"testing"
	"time"
)

func TestResetFromText(t *testing.T) {
	now := time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		text string
		want time.Time
		ok   bool
	}{
		{"Claude AI usage limit reached|1793548800", time.Unix(1793548800, 0), true},
		{"You've hit your limit · resets 3pm (UTC)", time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC), true},
		{"5-hour limit reached ∙ resets 9:30am", time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC), true},
		{"You've hit your usage limit. Try again in 2 hours 5 minutes.", now.Add(2*time.Hour + 5*time.Minute), true},
		{"usage limit; try again in 4 days 3 hours", now.Add(99 * time.Hour), true},
		{"You've hit your usage limit. Try again at 3:05 PM.", time.Date(2026, 11, 1, 15, 5, 0, 0, time.UTC), true},
		{"Claude rate limit reached (429)", time.Time{}, false},
		{"the cache resets 3 times", time.Time{}, false},
	}
	for _, test := range cases {
		got, ok := ResetFromText(test.text, now)
		if ok != test.ok || !got.Equal(test.want) {
			t.Errorf("ResetFromText(%q) = %v, %v; want %v, %v", test.text, got, ok, test.want, test.ok)
		}
	}
}

func TestResetFromWindowsWaitsForEveryExhaustedWindow(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	windows := []Window{
		{UsedPercent: 100, ResetsAtMS: 2_000_000},
		{UsedPercent: 100, ResetsAtMS: 5_000_000},
		{UsedPercent: 40, ResetsAtMS: 9_000_000},
		{UsedPercent: 100, ResetsAtMS: 500_000},
	}
	got, ok := ResetFromWindows(windows, now)
	if !ok || got.UnixMilli() != 5_000_000 {
		t.Fatalf("ResetFromWindows = %v, %v; want 5000000", got.UnixMilli(), ok)
	}
	if _, ok := ResetFromWindows([]Window{{UsedPercent: 80, ResetsAtMS: 2_000_000}}, now); ok {
		t.Fatal("a window below its limit is not a reset to wait for")
	}
}

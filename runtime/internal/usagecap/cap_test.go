package usagecap

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)

func reading(name string, windows ...Window) Reading {
	return Reading{Tool: "claude", Name: name, Available: true, ReadAtMS: testNow.UnixMilli(), Windows: windows}
}

func weekly(used int) Window {
	return Window{Minutes: 10_080, UsedPercent: used, ResetsAtMS: testNow.Add(3 * 24 * time.Hour).UnixMilli()}
}

func session(used int) Window {
	return Window{Minutes: 300, UsedPercent: used, ResetsAtMS: testNow.Add(2 * time.Hour).UnixMilli()}
}

func TestEvaluateAtBelowAndAboveTheCap(t *testing.T) {
	for _, test := range []struct {
		used int
		want string
	}{{89, StateUnder}, {90, StateOver}, {97, StateOver}} {
		verdict := Evaluate(reading("work", session(10), weekly(test.used)), 90, testNow)
		if verdict.State != test.want {
			t.Fatalf("%d%%: state %q, want %q (%+v)", test.used, verdict.State, test.want, verdict)
		}
		if verdict.UsedPercent != test.used || verdict.Window != "weekly" {
			t.Fatalf("%d%%: verdict rests on %+v", test.used, verdict)
		}
	}
	over := Evaluate(reading("work", weekly(92)), 90, testNow)
	if !strings.HasPrefix(over.Detail, "92% of weekly, resets ") || over.ResetsAtMS == 0 {
		t.Fatalf("detail = %q", over.Detail)
	}
}

func TestEvaluateNeverHoldsBackOnAnUntrustedReading(t *testing.T) {
	stale := reading("work", weekly(99))
	stale.Stale = true
	unavailable := Reading{Tool: "claude", Name: "work", Message: "The provider has not answered yet."}
	for _, value := range []Reading{stale, unavailable} {
		if verdict := Evaluate(value, 90, testNow); verdict.State != StateUnknown || verdict.Detail == "" {
			t.Fatalf("untrusted reading judged %+v", verdict)
		}
	}
}

func TestEvaluateChecksEveryWindowAndIgnoresResetOnes(t *testing.T) {
	opus := Window{Limit: "Opus", Minutes: 10_080, UsedPercent: 95, ResetsAtMS: testNow.Add(4 * 24 * time.Hour).UnixMilli()}
	verdict := Evaluate(reading("work", session(96), weekly(40), opus), 90, testNow)
	if verdict.State != StateOver || verdict.Window != "Opus weekly" || verdict.UsedPercent != 95 {
		t.Fatalf("the window that holds the account longest should win: %+v", verdict)
	}
	passed := Window{Minutes: 300, UsedPercent: 100, ResetsAtMS: testNow.Add(-time.Minute).UnixMilli()}
	if verdict := Evaluate(reading("work", passed, weekly(30)), 90, testNow); verdict.State != StateUnder || verdict.UsedPercent != 30 {
		t.Fatalf("a window past its reset still counted: %+v", verdict)
	}
	reached := reading("work", weekly(10))
	reached.Reached = true
	if verdict := Evaluate(reached, 90, testNow); verdict.State != StateOver {
		t.Fatalf("a provider-reported limit was ignored: %+v", verdict)
	}
	if verdict := Evaluate(reading("work", weekly(99)), 100, testNow); verdict.State != StateUnder {
		t.Fatalf("a 100%% cap held back at 99%%: %+v", verdict)
	}
}

func TestPickChoosesMostHeadroomAndSkipsTheSameAccount(t *testing.T) {
	current := reading("", weekly(95))
	current.Key = "me@example.test|"
	sameAccount := reading("alias", weekly(5))
	sameAccount.Key = current.Key
	stale := reading("stale", weekly(1))
	stale.Stale = true
	codex := reading("codex-home", weekly(0))
	codex.Tool = "codex"
	readings := []Reading{current, sameAccount, stale, codex, reading("busy", weekly(91)), reading("b", weekly(30)), reading("a", session(30))}
	candidate, ok := Pick(readings, current, 90, testNow)
	if !ok || candidate.Reading.Name != "a" || candidate.Verdict.UsedPercent != 30 {
		t.Fatalf("picked %+v (ok=%v)", candidate, ok)
	}
	if _, ok := Pick([]Reading{current, reading("busy", weekly(91))}, current, 90, testNow); ok {
		t.Fatal("picked an account without headroom")
	}
}

func TestWindowLabelsAndAccountLabels(t *testing.T) {
	if got := WindowLabel(Window{Minutes: 120}); got != "2h" {
		t.Fatalf("label = %q", got)
	}
	if got := AccountLabel(Reading{Tool: "codex"}); got != "codex default" {
		t.Fatalf("label = %q", got)
	}
	if got := AccountLabel(Reading{Tool: "codex", Name: "work", Label: "Work"}); got != "Work" {
		t.Fatalf("label = %q", got)
	}
}

func TestStoreKeepsChoicesAndRefusesAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	store := OpenStore(path)
	off := false
	if err := store.SetOverride("lane-1", &off); err != nil {
		t.Fatal(err)
	}
	reopened := OpenStore(path)
	if value, ok := reopened.Override("lane-1"); !ok || value {
		t.Fatalf("override = %v %v", value, ok)
	}
	if err := reopened.Prune(func(string) bool { return false }); err != nil || len(reopened.IDs()) != 0 {
		t.Fatalf("prune: %v %v", err, reopened.IDs())
	}
	broken := filepath.Join(t.TempDir(), FileName)
	if err := writeDocument(broken, storeDocument{Version: 9}); err != nil {
		t.Fatal(err)
	}
	if store := OpenStore(broken); store.Err() == nil || store.SetOverride("x", nil) == nil {
		t.Fatal("an unreadable store was used")
	}
}

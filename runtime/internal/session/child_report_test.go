package session

import (
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestOfferChildReportPublishesEveryOutcomeToTheReceiver(t *testing.T) {
	manager := &Manager{}
	reports := make(chan ChildReport, 4)
	manager.SetChildReportHandler(func(report ChildReport) { reports <- report })

	child := idleRecord{
		classification: IdleClassification{Outcome: IdleBlocked, Line: "Allow edits?"},
		summary:        "half done", at: 42, duration: 3 * time.Second,
		info: state.SessionInfo{ID: "child", Name: "worker", ParentSessionID: "parent"},
	}
	manager.offerChildReport(child)
	select {
	case report := <-reports:
		if report.SessionID != "child" || report.ParentSessionID != "parent" || report.IdleReason != state.IdleReasonNeedsInput ||
			report.Detail != "Allow edits?" || report.Summary != "half done" || report.At != 42 || report.DurationMS != 3000 {
			t.Fatalf("report = %+v", report)
		}
	case <-time.After(time.Second):
		t.Fatal("child outcome was not offered")
	}

	noOutcome := child
	noOutcome.classification.Outcome = idleNoOutcome
	manager.offerChildReport(noOutcome)
	manager.SetChildReportHandler(nil)
	manager.offerChildReport(child)
	select {
	case report := <-reports:
		t.Fatalf("unexpected report %+v", report)
	case <-time.After(100 * time.Millisecond):
	}
}

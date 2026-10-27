package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type reportCapturingService struct {
	sessionService
	mu           sync.Mutex
	calls        []proto.MessageControl
	attributions []state.InputAttribution
}

func (s *reportCapturingService) SubmitMessage(_ context.Context, _ string, control proto.MessageControl, attribution state.InputAttribution) (proto.MessageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, control)
	s.attributions = append(s.attributions, attribution)
	return proto.MessageResult{OperationID: control.OperationID, Accepted: true, Boundary: "provider"}, nil
}

func (s *reportCapturingService) submitted() ([]proto.MessageControl, []state.InputAttribution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]proto.MessageControl(nil), s.calls...), append([]state.InputAttribution(nil), s.attributions...)
}

func newParentReportFixture(t *testing.T) (testDaemon, *reportCapturingService, string, string) {
	t.Helper()
	daemon := newTestDaemon(t)
	parent := registerStructuredSession(t, daemon, "parent-lane")
	child := registerStructuredSession(t, daemon, "child-lane")
	service := &reportCapturingService{sessionService: daemon.registry}
	daemon.handler.registry = service
	if err := daemon.handler.parentReports.subscribe(child.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	return daemon, service, parent.ID, child.ID
}

func childOutcome(child, parent string, at int64) sessionruntime.ChildReport {
	return sessionruntime.ChildReport{
		SessionID: child, Name: "worker", ParentSessionID: parent,
		IdleReason: state.IdleReasonCompleted, Summary: "tests pass; committed abc123", At: at,
	}
}

func discardLog(string, ...any) {}

func TestParentReportDeliversChildOutcomeToIdleParentAttributedToChild(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	daemon.handler.offerParentReport(childOutcome(child, parent, 1000), discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)

	calls, attributions := service.submitted()
	if len(calls) != 1 {
		t.Fatalf("submitted %d reports, want 1", len(calls))
	}
	for _, want := range []string{`"worker"`, child, "finished", "tests pass; committed abc123", "sessions last " + child} {
		if !strings.Contains(calls[0].Text, want) {
			t.Fatalf("report %q does not mention %q", calls[0].Text, want)
		}
	}
	if attributions[0].SourceSessionID != child || attributions[0].Client != parentReportClient {
		t.Fatalf("attribution = %+v, want the child as source", attributions[0])
	}
	if pending := daemon.handler.parentReports.pending(); len(pending) != 0 {
		t.Fatalf("delivered report is still pending: %+v", pending)
	}
}

func TestParentReportSameOutcomeIsNeverDeliveredTwice(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	for range 2 {
		daemon.handler.offerParentReport(childOutcome(child, parent, 1000), discardLog)
		daemon.handler.deliverParentReports(context.Background(), discardLog)
	}
	if calls, _ := service.submitted(); len(calls) != 1 {
		t.Fatalf("submitted %d times, want exactly once", len(calls))
	}
}

func TestParentReportWaitsForBusyParentThenDeliversInOrder(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	current, _ := daemon.registry.Get(parent)
	current.SetWorking(true)
	daemon.handler.offerParentReport(childOutcome(child, parent, 1000), discardLog)
	second := childOutcome(child, parent, 2000)
	second.Summary = "second turn"
	daemon.handler.offerParentReport(second, discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("busy parent received %d reports", len(calls))
	}

	current.SetWorking(false)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	calls, _ := service.submitted()
	if len(calls) != 1 || !strings.Contains(calls[0].Text, "tests pass") {
		t.Fatalf("first pass after idle delivered %+v, want only the earlier report", calls)
	}
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ = service.submitted(); len(calls) != 2 || !strings.Contains(calls[1].Text, "second turn") {
		t.Fatalf("second pass delivered %+v", calls)
	}
}

func TestParentReportIgnoresUnsubscribedChildrenAndOtherParents(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	daemon.handler.offerParentReport(childOutcome("someone-else", parent, 1000), discardLog)
	daemon.handler.offerParentReport(childOutcome(child, "not-the-parent", 1000), discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("unsubscribed outcomes were delivered: %+v", calls)
	}
}

func TestParentReportDropsReportForUnknownParent(t *testing.T) {
	daemon, service, _, child := newParentReportFixture(t)
	if err := daemon.handler.parentReports.subscribe(child, "gone-parent"); err != nil {
		t.Fatal(err)
	}
	daemon.handler.offerParentReport(childOutcome(child, "gone-parent", 1000), discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("report to an unknown parent was submitted: %+v", calls)
	}
	if pending := daemon.handler.parentReports.pending(); len(pending) != 0 {
		t.Fatalf("report to an unknown parent stays pending: %+v", pending)
	}
}

func TestParentReportOutboxSurvivesDaemonRestart(t *testing.T) {
	daemon, _, parent, child := newParentReportFixture(t)
	current, _ := daemon.registry.Get(parent)
	current.SetWorking(true)
	daemon.handler.offerParentReport(childOutcome(child, parent, 1000), discardLog)

	restarted := newParentReports(daemon.config.StateRoot)
	if got, ok := restarted.parentOf(child); !ok || got != parent {
		t.Fatalf("restarted subscription = %q %v", got, ok)
	}
	if pending := restarted.pending(); len(pending) != 1 || pending[0].Child != child {
		t.Fatalf("restarted outbox = %+v", pending)
	}
}

func TestParentReportUncertainDeliveryIsNotResent(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	report := childOutcome(child, parent, 1000)
	// A daemon that stopped mid-send leaves the delivery record pending.
	if _, _, err := daemon.handler.deliveries.Begin(parentReportOperationID(child, 1000), parent, formatParentReport(report)); err != nil {
		t.Fatal(err)
	}
	daemon.handler.offerParentReport(report, discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("uncertain report was resent: %+v", calls)
	}
	if pending := daemon.handler.parentReports.pending(); len(pending) != 0 {
		t.Fatalf("uncertain report stays pending: %+v", pending)
	}
}

func TestParentReportSubscriptionEndsWithChild(t *testing.T) {
	daemon, _, _, child := newParentReportFixture(t)
	if err := daemon.handler.parentReports.subscribe("ended-child", "parent-lane"); err != nil {
		t.Fatal(err)
	}
	daemon.handler.pruneParentSubscriptions()
	if _, ok := daemon.handler.parentReports.parentOf("ended-child"); ok {
		t.Fatal("subscription for a child the daemon does not know survived pruning")
	}
	if _, ok := daemon.handler.parentReports.parentOf(child); !ok {
		t.Fatal("live child's subscription was pruned")
	}
	if _, err := os.Stat(daemon.handler.parentReports.subscriptionDir()); err != nil {
		t.Fatal(err)
	}
}

func TestFormatParentReportLeadsWithTheQuestionForBlockedLanes(t *testing.T) {
	report := childOutcome("child", "parent", 1)
	report.IdleReason, report.Detail = state.IdleReasonNeedsInput, "Allow edits to go.mod?"
	text := formatParentReport(report)
	if !strings.Contains(text, "needs input:\nAllow edits to go.mod?") {
		t.Fatalf("blocked report = %q", text)
	}
	report.Summary = strings.Repeat("x", parentReportTextLimit+50)
	report.IdleReason = state.IdleReasonCompleted
	if text := formatParentReport(report); strings.Count(text, "x") > parentReportTextLimit+1 {
		t.Fatalf("summary was not bounded: %d runes", len(text))
	}
}

type parentedCreateService struct {
	sessionService
	parent string
}

func (s *parentedCreateService) Create(_ context.Context, request state.CreateSessionRequest) (state.SessionInfo, error) {
	return state.SessionInfo{ID: "created-child", ParentSessionID: s.parent}, nil
}

func TestCreateWithReportToParentSubscribesOnlyAChild(t *testing.T) {
	for _, test := range []struct {
		name, parent string
		want         bool
	}{
		{name: "child", parent: "manager-lane", want: true},
		{name: "root", parent: "", want: false},
		{name: "shell parent", parent: "shell-lane", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := newTestDaemon(t)
			registerStructuredSession(t, daemon, "manager-lane")
			shell := prototest.NewRunner(proto.RunnerInfo{
				ID: "shell-lane", Cmd: "/bin/bash", Cwd: daemon.root, Cols: 80, Rows: 24, ProtocolVersion: proto.ProtocolVersion,
			})
			if _, err := daemon.registry.Register(context.Background(), shell, "Shell", ""); err != nil {
				t.Fatal(err)
			}
			daemon.handler.registry = &parentedCreateService{sessionService: daemon.registry, parent: test.parent}
			response := serve(t, daemon.handler, http.MethodPost, "/api/sessions",
				strings.NewReader(`{"reportToParent":true}`), "127.0.0.1:4567", nil)
			var created state.SessionInfo
			decodeBody(t, response, &created)
			if created.ReportsToParent != test.want {
				t.Fatalf("reports_to_parent = %v, want %v (%s)", created.ReportsToParent, test.want, response.Body.String())
			}
			if _, ok := daemon.handler.parentReports.parentOf("created-child"); ok != test.want {
				t.Fatalf("subscription recorded = %v, want %v", ok, test.want)
			}
		})
	}
}

// The live session record often has no parent id: provenance lives in the
// ledger. The subscription alone decides who hears the outcome.
func TestParentReportMatchesOnSubscriptionWhenTheRecordHasNoParent(t *testing.T) {
	daemon, service, parent, child := newParentReportFixture(t)
	daemon.handler.offerParentReport(childOutcome(child, "", 1000), discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 1 {
		t.Fatalf("submitted %d reports to %s, want 1", len(calls), parent)
	}
}

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// questionService gives the child a recorded parent, which a test runner
// cannot carry, and captures what is submitted to the parent.
type questionService struct {
	*reportCapturingService
	child, parent string
}

func (q *questionService) List(includeExited bool) []state.SessionInfo {
	infos := q.reportCapturingService.List(includeExited)
	for index := range infos {
		if infos[index].ID == q.child {
			infos[index].ParentSessionID = q.parent
		}
	}
	return infos
}

func newChildQuestionFixture(t *testing.T) (testDaemon, *questionService) {
	t.Helper()
	daemon := newTestDaemon(t)
	parent := registerStructuredSession(t, daemon, "manager-lane")
	child := registerStructuredSession(t, daemon, "worker-lane")
	service := &questionService{
		reportCapturingService: &reportCapturingService{sessionService: daemon.registry},
		child:                  child.ID, parent: parent.ID,
	}
	daemon.handler.registry = service
	return daemon, service
}

func askChild(t *testing.T, daemon testDaemon, child, question string, at int64) {
	t.Helper()
	current, ok := daemon.registry.Get(child)
	if !ok {
		t.Fatalf("child %s is not registered", child)
	}
	current.SetIdleResult(state.IdleReasonNeedsInput, question, "", at)
}

func questionPass(daemon testDaemon) {
	daemon.handler.scanChildQuestions("", discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
}

func TestChildQuestionReachesParentOncePerWaitingEpisode(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	askChild(t, daemon, service.child, "Which database should the migration target?", 1000)
	for range 3 {
		questionPass(daemon)
	}
	calls, attributions := service.submitted()
	if len(calls) != 1 {
		t.Fatalf("parent heard the question %d times, want once", len(calls))
	}
	for _, want := range []string{"Child " + shortID(service.child), "is waiting: Which database should the migration target?", "sessions send " + service.child} {
		if !strings.Contains(calls[0].Text, want) {
			t.Fatalf("question %q does not mention %q", calls[0].Text, want)
		}
	}
	if attributions[0].SourceSessionID != service.child {
		t.Fatalf("attribution = %+v, want the child as source", attributions[0])
	}

	// The child is answered and works; its next question is a new episode.
	current, _ := daemon.registry.Get(service.child)
	current.ClearIdleResult()
	questionPass(daemon)
	askChild(t, daemon, service.child, "Ship it now?", 5000)
	questionPass(daemon)
	if calls, _ = service.submitted(); len(calls) != 2 || !strings.Contains(calls[1].Text, "Ship it now?") {
		t.Fatalf("second episode delivered %+v, want one more question", calls)
	}
}

func TestChildQuestionIsNotSentToAGoneParent(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	service.parent = "parent-that-ended-long-ago"
	askChild(t, daemon, service.child, "Continue?", 1000)
	questionPass(daemon)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("a gone parent was sent %+v", calls)
	}
	if pending := daemon.handler.parentReports.pending(); len(pending) != 0 {
		t.Fatalf("a question to a gone parent is queued: %+v", pending)
	}
}

func TestChildQuestionAnsweredBeforeDeliveryIsDropped(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	parent, _ := daemon.registry.Get(service.parent)
	parent.SetWorking(true)
	askChild(t, daemon, service.child, "Overwrite the fixture?", 1000)
	questionPass(daemon)
	if pending := daemon.handler.parentReports.pending(); len(pending) != 1 {
		t.Fatalf("busy parent's question was not queued: %+v", pending)
	}

	child, _ := daemon.registry.Get(service.child)
	child.ClearIdleResult()
	parent.SetWorking(false)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("an answered question was delivered: %+v", calls)
	}
	if pending := daemon.handler.parentReports.pending(); len(pending) != 0 {
		t.Fatalf("an answered question stays queued: %+v", pending)
	}
}

func TestChildQuestionsOffKeepsParentsQuiet(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	response := serve(t, daemon.handler, http.MethodPut, childQuestionsRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("turn off = %d %s", response.Code, response.Body.String())
	}
	askChild(t, daemon, service.child, "Continue?", 1000)
	questionPass(daemon)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("questions were sent while off: %+v", calls)
	}

	// Turning it back on does not replay the episode the parent never heard.
	response = serve(t, daemon.handler, http.MethodPut, childQuestionsRoute, strings.NewReader(`{"enabled":true}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("turn on = %d %s", response.Code, response.Body.String())
	}
	questionPass(daemon)
	if calls, _ := service.submitted(); len(calls) != 0 {
		t.Fatalf("an old episode was sent after turning on: %+v", calls)
	}
}

func TestChildQuestionEpisodeSurvivesRestart(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	askChild(t, daemon, service.child, "Continue?", 1000)
	questionPass(daemon)
	daemon.handler.parentReports = newParentReports(strings.TrimSuffix(daemon.handler.parentReports.root, "/parent-reports"))
	questionPass(daemon)
	if calls, _ := service.submitted(); len(calls) != 1 {
		t.Fatalf("restart re-asked: %d deliveries", len(calls))
	}
}

func TestChildQuestionReclassifiedBlockedScreenIsTheSameEpisode(t *testing.T) {
	daemon, service := newChildQuestionFixture(t)
	askChild(t, daemon, service.child, "Trust this folder?", 1000)
	questionPass(daemon)
	// The blocked screen is classified again while the child never worked.
	askChild(t, daemon, service.child, "Trust this folder?", 4000)
	questionPass(daemon)
	if calls, _ := service.submitted(); len(calls) != 1 {
		t.Fatalf("one blocked prompt reached the parent %d times", len(calls))
	}
	// A turn that ended on a new question is a new episode even without a
	// scan seeing the child answered in between.
	askChild(t, daemon, service.child, "Push the branch?", 9000)
	daemon.handler.scanChildQuestions(service.child, discardLog)
	daemon.handler.deliverParentReports(context.Background(), discardLog)
	if calls, _ := service.submitted(); len(calls) != 2 || !strings.Contains(calls[1].Text, "Push the branch?") {
		t.Fatalf("a fresh question after an ended turn delivered %+v", calls)
	}
}

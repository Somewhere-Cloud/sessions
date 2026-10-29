package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
)

const handoffTestOp = "5d000000-0000-4000-8000-000000000001"
const handoffTarget = "5d000000-0000-4000-8000-000000000002"

type handoffFake struct {
	sessionService
	registry *state.Registry
	config   state.Config
	mu       sync.Mutex
	creates  int
	sends    int
	artifact *state.ContinuationContext
	unknown  bool
}

func (f *handoffFake) CodexModelOptions(context.Context) ([]codexapp.Model, error) {
	return []codexapp.Model{{ID: "fixture-astra", DisplayName: "Fixture Astra", IsDefault: true}}, nil
}
func (f *handoffFake) Create(ctx context.Context, request state.CreateSessionRequest) (state.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	c := *request.Continuation
	f.artifact = &c
	// This adapter is deliberately fake: it models an acknowledged text import,
	// not native Codex compatibility or model recall.
	c.NativeCreation, c.NativeTargetID, c.ProviderContext, c.LocalHistoryReady = "created", "fake-native", "applied", true
	if request.Cmd == "claude" {
		c.NativeCreation, c.NativeTargetID, c.ProviderContext = "", "", ""
	}
	if err := state.WriteContinuation(state.For(f.config.RunnerStateDir, handoffTarget).Continuation, c); err != nil {
		return state.SessionInfo{}, err
	}
	runner := prototest.NewRunner(proto.RunnerInfo{ID: handoffTarget, Cmd: request.Cmd, Cols: 120, Rows: 40, ProtocolVersion: proto.ProtocolVersion, Cwd: request.Cwd, MessageSubmit: true, ConversationID: "fake-native"})
	session, err := f.registry.Register(ctx, runner, request.Name, "")
	if err != nil {
		return state.SessionInfo{}, err
	}
	return session.Info(), nil
}
func (f *handoffFake) List(exited bool) []state.SessionInfo {
	infos := f.sessionService.List(exited)
	for i := range infos {
		if infos[i].ID == handoffTarget {
			infos[i].Start = &state.StartReceipt{OperationID: handoffTestOp, PromptOperationID: handoffPromptID(handoffTestOp)}
		}
	}
	return infos
}
func (f *handoffFake) SubmitMessage(_ context.Context, _ string, control proto.MessageControl, _ state.InputAttribution) (proto.MessageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends++
	if f.unknown {
		return proto.MessageResult{}, io.EOF
	}
	return proto.MessageResult{Accepted: true, Boundary: "provider"}, nil
}

func newHandoffFixture(t *testing.T) (testDaemon, *handoffFake, handoffRequest) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSIONS_STATE_DIR", filepath.Join(home, "runners"))
	t.Setenv("SESSIONS_LEDGER_PATH", filepath.Join(home, "lanes.sqlite3"))
	t.Setenv("SESSIONS_PORT", "8899")
	daemon := newTestDaemon(t)
	providerID := "11111111-2222-4333-8444-555555555555"
	info, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{Cmd: "claude", Kind: state.KindClaudeStructured, Cwd: daemon.root, ConversationID: providerID, Args: []string{"--session-id", providerID}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "projects", watch.EncodeClaudeCWD(daemon.root), providerID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	history := `{"type":"user","uuid":"a","message":{"role":"user","content":"The inherited fact is octopus-42. File reference: '/tmp/design notes.md'"}}` + "\n" +
		`{"type":"assistant","uuid":"b","message":{"role":"assistant","content":[{"type":"text","text":"I will remember the fact."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &handoffFake{sessionService: daemon.registry, registry: daemon.registry, config: daemon.config}
	daemon.handler.registry = fake
	return daemon, fake, handoffRequest{OperationID: handoffTestOp, SourceSessionID: info.ID, DestinationProvider: "codex", Model: "fixture-astra"}
}

func handoffHTTP(t *testing.T, s *Server, method, path string, body any) handoffView {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = strings.NewReader(string(data))
	}
	response := serve(t, s, method, path, reader, "127.0.0.1:1", nil)
	if response.Code != 200 && response.Code != 202 {
		t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
	}
	var view handoffView
	decodeBody(t, response, &view)
	return view
}

func waitHandoff(t *testing.T, s *Server) handoffView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view := handoffHTTP(t, s, "GET", "/api/handoffs/"+handoffTestOp, nil)
		if view.Creation != "pending" {
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("handoff stayed pending")
	return handoffView{}
}

func TestHandoffPreviewCommitRestartAndFirstPrompt(t *testing.T) {
	daemon, fake, body := newHandoffFixture(t)
	preview := handoffHTTP(t, daemon.handler, "POST", "/api/handoffs/preview", body)
	if preview.Coverage.Included != 2 || !strings.Contains(preview.Coverage.Limitation, "Binary") || fake.creates != 0 {
		t.Fatalf("preview %+v", preview)
	}
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	view := waitHandoff(t, daemon.handler)
	if view.TargetSessionID != handoffTarget || view.Context != "applied" || view.Prompt.Status != "not-sent" || view.Response != "not-observed" {
		t.Fatalf("view %+v", view)
	}
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	restarted := New(daemon.config, fake)
	handoffHTTP(t, restarted, "POST", "/api/handoffs", body)
	if fake.creates != 1 || fake.sends != 0 {
		t.Fatalf("creates %d sends %d", fake.creates, fake.sends)
	}
	if !strings.Contains(fake.artifact.Messages[0].Text, "octopus-42") || !strings.Contains(fake.artifact.Messages[0].Text, "'/tmp/design notes.md'") {
		t.Fatal("authored fact or file reference lost")
	}
	if source, ok := daemon.registry.Get(body.SourceSessionID); !ok || source.Info().Exited || source.Info().ReopenedAs != "" {
		t.Fatal("source mutated")
	}
	for i := 0; i < 2; i++ {
		result := serve(t, restarted, "POST", "/api/handoffs/"+handoffTestOp+"/prompt", strings.NewReader(`{"text":"Continue."}`), "127.0.0.1:1", nil)
		if result.Code != 200 {
			t.Fatalf("send %d %s", result.Code, result.Body.String())
		}
	}
	if fake.sends != 1 {
		t.Fatalf("first request sent %d times", fake.sends)
	}
	view = handoffHTTP(t, restarted, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Prompt.Status != "accepted" {
		t.Fatalf("prompt %+v", view.Prompt)
	}
	changed := body
	changed.Model = "different"
	encoded, _ := json.Marshal(changed)
	response := serve(t, restarted, "POST", "/api/handoffs", strings.NewReader(string(encoded)), "127.0.0.1:1", nil)
	if response.Code != 409 {
		t.Fatal("operation choices were mutable")
	}
}

func TestHandoffCrashBeforeCreateAndUnknownPromptNeverReplay(t *testing.T) {
	daemon, fake, body := newHandoffFixture(t)
	receipt, err := daemon.handler.prepareHandoff(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := daemon.handler.handoffPath(body.OperationID)
	if err := writeHandoff(path, receipt); err != nil {
		t.Fatal(err)
	}
	restarted := New(daemon.config, fake)
	view := handoffHTTP(t, restarted, "POST", "/api/handoffs", body)
	if view.Creation != "unknown" || fake.creates != 0 || view.Recovery == "" {
		t.Fatalf("crash %+v", view)
	}
	// Complete the already-recorded operation through the fake adapter to model
	// an authoritative later creation result, then lose a first-request ack.
	daemon.handler.runHandoff(path, receipt)
	fake.unknown = true
	for i := 0; i < 2; i++ {
		restarted = New(daemon.config, fake)
		response := serve(t, restarted, "POST", "/api/handoffs/"+handoffTestOp+"/prompt", strings.NewReader(`{"text":"Continue."}`), "127.0.0.1:1", nil)
		if response.Code != 200 {
			t.Fatalf("unknown send %d %s", response.Code, response.Body.String())
		}
	}
	if fake.sends != 1 {
		t.Fatalf("unknown prompt replayed %d times", fake.sends)
	}
	view = handoffHTTP(t, restarted, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Prompt.Status != "unknown" || view.Recovery == "" {
		t.Fatalf("unknown %+v", view)
	}
}

func TestHandoffBoundKeepsIntactUTF8AndDisclosesOmissions(t *testing.T) {
	all := []state.ContinuationMessage{{Role: "user", Text: strings.Repeat("猫", handoffTextBytes)}, {Role: "assistant", Text: "small fact"}}
	selected, coverage := boundedHandoffMessages(all)
	if len(selected) != 1 || selected[0].Text != "small fact" || coverage.Omitted != 1 || !utf8.ValidString(selected[0].Text) {
		t.Fatalf("%+v %+v", selected, coverage)
	}
}

func TestHandoffUncertainImportIsVisibleAndCannotSend(t *testing.T) {
	daemon, fake, body := newHandoffFixture(t)
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	waitHandoff(t, daemon.handler)
	path := state.For(daemon.config.RunnerStateDir, handoffTarget).Continuation
	c, err := state.ReadContinuation(path)
	if err != nil {
		t.Fatal(err)
	}
	c.ProviderContext = "applying"
	if err := state.WriteContinuation(path, c); err != nil {
		t.Fatal(err)
	}
	restarted := New(daemon.config, fake)
	view := handoffHTTP(t, restarted, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Context != "unknown" || view.NativeTargetID != "fake-native" || view.Recovery == "" {
		t.Fatalf("uncertain %+v", view)
	}
	response := serve(t, restarted, "POST", "/api/handoffs/"+handoffTestOp+"/prompt", strings.NewReader(`{"text":"Continue."}`), "127.0.0.1:1", nil)
	if response.Code != 409 || fake.sends != 0 {
		t.Fatalf("ambiguous import sent %d %s", response.Code, response.Body.String())
	}
}

func TestHandoffResponseRequiresNonImportedAssistantEvidence(t *testing.T) {
	daemon, _, body := newHandoffFixture(t)
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	waitHandoff(t, daemon.handler)
	response := serve(t, daemon.handler, "POST", "/api/handoffs/"+handoffTestOp+"/prompt", strings.NewReader(`{"text":"Continue."}`), "127.0.0.1:1", nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	target, _ := daemon.registry.Get(handoffTarget)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	target.SetIdleResult(state.IdleReasonCompleted, "", "", time.Now().UnixMilli())
	target.RecordClaudeEvent(json.RawMessage(`{"type":"assistant","subtype":"imported_message","timestamp":"` + at + `","message":{"content":"source answer"}}`))
	view := handoffHTTP(t, daemon.handler, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Response != "not-observed" {
		t.Fatal("imported history/idle became generated reply")
	}
	target.RecordClaudeEvent(json.RawMessage(`{"type":"assistant","timestamp":"` + at + `","message":{"content":"fake target reply"}}`))
	view = handoffHTTP(t, daemon.handler, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Response != "assistant-observed" {
		t.Fatalf("reply not observed: %+v", view)
	}
}

func TestHandoffRefusesTargetArtifactMismatch(t *testing.T) {
	daemon, fake, body := newHandoffFixture(t)
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	waitHandoff(t, daemon.handler)
	path := state.For(daemon.config.RunnerStateDir, handoffTarget).Continuation
	c, err := state.ReadContinuation(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Messages[0].Text = "another conversation"
	if err := state.WriteContinuation(path, c); err != nil {
		t.Fatal(err)
	}
	view := handoffHTTP(t, daemon.handler, "GET", "/api/handoffs/"+handoffTestOp, nil)
	if view.Context != "unknown" || !strings.Contains(view.Error, "differs") {
		t.Fatalf("mismatch %+v", view)
	}
	response := serve(t, daemon.handler, "POST", "/api/handoffs/"+handoffTestOp+"/prompt", strings.NewReader(`{"text":"Continue."}`), "127.0.0.1:1", nil)
	if response.Code != 409 || fake.sends != 0 {
		t.Fatal("sent to mismatched target")
	}
}

func TestHandoffCodexToClaudeStagesWithoutHiddenTurn(t *testing.T) {
	daemon, fake, body := newHandoffFixture(t)
	providerID := "77777777-7777-4777-8777-777777777777"
	source, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{Cmd: "codex", Kind: state.KindCodexAppServer, Cwd: daemon.root, ConversationID: providerID})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(source.CreatedAt)
	path := filepath.Join(os.Getenv("HOME"), ".codex", "sessions", at.Format("2006"), at.Format("01"), at.Format("02"), "rollout-"+providerID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":%q,"timestamp":%q}}
{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The inherited fact is octopus-42."}]}}
`, at.Format(time.RFC3339Nano), providerID, daemon.root, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	body.SourceSessionID, body.DestinationProvider, body.Model = source.ID, "claude", ""
	preview := handoffHTTP(t, daemon.handler, "POST", "/api/handoffs/preview", body)
	if preview.Artifact.DestinationProvider != "claude" || preview.Artifact.Mode != state.ContinuationLinkedSearch || !preview.Artifact.Handoff {
		t.Fatalf("reverse preview %+v", preview)
	}
	handoffHTTP(t, daemon.handler, "POST", "/api/handoffs", body)
	view := waitHandoff(t, daemon.handler)
	if view.Context != "staged-for-first-request" || fake.sends != 0 || view.Prompt.Status != "not-sent" {
		t.Fatalf("reverse switch %+v", view)
	}
	if !strings.Contains(fake.artifact.Messages[0].Text, "octopus-42") {
		t.Fatal("reverse source context lost")
	}
}

func TestHandoffDisclosesUnreadableSourceRecords(t *testing.T) {
	daemon, _, body := newHandoffFixture(t)
	path := filepath.Join(os.Getenv("HOME"), ".claude", "projects", watch.EncodeClaudeCWD(daemon.root), "11111111-2222-4333-8444-555555555555.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("{broken record}\n")
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	preview := handoffHTTP(t, daemon.handler, "POST", "/api/handoffs/preview", body)
	if !strings.Contains(preview.Coverage.Limitation, "skipped 1 unreadable") {
		t.Fatalf("source loss hidden: %+v", preview.Coverage)
	}
}

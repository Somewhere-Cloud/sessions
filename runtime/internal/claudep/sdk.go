package claudep

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SDKOptions selects an explicit bridge. It is not a fallback for a failed
// print-mode send: switching after uncertain delivery could run work twice.
type SDKOptions struct {
	NodePath, BridgePath, ModulePath string
	ClaudePath                       string
	Env                              []string
	// Observe receives transport/background facts, never foreground completion.
	Observe func(json.RawMessage)
}

type SDKClient struct {
	mu          sync.Mutex
	writeMu     sync.Mutex
	options     SDKOptions
	input       io.WriteCloser
	command     *exec.Cmd
	active      *sdkTurn
	initialized chan error
	done        chan struct{}
	closed      bool
	failed      error
	session     string
	config      TurnOptions
}

type sdkTurn struct {
	id          string
	operationID string
	events      chan Event
	state       *turnState
}

type sdkFrame struct {
	Type   string          `json:"type"`
	ID     string          `json:"operation_id"`
	Error  string          `json:"error"`
	Phase  string          `json:"phase"`
	Event  json.RawMessage `json:"event"`
	Result json.RawMessage `json:"result"`
}

func NewSDKClient(options SDKOptions) (*SDKClient, error) {
	if options.BridgePath == "" {
		return nil, errors.New("Claude SDK bridge path is required")
	}
	if options.NodePath == "" {
		options.NodePath = "node"
	}
	if options.ClaudePath == "" {
		path, err := exec.LookPath("claude")
		if err != nil {
			return nil, err
		}
		options.ClaudePath = path
	}
	if options.Env == nil {
		options.Env = os.Environ()
	}
	options.Env = withoutAnthropicAPIKey(options.Env)
	return &SDKClient{options: options, initialized: make(chan error, 1), done: make(chan struct{})}, nil
}

// SendTurn reuses one SDK query. Turn cancellation requests interrupt(), never
// cancels the provider process. Completion waits for a result naming this send.
func (c *SDKClient) SendTurn(ctx context.Context, prompt string, options TurnOptions) (*TurnStream, error) {
	if prompt == "" || options.SessionID == "" {
		return nil, errors.New("Claude prompt and session id are required")
	}
	c.mu.Lock()
	if c.closed || c.failed != nil || c.active != nil {
		c.mu.Unlock()
		return nil, errors.New("Claude SDK unavailable or turn still active; reconcile before retry")
	}
	first := c.command == nil
	changed := !first && (c.config.Model != options.Model || sdkEffort(c.config.ExtraArgs) != sdkEffort(options.ExtraArgs))
	if !first && !sameSDKConfiguration(c.config, options) {
		c.mu.Unlock()
		return nil, errors.New("Claude SDK configuration changed; explicit session reconfiguration is required")
	}
	if first {
		if err := c.startLocked(options); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	// Each explicit attempt needs its own provider UUID. The durable queue owns
	// operation deduplication; a failed attempt must not suppress an approved retry.
	id, err := NewSessionID()
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	turn := &sdkTurn{id: id, operationID: options.OperationID, events: make(chan Event, 128), state: newTurnState()}
	c.active = turn
	c.config = options
	c.mu.Unlock()
	if first {
		select {
		case err = <-c.initialized:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		command := map[string]any{"type": "submit", "operation_id": id, "text": prompt}
		if changed {
			command["settings"] = map[string]any{"model": sdkNullable(options.Model), "effortLevel": sdkNullable(sdkEffort(options.ExtraArgs))}
		}
		err = c.write(command)
	}
	if err != nil {
		c.fail(err)
	}
	go c.interruptOnCancel(ctx, turn)
	return &TurnStream{Events: turn.events, state: turn.state}, nil
}

func sameSDKConfiguration(a, b TurnOptions) bool {
	// Resume changes after the initial send; query identity does not.
	a.Resume, b.Resume = false, false
	a.OperationID, b.OperationID = "", ""
	a.Model, b.Model = "", ""
	a.ExtraArgs, b.ExtraArgs = sdkStableArgs(a.ExtraArgs), sdkStableArgs(b.ExtraArgs)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func sdkNullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func sdkEffort(args []string) string {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--effort=") {
			return strings.TrimPrefix(arg, "--effort=")
		}
		if arg == "--effort" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func sdkStableArgs(args []string) []string {
	var stable []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--model" || arg == "--effort" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--model=") || strings.HasPrefix(arg, "--effort=") {
			continue
		}
		stable = append(stable, arg)
	}
	return stable
}

func (c *SDKClient) startLocked(options TurnOptions) error {
	args := []string{c.options.BridgePath}
	if c.options.ModulePath != "" {
		args = append(args, c.options.ModulePath)
	}
	command := exec.Command(c.options.NodePath, args...)
	command.Dir, command.Env, command.Stderr = options.CWD, c.options.Env, os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err = command.Start(); err != nil {
		input.Close()
		return err
	}
	c.command, c.input, c.session, c.config = command, input, options.SessionID, options
	go c.read(output, command)
	return c.write(map[string]any{"type": "init", "options": sdkQueryOptions(c.options.ClaudePath, options)})
}

func (c *SDKClient) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.input).Encode(value)
}

func (c *SDKClient) interruptOnCancel(ctx context.Context, turn *sdkTurn) {
	select {
	case <-turn.state.done:
		return
	case <-ctx.Done():
	}
	c.mu.Lock()
	active := c.active == turn
	c.mu.Unlock()
	if active {
		if err := c.write(map[string]any{"type": "interrupt", "operation_id": turn.id}); err != nil {
			c.fail(err)
		}
	}
}

func (c *SDKClient) read(output io.Reader, command *exec.Cmd) {
	defer close(c.done)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 65536), scannerBuffer)
	for scanner.Scan() {
		var frame sdkFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			c.fail(err)
			_ = command.Process.Kill() // Only this client's malformed bridge.
			break
		}
		c.handleSDKFrame(frame, append(json.RawMessage(nil), scanner.Bytes()...))
	}
	readErr := scanner.Err()
	if readErr != nil {
		_ = command.Process.Kill()
	}
	err := command.Wait()
	if err == nil {
		err = errors.New("Claude SDK bridge exited")
	}
	c.fail(errors.Join(readErr, err))
}

func (c *SDKClient) handleSDKFrame(frame sdkFrame, raw json.RawMessage) {
	if frame.Type == "initialized" {
		c.initialized <- nil
		return
	}
	if frame.Type == "transport_error" || frame.Type == "command_error" {
		if c.options.Observe != nil {
			c.options.Observe(raw)
		}
		c.fail(errors.New(frame.Error))
		return
	}
	c.mu.Lock()
	turn := c.active
	associated := turn != nil && frame.ID == turn.id
	// Unstamped content and background records remain visible, but only a
	// matched settlement can close a foreground stream.
	content := frame.Type == "event" && !sdkIsResult(frame.Event)
	if turn == nil || (!associated && !content) {
		c.mu.Unlock()
		if c.options.Observe != nil {
			c.options.Observe(raw)
		}
		return
	}
	defer c.mu.Unlock()
	if frame.Type == "event" {
		// Only the settled frame carries a result into the per-turn stream.
		var value struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(frame.Event, &value)
		if value.Type == "result" {
			return
		}
		event, err := parseSDKEvent(frame.Event, c.session)
		if err == nil {
			if associated {
				event.Raw = stampSDKOperation(event.Raw, turn.operationID)
			}
			turn.events <- event
		}
	} else if frame.Type == "settled" {
		c.finishSDKTurn(turn, frame)
	}
}

func sdkIsResult(raw json.RawMessage) bool {
	var value struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Type == "result"
}

func (c *SDKClient) finishSDKTurn(turn *sdkTurn, frame sdkFrame) {
	// Keep original provider result as evidence, but translate its explicit
	// interrupted/error outcome for existing lifecycle readers.
	var native map[string]any
	_ = json.Unmarshal(frame.Result, &native)
	if _, ok := native["result"].(string); !ok {
		native["result"] = fmt.Sprint(native["errors"])
	}
	native["is_error"] = frame.Phase == "failed"
	native["transport"] = "agent-sdk"
	encoded, _ := json.Marshal(native)
	event, err := parseSDKEvent(encoded, c.session)
	result := TurnResult{SessionID: c.session}
	if err == nil {
		event.Raw = stampSDKOperation(event.Raw, turn.operationID)
		result.Message, result.Usage, result.Subtype = event.Message, event.Usage, event.Subtype
		result.IsError = frame.Phase != "answered"
		result.Interrupted = frame.Phase == "interrupted"
		if !result.Interrupted {
			turn.events <- event
		}
	}
	if result.IsError {
		err = fmt.Errorf("Claude SDK turn %s", frame.Phase)
	}
	turn.state.finish(result, err)
	close(turn.events)
	c.active = nil
}

func (c *SDKClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = err
	select {
	case c.initialized <- err:
	default:
	}
	if c.active != nil {
		c.active.state.finish(TurnResult{SessionID: c.session}, err)
		close(c.active.events)
		c.active = nil
	}
}

func (c *SDKClient) Close() error {
	c.mu.Lock()
	c.closed = true
	input := c.input
	command := c.command
	c.mu.Unlock()
	if input == nil {
		return nil
	}
	c.writeMu.Lock()
	err := input.Close()
	c.writeMu.Unlock()
	select {
	case <-c.done:
		return err
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-c.done
		return errors.Join(err, errors.New("SDK bridge did not close within five seconds"))
	}
}

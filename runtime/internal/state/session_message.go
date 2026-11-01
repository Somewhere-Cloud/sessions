package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func (s *Session) SubmitMessage(ctx context.Context, control proto.MessageControl) (proto.MessageResult, error) {
	s.mu.RLock()
	runner, supported := s.runner.(proto.MessageRunner)
	available := s.info.MessageSubmit && !s.info.Exited && !s.info.Unreachable
	s.mu.RUnlock()
	if !supported || !available {
		return proto.MessageResult{}, errors.New("session does not support acknowledged messages right now")
	}
	return runner.SubmitMessage(ctx, control)
}

// lateMessageRunner is a runner client that remembers an acknowledgment whose
// caller had already given up. It is an implementation detail of the daemon's
// side of the connection, not a runner capability: a client type that does not
// keep one simply has nothing to report, and no runner binary is asked for
// anything new.
type lateMessageRunner interface {
	LateMessageResult(string) (proto.MessageResult, bool)
}

// LateMessageResult reports what the runner said about an operation after the
// request that started it was interrupted, as far as this daemon's live
// connection to it still remembers. It never speaks for the runner: an
// unanswered operation, a replaced connection, and a restarted daemon all
// return nothing rather than a guess.
func (s *Session) LateMessageResult(operationID string) (proto.MessageResult, bool) {
	s.mu.RLock()
	runner, supported := s.runner.(lateMessageRunner)
	s.mu.RUnlock()
	if !supported || operationID == "" {
		return proto.MessageResult{}, false
	}
	return runner.LateMessageResult(operationID)
}

// ControlMessageQueue forwards one saved-message request to a structured
// Claude runner that advertised the capability. Codex keeps no runner queue:
// a message sent while it works steers the active turn.
func (s *Session) ControlMessageQueue(ctx context.Context, control proto.QueueControl) (proto.QueueResult, error) {
	s.mu.RLock()
	runner, supported := s.runner.(proto.QueueRunner)
	info := s.info
	s.mu.RUnlock()
	switch {
	case info.Kind == KindCodexAppServer:
		return proto.QueueResult{}, fmt.Errorf("%w: Codex keeps no saved messages; a message sent while it works steers the active turn", ErrQueueUnsupported)
	case info.Kind != KindClaudeStructured:
		return proto.QueueResult{}, fmt.Errorf("%w: only Rich Claude sessions save messages for the next turn", ErrQueueUnsupported)
	case info.Exited:
		return proto.QueueResult{}, ErrSessionEnded
	case !supported || !info.MessageQueue:
		return proto.QueueResult{}, fmt.Errorf("%w: this Claude runner predates queue changes; resume the conversation with the current Sessions runtime", ErrRunnerProtocol)
	case info.Unreachable:
		return proto.QueueResult{}, fmt.Errorf("%w: its runner is unreachable right now", ErrQueueUnsupported)
	}
	return runner.ControlQueue(ctx, control)
}

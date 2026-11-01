package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	// setAsideWakeInterval bounds how late a set-aside session comes back.
	setAsideWakeInterval = 15 * time.Second
	// setAsideMaxUntil bounds a return time, so a typo cannot hide a session
	// for decades.
	setAsideMaxUntil = 366 * 24 * time.Hour
)

type setAsideUntilService interface {
	UpdateSetAsideUntil(string, bool, *int64) (*int64, error)
}

type setAsideRequest struct {
	SetAside bool `json:"setAside"`
	// Until is when the daemon brings the session back, in Unix ms.
	Until *int64 `json:"until"`
	// UntilLimitReset brings it back when its usage-limit resume fires.
	UntilLimitReset bool `json:"untilLimitReset"`
}

// handleSetAside serves PUT /api/sessions/:id/set-aside. An optional return
// time makes it a snooze: the daemon brings the session back at that time,
// persistently, and any input brings it back sooner.
func (s *Server) handleSetAside(response http.ResponseWriter, request *http.Request, id, corsOrigin string) {
	var body setAsideRequest
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	until, status, err := s.setAsideUntil(id, body)
	if err != nil {
		s.sendJSON(response, status, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	workingSet, supported := s.registry.(setAsideUntilService)
	if !supported {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "session working-set organization is not available on this runtime"}, corsOrigin)
		return
	}
	setAsideAt, err := workingSet.UpdateSetAsideUntil(id, body.SetAside, until)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, state.ErrSessionNotFound):
			status = http.StatusNotFound
		case errors.Is(err, state.ErrSessionEnded):
			status = http.StatusConflict
		}
		s.sendJSON(response, status, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, map[string]any{"setAsideAt": setAsideAt, "setAsideUntil": until}, corsOrigin)
}

func (s *Server) setAsideUntil(id string, body setAsideRequest) (*int64, int, error) {
	if body.Until != nil && body.UntilLimitReset {
		return nil, http.StatusBadRequest, errors.New("send until or untilLimitReset, not both")
	}
	if !body.SetAside {
		if body.Until != nil || body.UntilLimitReset {
			return nil, http.StatusBadRequest, errors.New("a return time applies only when setting a session aside")
		}
		return nil, 0, nil
	}
	if body.UntilLimitReset {
		at, ok := s.limitResumeAt(id)
		if !ok {
			return nil, http.StatusConflict, errors.New("no usage-limit resume is scheduled for this session; `sessions limits status` shows why")
		}
		return &at, 0, nil
	}
	if body.Until == nil {
		return nil, 0, nil
	}
	now := time.Now()
	at := time.UnixMilli(*body.Until)
	if !at.After(now) {
		return nil, http.StatusBadRequest, errors.New("the return time has already passed; choose a later time")
	}
	if at.After(now.Add(setAsideMaxUntil)) {
		return nil, http.StatusBadRequest, errors.New("the return time is more than a year away; set the session aside without one instead")
	}
	until := *body.Until
	return &until, 0, nil
}

// StartSetAsideWake brings set-aside sessions back when their return time
// passes. The time is in session metadata, so a return that fell due while
// the daemon was stopped happens at the next start.
func (s *Server) StartSetAsideWake(ctx context.Context, logf func(string, ...any)) {
	go func() {
		ticker := time.NewTicker(setAsideWakeInterval)
		defer ticker.Stop()
		for {
			if err := s.setAsideWakeTick(time.Now()); err != nil && logf != nil {
				logf("[set-aside] %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) setAsideWakeTick(now time.Time) error {
	organizer, ok := s.registry.(workingSetService)
	if !ok {
		return nil
	}
	var first error
	for _, info := range s.registry.List(false) {
		if info.Exited || info.SetAsideAt == nil || info.SetAsideUntil == nil || *info.SetAsideUntil > now.UnixMilli() {
			continue
		}
		if _, err := organizer.UpdateSetAside(info.ID, false); err != nil && first == nil {
			first = fmt.Errorf("bring back %s: %w", info.ID, err)
		}
	}
	return first
}

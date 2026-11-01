package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReadState is what the daemon stored for one session's unread mark.
type ReadState struct {
	Unread      bool   `json:"unread"`
	SeenAt      *int64 `json:"seenAt"`
	AttentionAt *int64 `json:"attentionAt"`
}

// SessionUnread is the single rule every client and the CLI read as unread.
//
// A session is unread when a person marked it so, or when the daemon reported
// that it finished a turn, failed, or needs input after the person last saw it.
// "Last saw it" is the later of the last time a client opened it and the last
// time a person typed into it, because typing into a session is looking at it.
//
// A session nobody has ever opened or typed into is never unread by attention
// alone. That is the sessions an agent started for its own work: they report to
// the agent that started them, and marking every one of them unread would turn
// the sidebar into a wall of bold rows the person never asked to follow. An
// ended session is never unread; there is nothing left in it to catch up on
// that History does not already hold.
func SessionUnread(info SessionInfo) bool {
	if info.Exited {
		return false
	}
	if info.MarkedUnread {
		return true
	}
	if info.AttentionAt == nil {
		return false
	}
	baseline := latestMillis(info.SeenAt, info.LastHumanMessageAt)
	if baseline == nil {
		return false
	}
	return *info.AttentionAt > *baseline
}

func latestMillis(values ...*int64) *int64 {
	var latest *int64
	for _, value := range values {
		if value != nil && (latest == nil || *value > *latest) {
			latest = value
		}
	}
	return latest
}

// UpdateReadState records a person's read decision. unread=false stamps the
// session seen now and clears any explicit mark; unread=true sets the explicit
// mark and leaves the seen time alone. The metadata file is written first, so
// an acknowledged change survives a daemon restart and syncs to every client
// that lists this daemon.
func (r *Registry) UpdateReadState(id string, unread bool) (ReadState, error) {
	if !validMetadataID(id) {
		return ReadState{}, fmt.Errorf("%w: session %s", ErrSessionNotFound, id)
	}
	session, live := r.Get(id)
	path := filepath.Join(r.config.RunnerStateDir, id+".json")
	now := time.Now().UnixMilli()
	stored, err := updateMetadata(path, func(metadata *Metadata) error {
		metadata.MarkedUnread = unread
		if !unread {
			metadata.SeenAt = &now
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ReadState{}, fmt.Errorf("%w: session %s", ErrSessionNotFound, id)
		}
		return ReadState{}, fmt.Errorf("persist session read state: %w", err)
	}
	if !live {
		return ReadState{Unread: stored.MarkedUnread, SeenAt: stored.SeenAt, AttentionAt: stored.AttentionAt}, nil
	}
	session.setReadState(stored.SeenAt, stored.AttentionAt, stored.MarkedUnread)
	info := session.Info()
	return ReadState{Unread: info.Unread, SeenAt: info.SeenAt, AttentionAt: info.AttentionAt}, nil
}

// RecordAttention stamps the moment the daemon reported that a session needs
// a person to look at it. It is a fact about the session, not about any
// client, so it is persisted with the session rather than per device.
func (r *Registry) RecordAttention(id string, at int64) error {
	if !validMetadataID(id) {
		return fmt.Errorf("%w: session %s", ErrSessionNotFound, id)
	}
	session, live := r.Get(id)
	path := filepath.Join(r.config.RunnerStateDir, id+".json")
	stored, err := updateMetadata(path, func(metadata *Metadata) error {
		if metadata.AttentionAt == nil || *metadata.AttentionAt < at {
			metadata.AttentionAt = &at
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("persist session attention: %w", err)
	}
	if live {
		session.setReadState(stored.SeenAt, stored.AttentionAt, stored.MarkedUnread)
	}
	return nil
}
